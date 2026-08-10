package skill_test

import (
	"strings"
	"testing"

	"corvus/internal/skill"
)

func TestCorvusGuideBuiltinRegistered(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir(), DisableBuiltins: false})
	sk, ok := store.Read("corvus-guide")
	if !ok {
		t.Fatal("corvus-guide must be registered as a builtin")
	}
	if sk.Scope != skill.ScopeBuiltin {
		t.Fatalf("scope = %s", sk.Scope)
	}
	if sk.RunAs != skill.RunInline {
		t.Fatalf("runAs = %s", sk.RunAs)
	}
	if sk.Description == "" {
		t.Fatal("description required for index line")
	}
	if !strings.Contains(sk.Body, "corvus --help") {
		t.Fatal("body missing CLI help guidance")
	}
}

func TestCorvusGuideIndexLineOnly(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	list := store.List()
	var guide skill.Skill
	found := false
	for _, s := range list {
		if s.Name == "corvus-guide" {
			guide = s
			found = true
			break
		}
	}
	if !found {
		t.Fatal("corvus-guide missing from List")
	}
	idx := skill.IndexBlock(list)
	if !strings.Contains(idx, "corvus-guide") {
		t.Fatal("index missing corvus-guide line")
	}
	// Body must not appear in the index block.
	if strings.Contains(idx, "First action") || strings.Contains(idx, skBodySnippet(guide)) {
		t.Fatal("skill body leaked into system-prompt index")
	}
	// Exactly one index line for the skill name.
	if c := strings.Count(idx, "- corvus-guide"); c != 1 {
		t.Fatalf("index lines for corvus-guide = %d, want 1", c)
	}
}

func skBodySnippet(sk skill.Skill) string {
	body := strings.TrimSpace(sk.Body)
	if len(body) > 40 {
		return body[:40]
	}
	return body
}

func TestCorvusGuideOverriddenByProject(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	store := skill.New(skill.Options{HomeDir: home, ProjectRoot: root})
	// Create project override.
	path, err := store.CreateWithContent("corvus-guide", skill.ScopeProject, "---\ndescription: override\nrunAs: inline\n---\nproject body\n")
	if err != nil {
		t.Fatal(err)
	}
	_ = path
	store2 := skill.New(skill.Options{HomeDir: home, ProjectRoot: root})
	sk, ok := store2.Read("corvus-guide")
	if !ok {
		t.Fatal("expected override")
	}
	if sk.Scope != skill.ScopeProject {
		t.Fatalf("scope = %s, want project", sk.Scope)
	}
	if !strings.Contains(sk.Body, "project body") {
		t.Fatalf("body = %q", sk.Body)
	}
}

func TestCorvusGuideDisabled(t *testing.T) {
	store := skill.New(skill.Options{
		HomeDir:       t.TempDir(),
		DisabledNames: []string{"corvus-guide"},
	})
	if _, ok := store.Read("corvus-guide"); ok {
		t.Fatal("disabled builtin should not be readable")
	}
	for _, s := range store.List() {
		if s.Name == "corvus-guide" {
			t.Fatal("disabled builtin should not be listed")
		}
	}
}

func TestCorvusGuideIndexStableAcrossCalls(t *testing.T) {
	store := skill.New(skill.Options{HomeDir: t.TempDir()})
	a := skill.IndexBlock(store.List())
	b := skill.IndexBlock(store.List())
	if a != b {
		t.Fatal("skills index not byte-stable across List calls")
	}
}
