package shellsafe

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"corvus/internal/shellparse"
)

// TestNormalizeBashSafeRedirectsForMatch locks the redirect classification:
// fd duplication/close and null-sink output redirects are stripped for
// matching; anything that can write to a real file is left unnormalized (the
// function reports ok=false so callers keep the conservative shell-syntax
// guard).
func TestNormalizeBashSafeRedirectsForMatch(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "no redirects", in: "git status", want: "git status", wantOK: true},
		{name: "quoted operators are not redirects", in: `echo "a > b"`, want: `echo "a > b"`, wantOK: true},
		{name: "stdout to devnull", in: "git status > /dev/null", want: "git status", wantOK: true},
		{name: "stdout to devnull no space", in: "git status >/dev/null", want: "git status", wantOK: true},
		{name: "explicit fd to devnull", in: "git status 1>/dev/null", want: "git status", wantOK: true},
		{name: "append devnull", in: "git status >>/dev/null", want: "git status", wantOK: true},
		{name: "stderr devnull", in: "git status 2>/dev/null", want: "git status", wantOK: true},
		{name: "stderr devnull spaced", in: "git status 2> /dev/null", want: "git status", wantOK: true},
		{name: "all devnull", in: "git status &>/dev/null", want: "git status", wantOK: true},
		{name: "all append devnull", in: "git status &>>/dev/null", want: "git status", wantOK: true},
		{name: "clobber devnull", in: "git status >|/dev/null", want: "git status", wantOK: true},
		{name: "powershell null", in: "git status >$null", want: "git status", wantOK: true},
		{name: "powershell null uppercase", in: "git status >$NULL", want: "git status", wantOK: true},
		{name: "windows nul", in: "git status >nul", want: "git status", wantOK: true},
		{name: "windows nul uppercase", in: "git status >NUL", want: "git status", wantOK: true},
		{name: "fd dup stdout to stderr", in: "git status >&2", want: "git status", wantOK: true},
		{name: "fd dup stderr to stdout", in: "git status 2>&1", want: "git status", wantOK: true},
		{name: "fd dup explicit", in: "git status 1>&2", want: "git status", wantOK: true},
		{name: "fd dup close", in: "git status 2>&-", want: "git status", wantOK: true},
		{name: "fd dup input", in: "git status 0<&1", want: "git status", wantOK: true},
		{name: "combined safe redirects", in: "git status >/dev/null 2>&1", want: "git status", wantOK: true},
		{name: "two fds both null", in: "git status 2>&1 >/dev/null", want: "git status", wantOK: true},
		{name: "pipe with safe redirect", in: "ls -la 2>/dev/null | grep x", want: "ls -la  | grep x", wantOK: true},
		{name: "and chain with safe redirects", in: "git status >/dev/null && ls", want: "git status  && ls", wantOK: true},
		{name: "leading whitespace trimmed", in: "  git status  > /dev/null ", want: "git status", wantOK: true},
		{name: "unsafe stdout redirect", in: "cat a > out.txt", wantOK: false},
		{name: "unsafe append", in: "sort x >>out.txt", wantOK: false},
		{name: "unsafe stderr file", in: "git status 2>err.txt", wantOK: false},
		{name: "unsafe all", in: "ls &>file", wantOK: false},
		{name: "unsafe all append", in: "ls &>>file", wantOK: false},
		{name: "unsafe clobber", in: "ls >|file", wantOK: false},
		{name: "unsafe input", in: "sort < in.txt", wantOK: false},
		{name: "unsafe fd dup to word", in: "git status 2>&file", wantOK: false},
		{name: "unsafe fd dup empty", in: "git status 2>&", wantOK: false},
		{name: "non-null dev path", in: "git status > /dev", wantOK: false},
		{name: "devnull with suffix", in: "git status > /dev/nullx", wantOK: false},
		{name: "devnull with prefix", in: "git status > x/dev/null", wantOK: false},
		{name: "here document", in: "cat <<'EOF'\nhi\nEOF", wantOK: false},
		{name: "here string", in: "cat <<< word", wantOK: false},
		{name: "mixed safe and unsafe", in: "git status >/dev/null > out.txt", wantOK: false},
		{name: "parse error", in: "git status >", wantOK: false},
		{name: "empty subject", in: "", want: "", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NormalizeBashSafeRedirectsForMatch(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("NormalizeBashSafeRedirectsForMatch(%q) ok = %v, want %v (got %q)",
					tt.in, ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Fatalf("NormalizeBashSafeRedirectsForMatch(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizedReadOnlyRoundTrip proves the normalize-then-classify pipeline:
// a read-only command with only null-sink redirects still classifies as
// read-only, while the same command redirected to a real file does not.
func TestNormalizedReadOnlyRoundTrip(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"git log --oneline", true},
		{"git log --oneline >/dev/null", true},
		{"git log --oneline 2>&1", true},
		{"git log --oneline >$null", true},
		{"git log --oneline >/dev/null 2>&1", true},
		{"git log --oneline > out.txt", false},
		{"git log --oneline | tee out.txt", false},
		{"git log --oneline $(date)", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			subject := tt.command
			if normalized, ok := NormalizeBashSafeRedirectsForMatch(subject); ok {
				subject = normalized
			}
			_, _, ok := CommandIsReadOnly(subject)
			if ok != tt.want {
				t.Fatalf("CommandIsReadOnly(after normalize %q) = %v, want %v", tt.command, ok, tt.want)
			}
		})
	}
}

// wordOf parses cmd and returns the first redirect's word so helper predicates
// can be unit-tested against real syntax.Word values.
func wordOf(t *testing.T, cmd string) *syntax.Word {
	t.Helper()
	file, err := shellparse.ParseBash(cmd)
	if err != nil {
		t.Fatalf("ParseBash(%q): %v", cmd, err)
	}
	if len(file.Stmts) != 1 || file.Stmts[0] == nil || len(file.Stmts[0].Redirs) == 0 {
		t.Fatalf("ParseBash(%q): no single redirect statement", cmd)
	}
	return file.Stmts[0].Redirs[0].Word
}

func TestIsNullRedirectWord(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "devnull", cmd: "git status >/dev/null", want: true},
		{name: "devnull spaced", cmd: "git status > /dev/null", want: true},
		{name: "devnull trailing slash", cmd: "git status >/dev/null/", want: false},
		{name: "dev dir", cmd: "git status >/dev", want: false},
		{name: "devnull suffix", cmd: "git status >/dev/nullx", want: false},
		{name: "powershell null", cmd: "git status >$null", want: true},
		{name: "powershell null upper", cmd: "git status >$NULL", want: true},
		{name: "windows nul", cmd: "git status >nul", want: true},
		{name: "windows nul upper", cmd: "git status >NUL", want: true},
		{name: "null spelled out", cmd: "git status >null", want: false},
		{name: "real file", cmd: "git status >out.txt", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNullRedirectWord(tt.cmd, wordOf(t, tt.cmd)); got != tt.want {
				t.Fatalf("isNullRedirectWord(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
	// A nil word (or one without valid source positions) must never be treated
	// as a null sink.
	if isNullRedirectWord("git status >x", nil) {
		t.Fatal("isNullRedirectWord with nil word = true, want false")
	}
}

func TestIsSafeFDDupWord(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{name: "stdout to fd1", cmd: "git status >&1", want: true},
		{name: "stdout to fd2", cmd: "git status >&2", want: true},
		{name: "multi digit fd", cmd: "git status 2>&12", want: true},
		{name: "close fd", cmd: "git status 2>&-", want: true},
		{name: "word not fd", cmd: "git status 2>&file", want: false},
		{name: "mixed digits and letters", cmd: "git status 2>&1x", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSafeFDDupWord(tt.cmd, wordOf(t, tt.cmd)); got != tt.want {
				t.Fatalf("isSafeFDDupWord(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
	if isSafeFDDupWord("git status >&1", nil) {
		t.Fatal("isSafeFDDupWord with nil word = true, want false")
	}
}

func TestRedirectWordSource(t *testing.T) {
	// The source slice is the raw text of the word, whitespace-trimmed.
	word := wordOf(t, "git status > /dev/null")
	if got := redirectWordSource("git status > /dev/null", word); got != "/dev/null" {
		t.Fatalf("redirectWordSource = %q, want %q", got, "/dev/null")
	}
	if got := redirectWordSource("git status > /dev/null", nil); got != "" {
		t.Fatalf("redirectWordSource(nil word) = %q, want empty", got)
	}
}

func TestSafeRedirectSpanNilRedir(t *testing.T) {
	if _, ok := safeRedirectSpan("git status", nil); ok {
		t.Fatal("safeRedirectSpan(nil) = ok, want false")
	}
}

func TestAppendSafeRedirectSpansNilStmt(t *testing.T) {
	var spans []redirectSpan
	if !appendSafeRedirectSpans("git status", nil, &spans) {
		t.Fatal("appendSafeRedirectSpans(nil stmt) = false, want true")
	}
	if len(spans) != 0 {
		t.Fatalf("appendSafeRedirectSpans(nil stmt) appended %d spans", len(spans))
	}
}

func TestSafeRedirectSpansEmptyStatements(t *testing.T) {
	spans, ok := safeRedirectSpans("git status", nil)
	if !ok || len(spans) != 0 {
		t.Fatalf("safeRedirectSpans(nil stmts) = (%v, %v), want (nil, true)", spans, ok)
	}
	// A statement whose command is neither a binary chain nor a redirect
	// contributes no spans.
	file, err := shellparse.ParseBash("git status")
	if err != nil {
		t.Fatal(err)
	}
	spans, ok = safeRedirectSpans("git status", file.Stmts)
	if !ok || len(spans) != 0 {
		t.Fatalf("safeRedirectSpans(plain stmt) = (%v, %v), want (nil, true)", spans, ok)
	}
}
