// Command corvus-plugin-example is a reference MCP server used by Corvus's
// end-to-end plugin tests and as a template for authoring new plugins.
//
// It speaks the Model Context Protocol over stdio (newline-delimited JSON-RPC
// 2.0, one message per line) and exposes:
//
//   - tools: echo and wordcount (both annotated readOnlyHint)
//   - prompt: review (one required "path" argument)
//   - resource: doc://style-guide
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const protocolVersion = "2024-11-05"

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func isNotification(id json.RawMessage) bool {
	id = []byte(strings.TrimSpace(string(id)))
	return len(id) == 0 || string(id) == "null"
}

func respond(out *bufio.Writer, id json.RawMessage, result any, rpcErr *rpcError) {
	b, err := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result, Error: rpcErr})
	if err != nil {
		return
	}
	_, _ = out.Write(append(b, '\n'))
	out.Flush()
}

func handle(req rpcRequest) (result any, rpcErr *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"prompts":   map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]any{"name": "corvus-plugin-example", "version": "0.1.0"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return toolCatalog(), nil
	case "tools/call":
		return callTool(req.Params)
	case "prompts/list":
		return map[string]any{"prompts": []any{reviewPrompt()}}, nil
	case "prompts/get":
		return getPrompt(req.Params)
	case "resources/list":
		return map[string]any{"resources": []any{styleGuideResource()}}, nil
	case "resources/read":
		return readResource(req.Params)
	default:
		return nil, &rpcError{Code: -32601, Message: "Method not found"}
	}
}

func toolCatalog() map[string]any {
	inputSchema := map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
		"required":   []string{"text"},
	}
	annotations := map[string]any{"readOnlyHint": true}
	return map[string]any{"tools": []any{
		map[string]any{
			"name":        "echo",
			"description": "Echo the provided text back verbatim.",
			"inputSchema": inputSchema,
			"annotations": annotations,
		},
		map[string]any{
			"name":        "wordcount",
			"description": "Count the words in the provided text.",
			"inputSchema": inputSchema,
			"annotations": annotations,
		},
	}}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func callTool(raw json.RawMessage) (any, *rpcError) {
	var p callParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid tools/call params"}
	}
	args := map[string]json.RawMessage{}
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid tool arguments"}
		}
	}
	rawText, ok := args["text"]
	if !ok {
		return errorResult("missing required argument: text"), nil
	}
	var text string
	if err := json.Unmarshal(rawText, &text); err != nil {
		return errorResult(fmt.Sprintf("argument text must be a string, got %s", strings.TrimSpace(string(rawText)))), nil
	}
	switch p.Name {
	case "echo":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}, nil
	case "wordcount":
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("words: %d", len(strings.Fields(text)))}}}, nil
	default:
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown tool %q", p.Name)}
	}
}

func errorResult(message string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": message}},
		"isError": true,
	}
}

func reviewPrompt() map[string]any {
	return map[string]any{
		"name":        "review",
		"description": "Ask for a code review of a file.",
		"arguments": []any{
			map[string]any{"name": "path", "description": "File path to review", "required": true},
		},
	}
}

func getPrompt(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid prompts/get params"}
	}
	if p.Name != "review" {
		return nil, &rpcError{Code: -32602, Message: fmt.Sprintf("unknown prompt %q", p.Name)}
	}
	path := p.Arguments["path"]
	return map[string]any{"messages": []any{
		map[string]any{
			"role":    "user",
			"content": map[string]any{"type": "text", "text": fmt.Sprintf("Review the file at %s for style and correctness.", path)},
		},
	}}, nil
}

func styleGuideResource() map[string]any {
	return map[string]any{
		"uri":         "doc://style-guide",
		"name":        "Corvus Style Guide",
		"description": "Conventions for writing Corvus plugins.",
		"mimeType":    "text/plain",
	}
}

func readResource(raw json.RawMessage) (any, *rpcError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid resources/read params"}
	}
	if p.URI != "doc://style-guide" {
		return nil, &rpcError{Code: -32002, Message: fmt.Sprintf("unknown resource %q", p.URI)}
	}
	return map[string]any{"contents": []any{
		map[string]any{
			"uri":      p.URI,
			"mimeType": "text/plain",
			"text":     "Corvus style guide: keep tools read-only unless they mutate state.",
		},
	}}, nil
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if isNotification(req.ID) {
			continue
		}
		result, rpcErr := handle(req)
		respond(out, req.ID, result, rpcErr)
	}
}
