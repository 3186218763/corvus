package runtimepolicy

import "testing"

func TestParseSelections(t *testing.T) {
	g, err := ParseGuidanceSelection("  LIGHT ")
	if err != nil || g != GuidanceSelectionLight {
		t.Fatalf("guidance = %q err=%v", g, err)
	}
	if _, err := ParseGuidanceSelection("verbose"); err == nil {
		t.Fatal("invalid guidance should error")
	}
	c, err := ParseCompletionSelection("Verified")
	if err != nil || c != CompletionSelectionVerified {
		t.Fatalf("completion = %q err=%v", c, err)
	}
	if _, err := ParseCompletionSelection("paranoid"); err == nil {
		t.Fatal("invalid completion should error")
	}
	e, err := ParseExposureSelection("DEFERRED")
	if err != nil || e != ExposureSelectionDeferred {
		t.Fatalf("exposure = %q err=%v", e, err)
	}
	if _, err := ParseExposureSelection("minimal"); err == nil {
		t.Fatal("invalid exposure should error")
	}
	if _, err := ParsePreset("balanced"); err == nil {
		t.Fatal("balanced is a TokenMode alias, not a persisted preset")
	}
}

func TestRecordRoundTripAndTokenModeMigration(t *testing.T) {
	req := Request{
		Guidance:   GuidanceSelectionAuto,
		Completion: CompletionSelectionInherit,
		Exposure:   ExposureSelectionDeferred,
	}
	rec := RecordFromRequest(req)
	if rec.Version != RecordVersion {
		t.Fatalf("version = %d", rec.Version)
	}
	got, err := RequestFromRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if got != req {
		t.Fatalf("round-trip = %+v, want %+v", got, req)
	}

	legacy := RecordFromTokenMode("delivery")
	migrated, err := RequestFromRecord(legacy)
	if err != nil {
		t.Fatal(err)
	}
	want := Request{Guidance: GuidanceSelectionAuto, Completion: CompletionSelectionVerified, Exposure: ExposureSelectionEager}
	if migrated != want {
		t.Fatalf("legacy migrate = %+v, want %+v", migrated, want)
	}
}

func TestLegacyPresetMigratesToAxesAndClearsPreset(t *testing.T) {
	rec, changed, err := MigrateRecord(Record{Version: 1, Preset: string(PresetEconomy)})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || rec.Preset != "" || rec.Exposure != string(ExposureSelectionDeferred) || rec.Completion != string(CompletionSelectionStandard) {
		t.Fatalf("migrated legacy record = %+v changed=%v", rec, changed)
	}
	req, err := RequestFromRecord(Record{Version: 1, Preset: string(PresetDelivery)})
	if err != nil {
		t.Fatal(err)
	}
	if req.Preset != "" || req.Completion != CompletionSelectionVerified {
		t.Fatalf("delivery migration = %+v", req)
	}
	if migrated, changed, err := MigrateRecord(Record{Version: 1, Preset: "balanced"}); err != nil || !changed || migrated.Preset != "" {
		t.Fatalf("balanced migration = %+v changed=%v err=%v", migrated, changed, err)
	}
}

func TestOverlayRequestAndGuidancePrompt(t *testing.T) {
	base := Request{Guidance: GuidanceSelectionAuto, Completion: CompletionSelectionAuto, Exposure: ExposureSelectionAuto}
	got := OverlayRequest(base, Request{Guidance: GuidanceSelectionLight})
	if got.Preset != "" || got.Guidance != GuidanceSelectionLight || got.Completion != CompletionSelectionAuto {
		t.Fatalf("overlay = %+v", got)
	}
	if GuidancePrompt(GuidanceOff) != "" {
		t.Fatal("off guidance must have no fragment")
	}
	if GuidancePrompt(GuidanceLight) != GuidanceLightPrompt {
		t.Fatalf("light prompt = %q", GuidancePrompt(GuidanceLight))
	}
	if GuidancePrompt(GuidanceStructured) != GuidanceStructuredPrompt {
		t.Fatalf("structured prompt = %q", GuidancePrompt(GuidanceStructured))
	}
}
