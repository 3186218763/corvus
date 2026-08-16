package spill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveTextWritesContentVerbatim(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("line with 中文 and bytes\n", 200)
	loc, err := Local{}.SaveText(dir, "web_fetch", "web_fetch", content)
	if err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if loc.Bytes != len(content) {
		t.Fatalf("Bytes = %d, want %d", loc.Bytes, len(content))
	}
	if loc.Path == "" || loc.RetrievalHint == "" {
		t.Fatalf("locator incomplete: %+v", loc)
	}
	if !filepath.IsAbs(loc.Path) {
		t.Fatalf("Path = %q, want absolute", loc.Path)
	}
	got, err := os.ReadFile(loc.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != content {
		t.Fatalf("saved content differs: %d vs %d bytes", len(got), len(content))
	}
	if fi, err := os.Stat(loc.Path); err == nil && fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %o, want 600", fi.Mode().Perm())
	}
}

func TestSaveTextSanitizesSuggestedName(t *testing.T) {
	dir := t.TempDir()
	loc, err := Local{}.SaveText(dir, "tool", "../evil/path name!", "x")
	if err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(loc.Path), "evil_path_name") {
		t.Fatalf("base = %q, want sanitized prefix", filepath.Base(loc.Path))
	}
	if strings.Contains(filepath.Base(loc.Path), "..") {
		t.Fatalf("sanitized name still contains '..': %q", filepath.Base(loc.Path))
	}
}

func TestSaveTextDistinctNamesForSameInput(t *testing.T) {
	dir := t.TempDir()
	loc1, err := Local{}.SaveText(dir, "t", "same", "content")
	if err != nil {
		t.Fatal(err)
	}
	loc2, err := Local{}.SaveText(dir, "t", "same", "content")
	if err != nil {
		t.Fatal(err)
	}
	if loc1.Path == loc2.Path {
		t.Fatalf("colliding paths %q", loc1.Path)
	}
}

func TestSaveTextEmptyDirRejected(t *testing.T) {
	if _, err := (Local{}).SaveText("", "t", "n", "x"); err == nil {
		t.Fatal("empty dir accepted")
	}
}

func TestSafeSegment(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"  ":                       "",
		"web_fetch":                "web_fetch",
		"../evil/path name!":       "evil_path_name",
		"中文名":                      "",
		"a.b-c_d":                  "a.b-c_d",
		"..":                       "",
		(strings.Repeat("x", 100)): strings.Repeat("x", 40),
	}
	for in, want := range cases {
		if got := safeSegment(in); got != want {
			t.Errorf("safeSegment(%q) = %q, want %q", in, got, want)
		}
	}
}
