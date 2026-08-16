package agent

import (
	"strings"
	"testing"
)

func TestWithResponseLanguageOnlySkipsLeadingInjectedBlock(t *testing.T) {
	userMention := "explain why <response-language> appears in this file"
	got := WithResponseLanguage(userMention, "en")
	if !strings.HasPrefix(got, "<response-language>") || !strings.Contains(got, "use English") || !strings.HasSuffix(got, userMention) {
		t.Fatalf("WithResponseLanguage should prefix user-authored tag mentions, got %q", got)
	}

	alreadyPrefixed := ResponseLanguageBlock("en") + "\n\n" + userMention
	if got := WithResponseLanguage(alreadyPrefixed, "en"); got != alreadyPrefixed {
		t.Fatalf("WithResponseLanguage duplicated a leading injected block:\n got %q\nwant %q", got, alreadyPrefixed)
	}

	withLeadingMemory := "<memory-update>\nRemember this.\n</memory-update>\n\n" + alreadyPrefixed
	if got := WithResponseLanguage(withLeadingMemory, "en"); got != withLeadingMemory {
		t.Fatalf("WithResponseLanguage duplicated a response block after leading transient context:\n got %q\nwant %q", got, withLeadingMemory)
	}
}

func TestWithReasoningLanguageOnlySkipsLeadingInjectedBlock(t *testing.T) {
	userMention := "explain why <reasoning-language> appears in this file"
	got := WithReasoningLanguage(userMention, "zh")
	if !strings.HasPrefix(got, "<reasoning-language>") || !strings.Contains(got, "Simplified Chinese") || !strings.HasSuffix(got, userMention) {
		t.Fatalf("WithReasoningLanguage should prefix user-authored tag mentions, got %q", got)
	}

	alreadyPrefixed := ReasoningLanguageBlock("zh") + "\n\n" + userMention
	if got := WithReasoningLanguage(alreadyPrefixed, "zh"); got != alreadyPrefixed {
		t.Fatalf("WithReasoningLanguage duplicated a leading injected block:\n got %q\nwant %q", got, alreadyPrefixed)
	}

	withLeadingMemory := "<memory-update>\nRemember this.\n</memory-update>\n\n" + alreadyPrefixed
	if got := WithReasoningLanguage(withLeadingMemory, "zh"); got != withLeadingMemory {
		t.Fatalf("WithReasoningLanguage duplicated a reasoning block after leading transient context:\n got %q\nwant %q", got, withLeadingMemory)
	}
}

func TestReasoningLanguageBlockZhStaysImperative(t *testing.T) {
	// The imperative form measurably outperforms soft "偏好" phrasing on
	// Chinese prompts that embed English logs/code; keep it from regressing
	// back into a suggestion.
	block := ReasoningLanguageBlock("zh")
	for _, want := range []string{"Write all visible reasoning/thinking text in Simplified Chinese", "for the entire turn", "does not override an explicit user request for the final answer language"} {
		if !strings.Contains(block, want) {
			t.Fatalf("zh reasoning block lost required anchor %q:\n%s", want, block)
		}
	}
}

func TestWithReasoningLanguageAutoInjectsNothing(t *testing.T) {
	// English reasoning is the stable LanguagePolicy default, so auto never
	// wraps a turn — regardless of the prompt's language or referenced content.
	for _, in := range []string{
		"explain this module",
		"hi",
		"解释 AuthHandler 的 panic",
		"Referenced context:\n\n<file path=\"auth.go\">\npackage main\n</file>\n\n解释 @auth.go 的报错",
	} {
		if got := WithReasoningLanguage(in, "auto"); got != in {
			t.Fatalf("auto reasoning language should not wrap %q, got %q", in, got)
		}
	}
}
