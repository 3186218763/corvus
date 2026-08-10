package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	fakeModeEnv      = "CORVUS_LSP_FAKE_MODE"
	fakeEncEnv       = "CORVUS_LSP_FAKE_ENC"
	fakeCountFileEnv = "CORVUS_LSP_FAKE_COUNT_FILE"
	fakeInitErrorEnv = "CORVUS_LSP_FAKE_INIT_ERROR"
	fakeHoverEnv     = "CORVUS_LSP_FAKE_HOVER"
)

// runFakeLSPServer is the subprocess entry point: a minimal LSP server over
// stdio that answers initialize/shutdown and the read-only queries the client
// uses, and publishes diagnostics on didOpen/didChange. Behavior is scripted
// by environment variables so tests stay deterministic.
// fakeOutMsg is the fake server's response shape; it mirrors outMsg plus the
// Error field a real server needs when answering with an rpc error.
type fakeOutMsg struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      *int64    `json:"id,omitempty"`
	Method  string    `json:"method,omitempty"`
	Params  any       `json:"params,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

func runFakeLSPServer() int {
	r := bufio.NewReader(os.Stdin)
	w := os.Stdout
	failures := 0
	modes := map[string]bool{}
	for _, m := range strings.Split(os.Getenv(fakeModeEnv), ",") {
		if m != "" {
			modes[m] = true
		}
	}

	if f := os.Getenv(fakeCountFileEnv); f != "" {
		_ = appendCount(f)
	}

	for {
		body, err := readFrame(r)
		if err != nil {
			return 0
		}
		var m inMsg
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch {
		case m.Method == "initialize" && m.ID != nil:
			if modes["init-error"] {
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID,
					Error: &rpcError{Code: -32000, Message: os.Getenv(fakeInitErrorEnv)}})
				continue
			}
			enc := os.Getenv(fakeEncEnv)
			writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{
				"capabilities": map[string]any{"positionEncoding": enc},
			}})
		case m.Method == "shutdown" && m.ID != nil:
			writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID, Result: nil})
		case m.Method == "exit":
			return 0
		case m.Method == "textDocument/publishDiagnostics":
			// client→server notifications only; nothing to answer.
		case m.Method != "" && m.ID != nil:
			// client→server requests (didOpen/didChange are notifications, so
			// any request here is a textDocument query).
			switch m.Method {
			case "textDocument/didOpen", "textDocument/didChange":
				continue
			}
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position Position `json:"position"`
			}
			_ = json.Unmarshal(m.Params, &p)
			if modes["retry-once"] && failures == 0 &&
				(m.Method == "textDocument/definition" || m.Method == "textDocument/references" || m.Method == "textDocument/hover") {
				failures++
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID,
					Error: &rpcError{Code: -32801, Message: "content modified"}})
				continue
			}
			switch m.Method {
			case "textDocument/definition":
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{
					"uri": p.TextDocument.URI,
					"range": map[string]any{
						"start": map[string]any{"line": 0, "character": 0},
						"end":   map[string]any{"line": 0, "character": 1},
					},
				}})
			case "textDocument/references":
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID, Result: []any{
					map[string]any{"uri": p.TextDocument.URI, "range": map[string]any{
						"start": map[string]any{"line": 0, "character": 0},
						"end":   map[string]any{"line": 0, "character": 1}}},
					map[string]any{"uri": p.TextDocument.URI, "range": map[string]any{
						"start": map[string]any{"line": 2, "character": 0},
						"end":   map[string]any{"line": 2, "character": 1}}},
				}})
			case "textDocument/hover":
				hover := os.Getenv(fakeHoverEnv)
				if hover == "" {
					hover = "**fake hover**"
				}
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{
					"contents": map[string]any{"kind": "markdown", "value": hover},
				}})
			default:
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", ID: m.ID,
					Error: &rpcError{Code: -32601, Message: "method not found"}})
			}
		case m.Method != "" && m.ID == nil:
			// Notifications from the client: on document sync, publish one
			// diagnostic so waitDiagnostics has something to observe.
			if m.Method == "textDocument/didOpen" || m.Method == "textDocument/didChange" {
				var p struct {
					TextDocument struct {
						URI     string `json:"uri"`
						Version int    `json:"version"`
					} `json:"textDocument"`
				}
				_ = json.Unmarshal(m.Params, &p)
				version := p.TextDocument.Version
				writeFakeMsg(w, fakeOutMsg{JSONRPC: "2.0", Method: "textDocument/publishDiagnostics",
					Params: map[string]any{
						"uri":     p.TextDocument.URI,
						"version": version,
						"diagnostics": []any{map[string]any{
							"range": map[string]any{
								"start": map[string]any{"line": 0, "character": 0},
								"end":   map[string]any{"line": 0, "character": 1}},
							"severity": 1,
							"message":  "fake diagnostic",
						}},
					}})
			}
		}
	}
}

func appendCount(path string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, "initialize")
	return err
}

func writeFakeMsg(w *os.File, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return
	}
	_, _ = w.Write(b)
}
