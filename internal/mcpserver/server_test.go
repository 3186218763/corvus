package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"corvus/internal/netclient"
	"corvus/internal/permission"
	"corvus/internal/tool"
	"corvus/internal/tool/builtin"
)

// fakeTool is a minimal tool.Tool used to drive server behaviour without
// depending on real built-ins.
type fakeTool struct {
	name        string
	description string
	schema      json.RawMessage
	readOnly    bool
	executeFn   func(ctx context.Context, args json.RawMessage) (string, error)
}

func (f fakeTool) Name() string        { return f.name }
func (f fakeTool) Description() string { return f.description }
func (f fakeTool) Schema() json.RawMessage {
	if len(f.schema) == 0 {
		return json.RawMessage(`{"type":"object"}`)
	}
	return f.schema
}
func (f fakeTool) ReadOnly() bool { return f.readOnly }
func (f fakeTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if f.executeFn != nil {
		return f.executeFn(ctx, args)
	}
	return "", fmt.Errorf("not implemented")
}

func echoTool(name string) fakeTool {
	return fakeTool{
		name:        name,
		description: "echoes its arguments",
		readOnly:    true,
		executeFn: func(_ context.Context, args json.RawMessage) (string, error) {
			return "echo:" + string(args), nil
		},
	}
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serve runs the server over input until EOF and returns every response line.
func serve(t *testing.T, srv *Server, input string) []rpcResponse {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var resps []rpcResponse
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r rpcResponse
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("unmarshal response line %q: %v", line, err)
		}
		resps = append(resps, r)
	}
	return resps
}

func newTestServer(tools ...tool.Tool) *Server {
	return New(tools, permission.New("deny", nil, nil, nil))
}

func initRequest(version string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":%q,"capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, version)
}

func TestInitializeHandshake(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	resps := serve(t, srv, initRequest("2024-11-05")+"\n")
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1", len(resps))
	}
	r := resps[0]
	if string(r.ID) != "1" || r.Error != nil {
		t.Fatalf("unexpected envelope: id=%s error=%+v", r.ID, r.Error)
	}
	var res struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(r.Result, &res); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if res.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocolVersion = %q, want %q", res.ProtocolVersion, ProtocolVersion)
	}
	if res.Capabilities.Tools.ListChanged {
		t.Error("capabilities.tools.listChanged = true, want false")
	}
	if res.ServerInfo.Name != ServerName || res.ServerInfo.Version != ServerVersion {
		t.Errorf("serverInfo = %+v, want name=%s version=%s", res.ServerInfo, ServerName, ServerVersion)
	}
}

func TestInitializeAcceptsEmptyProtocolVersion(t *testing.T) {
	srv := newTestServer()
	resps := serve(t, srv, initRequest("")+"\n")
	if len(resps) != 1 || resps[0].Error != nil {
		t.Fatalf("initialize with empty protocolVersion rejected: %+v", resps)
	}
}

func TestInitializeRejectsUnsupportedProtocolVersion(t *testing.T) {
	srv := newTestServer()
	resps := serve(t, srv, initRequest("2025-06-18")+"\n")
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeInvalidParams {
		t.Fatalf("want invalid params error, got %+v", resps)
	}
}

func TestToolsListIncludesSchema(t *testing.T) {
	flaky := fakeTool{
		name:        "flaky",
		description: "a flaky tool",
		schema:      json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`),
		readOnly:    true,
	}
	srv := newTestServer(echoTool("echo"), flaky)
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("tools/list failed: %+v", resps)
	}
	var res struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resps[1].Result, &res); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(res.Tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(res.Tools))
	}
	if res.Tools[0].Name != "echo" || res.Tools[1].Name != "flaky" {
		t.Errorf("tools not sorted by name: %v %v", res.Tools[0].Name, res.Tools[1].Name)
	}
	if res.Tools[1].Description != "a flaky tool" || !strings.Contains(string(res.Tools[1].InputSchema), `"required"`) {
		t.Errorf("schema/description missing: %+v", res.Tools[1])
	}
}

func TestToolsCallReadOnlyTool(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"hi"}}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("tools/call failed: %+v", resps)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resps[1].Result, &res); err != nil {
		t.Fatalf("unmarshal call result: %v", err)
	}
	if res.IsError || len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("unexpected call result: %+v", res)
	}
	if want := `echo:{"msg":"hi"}`; res.Content[0].Text != want {
		t.Errorf("text = %q, want %q", res.Content[0].Text, want)
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"nope"}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("unexpected envelope: %+v", resps)
	}
	if !strings.Contains(string(resps[1].Result), "Unknown tool") {
		t.Errorf("result = %s, want Unknown tool message", resps[1].Result)
	}
}

func TestToolsCallMissingName(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error == nil || resps[1].Error.Code != codeInvalidParams {
		t.Fatalf("want invalid params error, got %+v", resps)
	}
}

func TestToolsCallBeforeInitialize(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	resps := serve(t, srv, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo"}}`+"\n")
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeNotInitialized {
		t.Fatalf("want -32002 not initialized, got %+v", resps)
	}
}

func TestToolsCallDeniedByPolicy(t *testing.T) {
	srv := New([]tool.Tool{echoTool("echo")}, permission.New("deny", nil, nil, []string{"echo"}))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"secret"}}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("unexpected envelope: %+v", resps)
	}
	if !strings.Contains(string(resps[1].Result), "denied by policy") {
		t.Errorf("result = %s, want policy denial text", resps[1].Result)
	}
	if strings.Contains(string(resps[1].Result), "secret") {
		t.Error("denial text leaked the call arguments")
	}
}

func TestToolsCallAskFailsClosed(t *testing.T) {
	// An Ask rule for the tool must be treated as a denial: this headless
	// server has no interactive approver.
	srv := New([]tool.Tool{echoTool("echo")}, permission.New("deny", nil, []string{"echo"}, nil))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"echo"}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("unexpected envelope: %+v", resps)
	}
	if !strings.Contains(string(resps[1].Result), "requires approval") {
		t.Errorf("result = %s, want approval-denial text", resps[1].Result)
	}
}

func TestPing(t *testing.T) {
	srv := newTestServer()
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":"p","method":"ping"}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("ping failed: %+v", resps)
	}
	if string(resps[1].ID) != `"p"` || string(resps[1].Result) != "{}" {
		t.Errorf("ping result = id %s result %s, want id \"p\" result {}", resps[1].ID, resps[1].Result)
	}
}

func TestUnknownMethod(t *testing.T) {
	srv := newTestServer()
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":8,"method":"resources/list"}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error == nil || resps[1].Error.Code != codeMethodNotFound {
		t.Fatalf("want method not found, got %+v", resps)
	}
}

func TestParseError(t *testing.T) {
	srv := newTestServer()
	resps := serve(t, srv, "this is not json\n")
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeParseError {
		t.Fatalf("want parse error, got %+v", resps)
	}
	if string(resps[0].ID) != "null" {
		t.Errorf("parse error id = %s, want null", resps[0].ID)
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	srv := newTestServer()
	resps := serve(t, srv, `{"jsonrpc":"1.0","id":1,"method":"ping"}`+"\n")
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeInvalidRequest {
		t.Fatalf("want invalid request, got %+v", resps)
	}
}

func TestBatchRequestRejected(t *testing.T) {
	srv := newTestServer()
	resps := serve(t, srv, "[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]\n")
	if len(resps) != 1 || resps[0].Error == nil || resps[0].Error.Code != codeInvalidRequest {
		t.Fatalf("want invalid request, got %+v", resps)
	}
}

func TestNotificationGetsNoResponse(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"x"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"no/such/notification"}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1 (only initialize may reply)", len(resps))
	}
	// A notification-only session must produce zero output.
	srv2 := newTestServer()
	resps2 := serve(t, srv2, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	if len(resps2) != 0 {
		t.Fatalf("notification produced %d responses, want 0", len(resps2))
	}
}

func TestTwoCallsInOneSession(t *testing.T) {
	srv := newTestServer(echoTool("echo"))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}` + "\n" +
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"echo","arguments":{"b":2}}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	for i, wantID := range []string{"10", "11"} {
		if string(resps[i+1].ID) != wantID || resps[i+1].Error != nil {
			t.Fatalf("call %d envelope: id=%s error=%+v", i+1, resps[i+1].ID, resps[i+1].Error)
		}
	}
	want := []string{`echo:{"a":1}`, `echo:{"b":2}`}
	for i, w := range want {
		var res struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(resps[i+1].Result, &res); err != nil {
			t.Fatalf("unmarshal call result %d: %v", i+1, err)
		}
		if len(res.Content) != 1 || res.Content[0].Text != w {
			t.Errorf("call %d text = %q, want %q", i+1, res.Content[0].Text, w)
		}
	}
}

// TestWebFetchSSRFRejected exercises a real read-only built-in through the
// server: web_fetch refuses link-local targets at dial time, before any
// network I/O, so the test is fast and hermetic.
func TestWebFetchSSRFRejected(t *testing.T) {
	srv := newTestServer(builtin.ConfineWebFetch(netclient.ProxySpec{}))
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"web_fetch","arguments":{"url":"http://169.254.169.254/latest/meta-data/"}}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("unexpected envelope: %+v", resps)
	}
	if !strings.Contains(string(resps[1].Result), "refusing to fetch internal address") {
		t.Errorf("result = %s, want SSRF refusal text", resps[1].Result)
	}
}

// TestWebFetchToolExecutionError ensures a tool error surfaces as an isError
// result rather than a JSON-RPC error.
func TestToolExecuteErrorIsResultError(t *testing.T) {
	failing := fakeTool{
		name:     "failing",
		readOnly: true,
		executeFn: func(context.Context, json.RawMessage) (string, error) {
			return "", fmt.Errorf("boom")
		},
	}
	srv := newTestServer(failing)
	input := initRequest("2024-11-05") + "\n" +
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"failing"}}` + "\n"
	resps := serve(t, srv, input)
	if len(resps) != 2 || resps[1].Error != nil {
		t.Fatalf("unexpected envelope: %+v", resps)
	}
	if !strings.Contains(string(resps[1].Result), "boom") {
		t.Errorf("result = %s, want tool error text", resps[1].Result)
	}
}

func TestNewDeduplicatesTools(t *testing.T) {
	srv := newTestServer(echoTool("echo"), echoTool("echo"))
	resps := serve(t, srv, initRequest("2024-11-05")+"\n"+`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2", len(resps))
	}
	if got := strings.Count(string(resps[1].Result), `"name":"echo"`); got != 1 {
		t.Errorf("echo listed %d times, want 1", got)
	}
}
