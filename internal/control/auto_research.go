package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"corvus/internal/autoresearch"
)

type autoResearchSetup struct {
	taskID      string
	createToken string
	blockReason string
	notice      string
	created     bool
}

func (c *Controller) prepareAutoResearchTask(goal string, researchMode GoalResearchMode, createToken string) autoResearchSetup {
	goal = strings.TrimSpace(goal)
	if goal == "" || c.autoResearch == nil || !shouldUseAutoResearch(goal, researchMode) {
		return autoResearchSetup{}
	}
	currentGoal, currentStatus, _, currentTaskID := c.goals.snapshot()
	if strings.TrimSpace(currentGoal) == goal && currentStatus == GoalStatusRunning && strings.TrimSpace(currentTaskID) != "" {
		return autoResearchSetup{taskID: currentTaskID}
	}
	if task, ok, err := c.autoResearch.ResumeFromGoalText(goal); err != nil {
		slog.Warn("controller: resume autoresearch task", "err", err)
		if ok {
			return autoResearchSetup{blockReason: err.Error()}
		}
	} else if ok {
		return autoResearchSetup{taskID: task.ID, notice: "autoresearch task resumed: " + task.ID}
	}
	task, err := c.autoResearch.CreateTask(goal, autoresearch.CreateOptions{
		CreateToken: createToken,
		AllowedOperations: autoresearch.AllowedOperations{
			Write:   true,
			Network: false,
			Publish: false,
		},
		SuccessCriteria: defaultAutoResearchSuccessCriteria(),
	})
	if err != nil {
		slog.Warn("controller: create autoresearch task", "err", err)
		return autoResearchSetup{}
	}
	return autoResearchSetup{
		taskID:      task.ID,
		createToken: task.CreateToken,
		notice:      "autoresearch task created: " + task.ID,
		created:     true,
	}
}

func defaultAutoResearchSuccessCriteria() []autoresearch.SuccessCriterion {
	return []autoresearch.SuccessCriterion{
		{
			ID:          "objective_evidence",
			Description: "The goal outcome is supported by direct evidence, such as inspected code, reproduced behavior, source material, or concrete findings.",
			Required:    true,
		},
		{
			ID:          "verification",
			Description: "The result has relevant verification evidence, such as tests, commands, benchmarks, manual checks, or a documented reason why verification is not applicable.",
			Required:    true,
		},
	}
}

func (c *Controller) appendAutoResearchHeartbeat(taskID, status, message string) {
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	iteration := 0
	if summary, err := c.autoResearch.Summary(taskID); err == nil {
		iteration = summary.Iteration
	}
	if err := c.autoResearch.AppendHeartbeat(taskID, autoresearch.Heartbeat{
		Status:    status,
		Iteration: iteration,
		Message:   message,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		slog.Warn("controller: append autoresearch heartbeat", "task_id", taskID, "status", status, "err", err)
	}
}

func (c *Controller) autoResearchAcceptedEvidenceIDs(taskID string) map[string]bool {
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	findings, err := c.autoResearch.Findings(taskID, 0)
	if err != nil {
		slog.Warn("controller: read autoresearch findings", "task_id", taskID, "err", err)
		return nil
	}
	accepted := make(map[string]bool, len(findings))
	for _, finding := range findings {
		if finding.Accepted {
			accepted[finding.ID] = true
		}
	}
	return accepted
}

func (c *Controller) recordAutoResearchTurnProgress(taskID string, acceptedBefore map[string]bool) {
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	acceptedAfter := c.autoResearchAcceptedEvidenceIDs(taskID)
	newAccepted := make([]string, 0)
	for id := range acceptedAfter {
		if acceptedBefore == nil || !acceptedBefore[id] {
			newAccepted = append(newAccepted, id)
		}
	}
	sort.Strings(newAccepted)
	summary := autoResearchDirectionSummary(lastAssistantText(c.History()))
	if _, err := c.autoResearch.RecordDirection(taskID, autoresearch.Direction{
		Summary:             summary,
		AcceptedEvidenceIDs: newAccepted,
		Now:                 time.Now().UTC(),
	}); err != nil {
		slog.Warn("controller: record autoresearch direction", "task_id", taskID, "err", err)
	}
}

func (c *Controller) recordAutoResearchEvidenceFromAssistant(taskID, text string) {
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	for _, item := range parseAutoResearchEvidenceBlocks(text) {
		if err := c.recordAutoResearchEvidenceForTask(taskID, item.CriterionID, AutoResearchEvidenceInput{
			ID:       item.ID,
			Kind:     item.Kind,
			Summary:  item.Summary,
			Source:   item.Source,
			Command:  item.Command,
			Paths:    append([]string(nil), item.Paths...),
			Accepted: item.Accepted,
		}); err != nil {
			slog.Warn("controller: record autoresearch evidence block", "task_id", taskID, "criterion_id", item.CriterionID, "err", err)
		}
	}
}

type autoResearchEvidenceBlock struct {
	CriterionID string   `json:"criterion_id"`
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Summary     string   `json:"summary"`
	Source      string   `json:"source"`
	Command     string   `json:"command"`
	Paths       []string `json:"paths"`
	Accepted    bool     `json:"accepted"`
}

const (
	autoResearchEvidenceOpen  = "<autoresearch-evidence>"
	autoResearchEvidenceClose = "</autoresearch-evidence>"
)

func parseAutoResearchEvidenceBlocks(text string) []autoResearchEvidenceBlock {
	var out []autoResearchEvidenceBlock
	rest := text
	for {
		start := strings.Index(rest, autoResearchEvidenceOpen)
		if start < 0 {
			return out
		}
		rest = rest[start+len(autoResearchEvidenceOpen):]
		end := strings.Index(rest, autoResearchEvidenceClose)
		if end < 0 {
			return out
		}
		raw := strings.TrimSpace(rest[:end])
		rest = rest[end+len(autoResearchEvidenceClose):]
		if raw == "" {
			continue
		}
		var many []autoResearchEvidenceBlock
		if err := json.Unmarshal([]byte(raw), &many); err == nil {
			out = append(out, many...)
			continue
		}
		var one autoResearchEvidenceBlock
		if err := json.Unmarshal([]byte(raw), &one); err == nil {
			out = append(out, one)
		}
	}
}

func autoResearchDirectionSummary(text string) string {
	text = stripAutoResearchEvidenceBlocks(text)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if line == "" || strings.HasPrefix(lower, "[goal:") {
			continue
		}
		if len(line) > 160 {
			line = line[:160]
		}
		return line
	}
	return "turn completed"
}

func stripAutoResearchEvidenceBlocks(text string) string {
	var b strings.Builder
	rest := text
	for {
		start := strings.Index(rest, autoResearchEvidenceOpen)
		if start < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:start])
		afterOpen := rest[start+len(autoResearchEvidenceOpen):]
		end := strings.Index(afterOpen, autoResearchEvidenceClose)
		if end < 0 {
			return b.String()
		}
		rest = afterOpen[end+len(autoResearchEvidenceClose):]
	}
}

func (c *Controller) autoResearchReadinessFailure() string {
	taskID := c.goals.currentAutoResearchTaskID()
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return ""
	}
	report, err := c.autoResearch.Readiness(taskID)
	if err != nil {
		return "AutoResearch readiness check failed: " + err.Error()
	}
	if report.Ready {
		return ""
	}
	var parts []string
	if len(report.MissingCriteria) > 0 {
		parts = append(parts, "missing criteria: "+strings.Join(report.MissingCriteria, ", "))
	}
	if report.BlockedReason != "" {
		parts = append(parts, "blocked: "+report.BlockedReason)
	}
	if len(report.Errors) > 0 {
		parts = append(parts, "state errors: "+strings.Join(report.Errors, "; "))
	}
	if len(parts) == 0 {
		parts = append(parts, "task is not ready")
	}
	return "AutoResearch readiness check failed: " + strings.Join(parts, "; ")
}

func (c *Controller) recordAutoResearchEvidenceForTask(taskID, criterionID string, input AutoResearchEvidenceInput) error {
	if c.autoResearch == nil || strings.TrimSpace(taskID) == "" {
		return errors.New("autoresearch: no active task")
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = c.nextAutoResearchFindingID(taskID)
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = autoresearch.FindingKindManual
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = autoresearch.FindingSourceManual
	}
	finding := autoresearch.Finding{
		ID:        id,
		Kind:      kind,
		Summary:   strings.TrimSpace(input.Summary),
		Source:    source,
		Command:   strings.TrimSpace(input.Command),
		Paths:     append([]string(nil), input.Paths...),
		Accepted:  input.Accepted,
		CreatedAt: time.Now().UTC(),
	}
	return c.autoResearch.RecordEvidence(taskID, criterionID, finding)
}

func (c *Controller) nextAutoResearchFindingID(taskID string) string {
	findings, err := c.autoResearch.Findings(taskID, 0)
	if err != nil {
		return fmt.Sprintf("f%d", time.Now().UTC().UnixNano())
	}
	used := make(map[string]bool, len(findings))
	for _, finding := range findings {
		used[finding.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("f%d", len(findings)+i)
		if !used[id] {
			return id
		}
	}
}
