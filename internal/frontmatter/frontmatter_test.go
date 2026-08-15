package frontmatter

import (
	"strings"
	"testing"
)

func TestSplitNoFence(t *testing.T) {
	fm, body := Split("just body text\nno fence")
	if len(fm) != 0 {
		t.Errorf("expected empty fm, got %v", fm)
	}
	if !strings.Contains(body, "just body text") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitUnclosedFence(t *testing.T) {
	fm, body := Split("---\nkey: val\n\nno closing fence")
	if len(fm) != 0 {
		t.Errorf("unclosed fence should return empty fm, got %v", fm)
	}
	if !strings.Contains(body, "---") {
		t.Errorf("body should contain original content: %q", body)
	}
}

func TestSplitEmptyBody(t *testing.T) {
	fm, body := Split("---\nkey: val\n---\n")
	if fm["key"] != "val" {
		t.Errorf("key = %q", fm["key"])
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestSplitNestedMetadata(t *testing.T) {
	fm, body := Split("---\nname: test\ndescription: desc\nmetadata:\n  type: user\n---\n\nbody here")
	if fm["name"] != "test" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "desc" {
		t.Errorf("description = %q", fm["description"])
	}
	if fm["type"] != "user" {
		t.Errorf("type = %q, expected flattened from metadata", fm["type"])
	}
	if !strings.Contains(body, "body here") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitCRLF(t *testing.T) {
	fm, body := Split("---\r\nname: test\r\n---\r\nbody\r\n")
	if fm["name"] != "test" {
		t.Errorf("name = %q", fm["name"])
	}
	if !strings.Contains(body, "body") {
		t.Errorf("body = %q", body)
	}
}

func TestSplitQuotedValues(t *testing.T) {
	fm, _ := Split("---\nname: test\ndescription: \"quoted desc\"\n---\n")
	if fm["description"] != "quoted desc" {
		t.Errorf("description should be unquoted: %q", fm["description"])
	}
}

func TestSplitSingleQuotes(t *testing.T) {
	fm, _ := Split("---\nname: test\ndescription: 'single quoted'\n---\n")
	if fm["description"] != "single quoted" {
		t.Errorf("description should be unquoted: %q", fm["description"])
	}
}

func TestSplitEmptyInput(t *testing.T) {
	fm, body := Split("")
	if len(fm) != 0 {
		t.Errorf("empty input should return empty fm, got %v", fm)
	}
	if body != "" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitOnlyFence(t *testing.T) {
	fm, body := Split("---\n---\n")
	if len(fm) != 0 {
		t.Errorf("empty fence should return empty fm, got %v", fm)
	}
	if strings.TrimSpace(body) != "" {
		t.Errorf("body = %q", body)
	}
}

func TestSplitMultipleKeys(t *testing.T) {
	fm, _ := Split("---\na: 1\nb: 2\nc: 3\n---\n")
	if fm["a"] != "1" || fm["b"] != "2" || fm["c"] != "3" {
		t.Errorf("fm = %v", fm)
	}
}

func TestSplitCaseInsensitive(t *testing.T) {
	fm, _ := Split("---\nName: Test\nDESCRIPTION: desc\n---\n")
	if fm["name"] != "Test" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "desc" {
		t.Errorf("description = %q", fm["description"])
	}
}

func TestSplitYAMLScalarsWithColonAndMultiline(t *testing.T) {
	fm, body := Split("---\n" +
		"name: test\n" +
		"description: \"run: with colon\"\n" +
		"notes: |\n" +
		"  first line\n" +
		"  second line\n" +
		"---\n" +
		"body")
	if fm["description"] != "run: with colon" {
		t.Fatalf("description = %q", fm["description"])
	}
	if fm["notes"] != "first line\nsecond line" {
		t.Fatalf("notes = %q", fm["notes"])
	}
	if body != "body" {
		t.Fatalf("body = %q", body)
	}
}

func TestParseErrorReportsMalformedYAML(t *testing.T) {
	err := ParseError("---\nname: [unterminated\n---\nbody")
	if err == nil {
		t.Fatal("ParseError accepted malformed YAML")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "line") {
		t.Fatalf("error = %v, want YAML location detail", err)
	}
}

func TestParseErrorNoopsWithoutFenceOrContent(t *testing.T) {
	if err := ParseError("no fence at all"); err != nil {
		t.Fatalf("unfenced input: %v", err)
	}
	if err := ParseError("---\n---\n"); err != nil {
		t.Fatalf("empty fence: %v", err)
	}
	if err := ParseError("---\nname: ok\n---\nbody"); err != nil {
		t.Fatalf("valid block: %v", err)
	}
}

func TestRaw(t *testing.T) {
	raw, body, ok := Raw("---\r\nname: test\r\n---\r\nbody")
	if !ok || raw != "name: test" || body != "body" {
		t.Fatalf("Raw = (%q, %q, %v), want CRLF-normalized split", raw, body, ok)
	}
	if _, _, ok := Raw("---\nnever closed\n"); ok {
		t.Fatal("unclosed fence reported ok")
	}
	if _, b, ok := Raw("plain"); ok || b != "plain" {
		t.Fatalf("unfenced = (%q, %v), want body passthrough", b, ok)
	}
}

func TestEncode(t *testing.T) {
	fm := struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Allowed     []string `yaml:"allowed-tools,omitempty,flow"`
	}{"review", "run: checks", []string{"read_file", "grep"}}
	got := Encode(fm, "body\n\n")
	want := "---\nname: review\ndescription: 'run: checks'\nallowed-tools: [read_file, grep]\n---\n\nbody\n"
	if got != want {
		t.Fatalf("Encode =\n%q\nwant\n%q", got, want)
	}
}

func TestEncodeEscapesAndTrimsBody(t *testing.T) {
	got := Encode(map[string]string{"title": "Plan: step one"}, "  padded \n\n")
	want := "---\ntitle: 'Plan: step one'\n---\n\n  padded\n"
	if got != want {
		t.Fatalf("Encode = %q, want %q", got, want)
	}
	// The round trip: Split reads back what Encode wrote (the body keeps the
	// blank separator line that follows the fence).
	fm, body := Split(got)
	if fm["title"] != "Plan: step one" || body != "\n  padded\n" {
		t.Fatalf("round trip = (%v, %q)", fm, body)
	}
}
