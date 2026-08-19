package agent

import (
	"os"
	"path/filepath"
	"testing"

	"corvus/internal/runtimepolicy"
)

func TestSessionRuntimePolicyMigratesTokenMode(t *testing.T) {
	rec, ok := SessionRuntimePolicy(BranchMeta{TokenMode: "economy"})
	if !ok {
		t.Fatal("expected migrated record")
	}
	req, err := runtimepolicy.RequestFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if req.Preset != "" || req.Exposure != runtimepolicy.ExposureSelectionDeferred {
		t.Fatalf("migrated = %+v", req)
	}
}

func TestPersistAndLoadSessionRuntimePolicy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sess.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := runtimepolicy.RecordFromRequest(runtimepolicy.Request{
		Preset:     runtimepolicy.PresetFull,
		Guidance:   runtimepolicy.GuidanceSelectionLight,
		Completion: runtimepolicy.CompletionSelectionVerified,
		Exposure:   runtimepolicy.ExposureSelectionDeferred,
	})
	if err := PersistSessionRuntimePolicy(path, rec, "full"); err != nil {
		t.Fatal(err)
	}
	got, ok := LoadSessionRuntimePolicy(path)
	if !ok {
		t.Fatal("expected persisted record")
	}
	if got.Guidance != "light" || got.Completion != "verified" || got.Exposure != "deferred" {
		t.Fatalf("loaded = %+v", got)
	}
	meta, ok, err := LoadBranchMeta(path)
	if err != nil || !ok {
		t.Fatalf("meta ok=%v err=%v", ok, err)
	}
	if meta.TokenMode != "" {
		t.Fatalf("token_mode should be cleared, got %q", meta.TokenMode)
	}
}
