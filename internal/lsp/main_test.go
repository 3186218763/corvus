package lsp

import (
	"os"
	"testing"

	"go.uber.org/goleak"
)

// fakeServerEnv switches the test binary into fake-LSP-server mode so
// startClient/Manager tests can drive a real subprocess without installing a
// language server (see fake_server_test.go).
const fakeServerEnv = "CORVUS_LSP_FAKE_SERVER"

func TestMain(m *testing.M) {
	if os.Getenv(fakeServerEnv) == "1" {
		os.Exit(runFakeLSPServer())
	}
	goleak.VerifyTestMain(m)
}
