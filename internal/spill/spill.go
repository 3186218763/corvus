// Package spill persists oversized tool output to session-scoped files so the
// model receives a stable locator plus retrieval guidance instead of an
// irreversible truncation. The pattern follows deepseek-harness's spill seam:
// the agent loop is a Consumer, Store is the Service Definition, and Local is
// the shipped Service Provider. A future provider can swap Local for a remote
// or database backend without touching the loop.
//
// Security note: spilled content is tool output the model already saw in
// truncated form, so it may contain anything a tool returned. The file is
// written under the producing session's spill directory (owner-owned), never
// to a caller-supplied path; suggestedName is sanitized to a single safe
// segment and is a naming hint only.
package spill

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"corvus/internal/fileutil"
)

// maxSuggestedName bounds the sanitized suggested name so a malicious or
// broken tool name cannot blow past filesystem segment limits after padding
// with the timestamp and random suffix.
const maxSuggestedName = 40

// Store saves one oversized tool output and returns its model-facing locator.
// Implementations must be safe for concurrent calls from parallel tool
// execution goroutines.
type Store interface {
	SaveText(dir, toolName, suggestedName, content string) (Locator, error)
}

// Locator describes a saved spill artifact to the model and the UI.
type Locator struct {
	// Path is the model-facing handle. The local backend renders an absolute
	// filesystem path the model can hand to read_file/grep.
	Path string
	// Bytes is the exact byte length of the saved content.
	Bytes int
	// RetrievalHint is guidance text for the model on how to read Path.
	RetrievalHint string
}

// Local is the shipped spill provider: session-scoped files on the host
// filesystem under the caller-supplied session spill directory.
type Local struct{}

// SaveText persists content verbatim to <dir>/<tool>-<stamp>-<rand>.txt and
// returns the absolute path. dir is created with 0700 when missing. The file
// is published atomically, so a concurrent reader never sees a torn write.
func (Local) SaveText(dir, toolName, suggestedName, content string) (Locator, error) {
	if strings.TrimSpace(dir) == "" {
		return Locator{}, fmt.Errorf("spill: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Locator{}, fmt.Errorf("spill: create directory: %w", err)
	}
	name := safeSegment(suggestedName)
	if name == "" {
		name = safeSegment(toolName)
	}
	if name == "" {
		name = "output"
	}
	var randBytes [4]byte
	if _, err := rand.Read(randBytes[:]); err != nil {
		return Locator{}, fmt.Errorf("spill: random suffix: %w", err)
	}
	full := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.txt",
		name, time.Now().UTC().Format("20060102T150405.000"), hex.EncodeToString(randBytes[:])))
	if err := fileutil.AtomicWriteFile(full, []byte(content), 0o600); err != nil {
		return Locator{}, fmt.Errorf("spill: write %s: %w", full, err)
	}
	abs, err := filepath.Abs(full)
	if err != nil {
		return Locator{}, fmt.Errorf("spill: absolute path: %w", err)
	}
	return Locator{
		Path:          abs,
		Bytes:         len(content),
		RetrievalHint: "use read_file to retrieve the full output, or grep the path to search it",
	}, nil
}

// safeSegment converts a suggested name into one safe filesystem segment:
// alphanumerics, dot, dash, and underscore survive; every other rune becomes
// '_'; empty input yields "". The caller substitutes a default when empty.
func safeSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	last := byte(0)
	for i := 0; i < len(s) && b.Len() < maxSuggestedName; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
			b.WriteByte(c)
		case last == '_':
			continue
		default:
			b.WriteByte('_')
		}
		last = c
	}
	return strings.Trim(b.String(), "._-")
}
