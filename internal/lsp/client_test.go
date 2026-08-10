package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeServerSpec builds a ServerSpec that runs the current test binary as the
// fake LSP server, so startClient/Manager tests need no real language server.
func fakeServerSpec(t *testing.T, mode, enc string) ServerSpec {
	t.Helper()
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{fakeServerEnv: "1"}
	if mode != "" {
		env[fakeModeEnv] = mode
	}
	if enc != "" {
		env[fakeEncEnv] = enc
	}
	return ServerSpec{
		Command:     bin,
		Args:        []string{"-test.run=^$"},
		Env:         env,
		LanguageID:  "fake",
		Extensions:  []string{".fake"},
		InstallHint: "fake server",
	}
}

func TestStartClientLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := t.TempDir()
	c, err := startClient(ctx, fakeServerSpec(t, "", "utf-8").Command, []string{"-test.run=^$"},
		map[string]string{fakeServerEnv: "1", fakeEncEnv: "utf-8"}, "fake", root)
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	if c == nil {
		t.Fatal("startClient returned nil client")
	}
	if c.posEnc != "utf-8" {
		t.Errorf("posEnc = %q, want utf-8", c.posEnc)
	}
	if c.langID != "fake" || c.root != root {
		t.Errorf("client metadata = (%q, %q), want (fake, %q)", c.langID, c.root, root)
	}
	c.close()
}

func TestStartClientDefaultsUTF16Encoding(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := startClient(ctx, fakeServerSpec(t, "", "").Command, []string{"-test.run=^$"},
		map[string]string{fakeServerEnv: "1"}, "fake", t.TempDir())
	if err != nil {
		t.Fatalf("startClient: %v", err)
	}
	if c.posEnc != encodingUTF16 {
		t.Errorf("posEnc = %q, want default utf-16", c.posEnc)
	}
	c.close()
}

func TestStartClientInitializationFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := startClient(ctx, fakeServerSpec(t, "init-error", "").Command, []string{"-test.run=^$"},
		map[string]string{fakeServerEnv: "1", fakeModeEnv: "init-error", fakeInitErrorEnv: "boom"},
		"fake", t.TempDir())
	if err == nil {
		t.Fatal("startClient with init-error mode = nil error, want failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %q, want init failure message", err)
	}
}

func TestStartClientCrashOnLaunch(t *testing.T) {
	// A binary that exits immediately must surface as a start error, not hang.
	bin := filepath.Join(t.TempDir(), "not-a-server")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := startClient(ctx, bin, nil, map[string]string{fakeServerEnv: "1"}, "fake", t.TempDir())
	if err == nil {
		t.Fatal("startClient(crashing binary) = nil error, want failure")
	}
}

// connPair wires two conns back-to-back over io.Pipe so unit tests can drive
// the client with an in-process peer. The returned cleanup closes both writers
// so both readLoops see EOF (goleak-clean).
func connPair(clientNotify func(string, json.RawMessage), clientRequest func(int64, string, json.RawMessage),
	serverNotify func(string, json.RawMessage), serverRequest func(int64, string, json.RawMessage)) (cc, sc *conn, cleanup func()) {
	caR, caW := io.Pipe()
	acR, acW := io.Pipe()
	cc = newConn(caW, acR, clientNotify, clientRequest)
	sc = newConn(acW, caR, serverNotify, serverRequest)
	return cc, sc, func() {
		caW.Close()
		acW.Close()
	}
}

// replyErrorForTest lets the in-process fake server answer a client request
// with an rpc error (outMsg deliberately has no Error field — clients do not
// send errors — so the test peer uses its own response shape).
func (c *conn) replyErrorForTest(id int64, code int) error {
	return c.writeMsg(fakeOutMsg{JSONRPC: "2.0", ID: &id, Error: &rpcError{Code: code, Message: "test error"}})
}

func TestClientEnsureSynced(t *testing.T) {
	c := &client{langID: "fake", docs: map[string]*docState{}, diags: map[string][]Diagnostic{}, diagVer: map[string]int{}}
	var buf bytes.Buffer
	pr, pw := io.Pipe()
	pw.Close() // EOF immediately: the readLoop exits and goleak stays happy
	c.conn = newConn(&buf, pr, nil, nil)

	path := filepath.Join(t.TempDir(), "a.fake")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(path)
	if err := c.ensureSynced(uri, path); err != nil {
		t.Fatalf("ensureSynced first: %v", err)
	}
	if got := c.docVersion(uri); got != 1 {
		t.Fatalf("docVersion after didOpen = %d, want 1", got)
	}
	if !strings.Contains(buf.String(), `"method":"textDocument/didOpen"`) {
		t.Fatalf("no didOpen written: %s", buf.String())
	}

	// Identical stat → no-op, no didChange.
	before := buf.Len()
	if err := c.ensureSynced(uri, path); err != nil {
		t.Fatalf("ensureSynced no-op: %v", err)
	}
	if buf.Len() != before {
		t.Fatalf("unchanged file produced a sync message: %s", buf.String()[before:])
	}

	// Out-of-band edit → didChange with the next version.
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.ensureSynced(uri, path); err != nil {
		t.Fatalf("ensureSynced didChange: %v", err)
	}
	if got := c.docVersion(uri); got != 2 {
		t.Fatalf("docVersion after didChange = %d, want 2", got)
	}
	if !strings.Contains(buf.String(), `"method":"textDocument/didChange"`) {
		t.Fatalf("no didChange written: %s", buf.String())
	}

	// Missing file surfaces the stat error.
	if err := c.ensureSynced(uri, filepath.Join(t.TempDir(), "missing.fake")); err == nil {
		t.Fatal("ensureSynced(missing file) = nil error, want failure")
	}
}

func TestClientWaitDiagnostics(t *testing.T) {
	uri := "file:///a.fake"
	newClient := func() *client {
		return &client{docs: map[string]*docState{}, diags: map[string][]Diagnostic{}, diagVer: map[string]int{}}
	}

	t.Run("already satisfied", func(t *testing.T) {
		c := newClient()
		c.diagVer[uri] = 5
		c.diags[uri] = []Diagnostic{{Message: "old"}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		start := time.Now()
		got := c.waitDiagnostics(ctx, uri, 3, 10*time.Second)
		if len(got) != 1 || got[0].Message != "old" {
			t.Fatalf("diags = %+v", got)
		}
		if time.Since(start) > time.Second {
			t.Fatal("waitDiagnostics blocked on an already-satisfied version")
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		c := newClient()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()
		got := c.waitDiagnostics(ctx, uri, 99, time.Minute)
		if got != nil {
			t.Fatalf("diags = %+v, want nil", got)
		}
		if time.Since(start) > time.Second {
			t.Fatal("waitDiagnostics ignored context cancellation")
		}
	})

	t.Run("deadline returns freshest cache", func(t *testing.T) {
		c := newClient()
		c.diagVer[uri] = 1
		c.diags[uri] = []Diagnostic{{Message: "stale"}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		start := time.Now()
		got := c.waitDiagnostics(ctx, uri, 99, 30*time.Millisecond)
		if len(got) != 1 || got[0].Message != "stale" {
			t.Fatalf("diags = %+v, want stale cache", got)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("waitDiagnostics took %v, want ~30ms deadline", elapsed)
		}
	})
}

func TestClientHandleNotify(t *testing.T) {
	uri := "file:///a.fake"
	c := &client{docs: map[string]*docState{}, diags: map[string][]Diagnostic{}, diagVer: map[string]int{}}

	params := `{"uri":"file:///a.fake","version":7,"diagnostics":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"severity":2,"message":"warn"}]}`
	c.handleNotify("textDocument/publishDiagnostics", json.RawMessage(params))
	if c.diagVer[uri] != 7 || len(c.diags[uri]) != 1 || c.diags[uri][0].Severity != 2 {
		t.Fatalf("diag state = ver %d diags %+v", c.diagVer[uri], c.diags[uri])
	}

	// Non-diagnostic notifications are ignored.
	c.handleNotify("window/logMessage", json.RawMessage(`{"type":1,"message":"x"}`))
	if c.diagVer[uri] != 7 {
		t.Fatal("non-diagnostic notification changed diag state")
	}

	// Malformed params are ignored.
	before := c.diagVer[uri]
	c.handleNotify("textDocument/publishDiagnostics", json.RawMessage(`not json`))
	if c.diagVer[uri] != before {
		t.Fatal("malformed diagnostics changed diag state")
	}

	// A version-less publish falls back to the tracked document version.
	c.docs[uri] = &docState{version: 3}
	c.handleNotify("textDocument/publishDiagnostics", json.RawMessage(`{"uri":"file:///a.fake","diagnostics":[]}`))
	if c.diagVer[uri] != 3 {
		t.Fatalf("diagVer without version = %d, want 3", c.diagVer[uri])
	}
}

func TestClientHandleRequest(t *testing.T) {
	c := &client{}
	client, server, cleanup := connPair(nil, c.handleRequest, nil, nil)
	defer cleanup()
	c.conn = client

	// workspace/configuration must be answered with one null per item.
	res, err := server.call(context.Background(), "workspace/configuration", map[string]any{
		"items": []any{map[string]any{}, map[string]any{}, map[string]any{}},
	})
	if err != nil {
		t.Fatalf("workspace/configuration call: %v", err)
	}
	if string(res) != `[null,null,null]` {
		t.Fatalf("workspace/configuration reply = %s", res)
	}

	// Unknown server→client requests still get a reply so servers don't stall.
	// reply(id, nil) omits the JSON "result" member entirely (omitempty), so
	// the caller sees an empty result with no error — that is the current
	// wire behavior for the default handler branch.
	other, err := server.call(context.Background(), "custom/request", nil)
	if err != nil {
		t.Fatalf("custom request call: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("custom request reply = %q, want empty result (nil reply omits result)", other)
	}
}

func TestClientDocVersion(t *testing.T) {
	c := &client{docs: map[string]*docState{}}
	if got := c.docVersion("file:///x"); got != 0 {
		t.Fatalf("unknown doc version = %d, want 0", got)
	}
	c.docs["file:///x"] = &docState{version: 9}
	if got := c.docVersion("file:///x"); got != 9 {
		t.Fatalf("doc version = %d, want 9", got)
	}
}

func TestIsContentModified(t *testing.T) {
	if !isContentModified(&rpcError{Code: -32801}) {
		t.Fatal("-32801 should classify as content modified")
	}
	if isContentModified(&rpcError{Code: -32601}) {
		t.Fatal("other codes must not classify as content modified")
	}
	if isContentModified(nil) {
		t.Fatal("nil must not classify as content modified")
	}
}

func TestCallRetryRetriesContentModifiedOnce(t *testing.T) {
	cc, server, cleanup := connPair(nil, nil, nil, nil)
	defer cleanup()
	c := &client{conn: cc}

	failures := 0
	server.onRequest = func(id int64, method string, _ json.RawMessage) {
		if failures == 0 && method == "textDocument/x" {
			failures++
			_ = server.replyErrorForTest(id, -32801)
			return
		}
		_ = server.reply(id, map[string]any{"ok": true})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := c.callRetry(ctx, "textDocument/x", map[string]any{})
	if err != nil {
		t.Fatalf("callRetry: %v", err)
	}
	if !strings.Contains(string(res), `"ok":true`) {
		t.Fatalf("result = %s", res)
	}
	if failures != 1 {
		t.Fatalf("server saw %d failures, want 1 (retried once)", failures)
	}
}

func TestCallRetryReturnsNonContentModifiedImmediately(t *testing.T) {
	cc, server, cleanup := connPair(nil, nil, nil, nil)
	defer cleanup()
	c := &client{conn: cc}
	server.onRequest = func(id int64, _ string, _ json.RawMessage) {
		_ = server.replyErrorForTest(id, -32601)
	}
	start := time.Now()
	_, err := c.callRetry(context.Background(), "textDocument/x", nil)
	if err == nil {
		t.Fatal("callRetry = nil error, want -32601")
	}
	if time.Since(start) > time.Second {
		t.Fatal("non-content-modified error should not be retried")
	}
}

func TestCallRetryContextCancellation(t *testing.T) {
	cc, server, cleanup := connPair(nil, nil, nil, nil)
	defer cleanup()
	c := &client{conn: cc}
	server.onRequest = func(id int64, _ string, _ json.RawMessage) {
		_ = server.replyErrorForTest(id, -32801)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.callRetry(ctx, "textDocument/x", nil); err == nil {
		t.Fatal("callRetry with canceled context = nil error, want context error")
	}
}

func TestClientQueryAndReferencesShapes(t *testing.T) {
	cc, server, cleanup := connPair(nil, nil, nil, nil)
	defer cleanup()
	c := &client{conn: cc, posEnc: encodingUTF16}

	got := make(chan string, 2)
	server.onRequest = func(id int64, method string, params json.RawMessage) {
		got <- method + " " + string(params)
		_ = server.reply(id, nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := c.query(ctx, "textDocument/definition", "file:///a.fake", Position{Line: 1, Character: 2}); err != nil {
		t.Fatalf("query: %v", err)
	}
	select {
	case m := <-got:
		if !strings.Contains(m, `"uri":"file:///a.fake"`) || !strings.Contains(m, `"character":2`) {
			t.Fatalf("query params = %s", m)
		}
	case <-ctx.Done():
		t.Fatal("query never reached the server")
	}

	if _, err := c.references(ctx, "file:///a.fake", Position{Line: 3, Character: 4}); err != nil {
		t.Fatalf("references: %v", err)
	}
	select {
	case m := <-got:
		if !strings.Contains(m, "textDocument/references") || !strings.Contains(m, `"includeDeclaration":true`) {
			t.Fatalf("references params = %s", m)
		}
	case <-ctx.Done():
		t.Fatal("references never reached the server")
	}
}

func TestCloseIdempotentAndNilProcess(t *testing.T) {
	c := &client{
		cmd:  &exec.Cmd{},
		conn: newConn(&bytes.Buffer{}, io.NopCloser(strings.NewReader("")), nil, nil),
	}
	c.close() // no Process, reader EOF: must not panic or hang
	c.close()
}

func TestEnvSlice(t *testing.T) {
	got := envSlice(map[string]string{"A": "1", "B": "2"})
	if len(got) != 2 || !contains(got, "A=1") || !contains(got, "B=2") {
		t.Fatalf("envSlice = %v", got)
	}
	if got := envSlice(nil); len(got) != 0 {
		t.Fatalf("envSlice(nil) = %v, want empty", got)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
