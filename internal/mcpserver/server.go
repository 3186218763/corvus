// Package mcpserver exposes Corvus's built-in tools as a Model Context
// Protocol (MCP) server over the stdio transport: newline-delimited
// JSON-RPC 2.0 with exactly one JSON message per line and no embedded
// newlines, matching the framing the MCP stdio client in
// internal/plugin/transport_stdio.go speaks.
//
// The server implements the 2024-11-05 protocol surface needed by IDEs and
// other MCP hosts: initialize, notifications/initialized, ping, tools/list,
// and tools/call. Messages are processed sequentially in Serve (one request
// at a time, in arrival order); a long-running tool call therefore blocks
// later messages, which is safe and simple for a stdio server whose client
// is normally request/response anyway. Tool calls do not run concurrently,
// so tools never observe parallel execution from this server.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"corvus/internal/permission"
	"corvus/internal/tool"
)

// Protocol identity advertised in initialize results.
const (
	ProtocolVersion = "2024-11-05"
	ServerName      = "corvus-mcp-server"
	ServerVersion   = "0.1.0"
)

// maxLineBytes caps a single stdio line. Tool schemas can be large, so the
// default bufio.Scanner limit (64 KiB) is too small; 1 MiB is plenty for any
// built-in schema and still bounds a hostile client's memory use.
const maxLineBytes = 1 << 20

// JSON-RPC 2.0 error codes (MCP adds -32002 on top of the JSON-RPC set).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32000
	codeNotInitialized = -32002
)

// Server is an MCP stdio server bound to a fixed tool set and permission
// policy. The tool set and policy are immutable after New; the only mutable
// state is the initialize handshake flag, guarded by mu so Serve calls (even
// on different readers) never race it.
type Server struct {
	tools  []tool.Tool // sorted by name, deduplicated
	byName map[string]tool.Tool
	policy permission.Policy

	mu          sync.Mutex
	initialized bool
}

// New returns a Server serving tools under policy. Duplicate tool names keep
// the first occurrence (so a caller can layer ConfineWebFetch/ConfineBash
// over a Workspace set and the Workspace-bound instances win); tools are
// listed in name order in tools/list.
func New(tools []tool.Tool, policy permission.Policy) *Server {
	seen := make(map[string]bool, len(tools))
	uniq := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		name := t.Name()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		uniq = append(uniq, t)
	}
	sort.Slice(uniq, func(i, j int) bool { return uniq[i].Name() < uniq[j].Name() })
	byName := make(map[string]tool.Tool, len(uniq))
	for _, t := range uniq {
		byName[t.Name()] = t
	}
	return &Server{tools: uniq, byName: byName, policy: policy}
}

// Serve reads newline-delimited JSON-RPC messages from r and writes one
// JSON-RPC response line per request to w until r is exhausted (EOF or a
// scanner error) or ctx is cancelled. Notifications (messages without an id)
// never produce a response, even for unknown methods, per JSON-RPC 2.0.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLineBytes)
	enc := json.NewEncoder(w)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		resp := s.handle(ctx, line)
		if resp == nil {
			continue // notification: no response
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("mcpserver: write response: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("mcpserver: read: %w", err)
	}
	return nil
}

// request is the JSON-RPC envelope a client sends. ID is kept raw so it can
// be echoed back byte-for-byte (string, number, or null).
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func resultResponse(id json.RawMessage, result any) *response {
	return &response{JSONRPC: "2.0", ID: id, Result: result}
}

func errorResponse(id json.RawMessage, code int, message string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

// handle processes one line and returns the response to write, or nil when
// the message is a notification and JSON-RPC forbids a response.
func (s *Server) handle(ctx context.Context, line []byte) *response {
	// A top-level array is a JSON-RPC batch; MCP transports one message per
	// line and batches are not supported here.
	if len(line) > 0 && line[0] == '[' {
		return errorResponse(nil, codeInvalidRequest, "Invalid Request: batch requests are not supported")
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return errorResponse(nil, codeParseError, "Parse error: invalid JSON payload")
	}
	if req.JSONRPC != "2.0" {
		return errorResponse(req.ID, codeInvalidRequest, "Invalid Request: jsonrpc must be \"2.0\"")
	}
	if req.Method == "" {
		return errorResponse(req.ID, codeInvalidRequest, "Invalid Request: missing method")
	}
	// A missing or null id marks a notification. JSON-RPC 2.0 requires no
	// response to notifications, including error responses.
	if len(req.ID) == 0 || bytes.Equal(bytes.TrimSpace(req.ID), []byte("null")) {
		return nil
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req.ID, req.Params)
	case "notifications/initialized", "notifications/cancelled":
		// These are notifications; a client sending one with an id is a
		// protocol violation. Acknowledge it so the client's request/response
		// bookkeeping stays consistent instead of timing out.
		return resultResponse(req.ID, map[string]any{})
	case "ping":
		return resultResponse(req.ID, map[string]any{})
	case "tools/list":
		return s.handleToolsList(req.ID)
	case "tools/call":
		return s.handleToolsCall(ctx, req.ID, req.Params)
	default:
		return errorResponse(req.ID, codeMethodNotFound, fmt.Sprintf("Method not found: %q", req.Method))
	}
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	// capabilities and clientInfo are accepted and ignored: this server
	// exposes only the tools capability and does not consume client features.
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools toolCapabilities `json:"tools"`
}

type toolCapabilities struct {
	// ListChanged is false: the tool list is fixed for the process lifetime,
	// so the client never needs a tools/list_changed notification.
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (s *Server) handleInitialize(id json.RawMessage, params json.RawMessage) *response {
	var p initializeParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return errorResponse(id, codeInvalidParams, "Invalid params: initialize parameters must be an object")
		}
	}
	// Accept the supported version or an absent one. An explicit unsupported
	// version is rejected rather than negotiated down: failing loudly beats a
	// client that assumes a newer protocol shape.
	if p.ProtocolVersion != "" && p.ProtocolVersion != ProtocolVersion {
		return errorResponse(id, codeInvalidParams,
			fmt.Sprintf("Unsupported protocol version %q; this server supports %q", p.ProtocolVersion, ProtocolVersion))
	}
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()
	return resultResponse(id, initializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    capabilities{Tools: toolCapabilities{ListChanged: false}},
		ServerInfo:      serverInfo{Name: ServerName, Version: ServerVersion},
	})
}

type toolListItem struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []toolListItem `json:"tools"`
}

func (s *Server) handleToolsList(id json.RawMessage) *response {
	items := make([]toolListItem, 0, len(s.tools))
	for _, t := range s.tools {
		items = append(items, toolListItem{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		})
	}
	return resultResponse(id, toolsListResult{Tools: items})
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type callResult struct {
	Content []contentItem `json:"content"`
	// IsError is omitted when false (the MCP default).
	IsError bool `json:"isError,omitempty"`
}

type contentItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func textContent(text string) []contentItem {
	return []contentItem{{Type: "text", Text: text}}
}

func (s *Server) handleToolsCall(ctx context.Context, id json.RawMessage, params json.RawMessage) *response {
	// -32002 is the MCP-defined "Server not initialized" code: tools/call
	// requires the initialize handshake to have completed.
	if !s.isInitialized() {
		return errorResponse(id, codeNotInitialized, "Server not initialized")
	}
	var p callParams
	if err := json.Unmarshal(params, &p); err != nil {
		return errorResponse(id, codeInvalidParams, "Invalid params: tools/call parameters must be an object")
	}
	if strings.TrimSpace(p.Name) == "" {
		return errorResponse(id, codeInvalidParams, "Invalid params: tools/call requires a tool name")
	}
	t, ok := s.byName[p.Name]
	if !ok {
		return resultResponse(id, callResult{IsError: true, Content: textContent(fmt.Sprintf("Unknown tool: %s", p.Name))})
	}
	args := p.Arguments
	if len(args) == 0 {
		args = json.RawMessage("{}") // absent arguments behave like an empty object
	}
	switch s.policy.Decide(p.Name, t.ReadOnly(), args) {
	case permission.Allow:
		text, err := t.Execute(ctx, args)
		if err != nil {
			// Tool failures surface in the result (isError) rather than as a
			// JSON-RPC error, per the MCP spec.
			return resultResponse(id, callResult{IsError: true, Content: textContent(fmt.Sprintf("Tool %s failed: %v", p.Name, err))})
		}
		return resultResponse(id, callResult{Content: textContent(text)})
	case permission.Ask:
		// Fail closed: this headless server has no interactive approver, so an
		// Ask decision is a denial. The message never echoes the call's
		// (possibly sensitive) arguments.
		return resultResponse(id, callResult{IsError: true,
			Content: textContent(fmt.Sprintf("Tool call %q requires approval, which is not available on this server", p.Name))})
	default: // permission.Deny
		return resultResponse(id, callResult{IsError: true,
			Content: textContent(fmt.Sprintf("Tool call %q was denied by policy", p.Name))})
	}
}

func (s *Server) isInitialized() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}
