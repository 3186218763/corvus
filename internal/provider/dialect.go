// dialect.go holds the SSE/streaming scaffolding shared by the concrete
// dialect subpackages (anthropic, openai, responses): chunk delivery, custom
// header cleaning, the request-body buffer pool, and the tuned streaming HTTP
// client. Dialect-specific policy (reserved header names, empty-value
// handling) stays in the dialects.

package provider

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"corvus/internal/netclient"
)

// DefaultStreamIdleTimeout caps how long a started SSE stream may go without
// any bytes before the stall watchdog aborts it.
const DefaultStreamIdleTimeout = 120 * time.Second

// SendChunk delivers chunk to out: first without blocking (the common case
// while the consumer keeps up), then blocking while honouring ctx
// cancellation. It reports false when ctx was cancelled before delivery.
func SendChunk(ctx context.Context, out chan<- Chunk, chunk Chunk) bool {
	select {
	case out <- chunk:
		return true
	default:
	}
	select {
	case <-ctx.Done():
		return false
	case out <- chunk:
		return true
	}
}

// CleanCustomHeaders trims caller-supplied header pairs and drops empty names
// and reserved ones. dropEmptyValues also drops empty values: OpenAI gateways
// reject them, while Anthropic accepts a header set to "" as a clearing
// instruction, so only the OpenAI dialect passes true.
func CleanCustomHeaders(in map[string]string, reserved func(string) bool, dropEmptyValues bool) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for name, value := range in {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || reserved(name) || (dropEmptyValues && value == "") {
			continue
		}
		out[name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ApplyCustomHeaders sets the cleaned custom headers on h.
func ApplyCustomHeaders(h http.Header, headers map[string]string, reserved func(string) bool, dropEmptyValues bool) {
	for name, value := range CleanCustomHeaders(headers, reserved, dropEmptyValues) {
		h.Set(name, value)
	}
}

// BodyBufPool reuses byte buffers for JSON-marshalled request bodies. Each
// turn allocates a buffer, marshals the request, and sends it — pooling avoids
// the GC churn of repeated ~10-100KB alloc/free cycles.
var BodyBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

// NewStreamingHTTPClient builds the tuned HTTP client shared by the streaming
// dialects: a model can think for a long while before its first token, so
// response headers get a deliberately long timeout.
func NewStreamingHTTPClient(spec netclient.ProxySpec) (*http.Client, error) {
	return netclient.NewHTTPClient(spec, netclient.TransportOptions{
		DialTimeout:           30 * time.Second,
		KeepAlive:             30 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second,
	})
}
