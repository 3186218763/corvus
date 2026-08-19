package boot

import (
	"testing"

	"corvus/internal/config"
)

func TestPlannerOffHasHighestPrecedence(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agent.PlannerModel = "configured-planner"
	if got := effectivePlannerModel(cfg, Options{}, false); got != "configured-planner" {
		t.Fatalf("ordinary planner model = %q", got)
	}
	if got := effectivePlannerModel(cfg, Options{DisablePlanner: true}, false); got != "" {
		t.Fatalf("planner-off returned %q, want disabled", got)
	}
	if got := effectivePlannerModel(cfg, Options{}, true); got != "configured-planner" {
		t.Fatalf("deferred exposure changed planner selection: %q", got)
	}
}
