package stats

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"corvus/internal/event"
	"corvus/internal/filelock"
	"corvus/internal/provider"
)

func flushRecorder(t *testing.T, recorder *Recorder) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recorder.Flush(ctx); err != nil {
		t.Fatalf("flush recorder: %v", err)
	}
}

// readStatsRecords decodes every record written under dir. The recorder writes
// one file per day; tests emit within a single day, so reading all files back
// is equivalent.
func readStatsRecords(t *testing.T, dir string) []record {
	t.Helper()
	var out []record
	for _, f := range dailyJSONLFiles(t, dir) {
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			t.Fatalf("read stats file: %v", err)
		}
		recs, err := decodeRecords(strings.NewReader(string(b)))
		if err != nil {
			t.Fatalf("decode stats file: %v", err)
		}
		out = append(out, recs...)
	}
	return out
}

func TestRecorderWritesDailyFile(t *testing.T) {
	dir := t.TempDir()
	inner := &spySink{}
	r := NewRecorder(inner, dir, "desktop")

	r.Emit(usageEvent("deepseek/deepseek-v4-flash", 100, 50, 10, 20, 30, 150))
	r.Emit(usageEvent("deepseek/deepseek-v4-pro", 200, 100, 0, 0, 0, 300))
	r.Emit(turnEvent())
	flushRecorder(t, r)

	// The daily file must exist with three lines (2 usage + 1 turn marker).
	files := dailyJSONLFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want 1 daily file, got %d", len(files))
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 {
		t.Fatalf("want 3 lines, got %d", lines)
	}
	// Forwarding must be untouched.
	if len(inner.events) != 3 {
		t.Fatalf("want 3 forwarded events, got %d", len(inner.events))
	}
}

func TestRecorderCountsMergedProviderRequests(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(&spySink{}, dir, "desktop")
	e := usageEvent("deepseek/deepseek-v4-pro", 100, 50, 10, 0, 100, 150)
	e.Usage.RequestCount = 2
	r.Emit(e)
	flushRecorder(t, r)

	recs := readStatsRecords(t, dir)
	if len(recs) != 1 || recs[0].Requests != 2 {
		t.Fatalf("merged requests = %+v, want 1 record with requests=2", recs)
	}
}

func TestRecorderCapturesGuardianUsageAndPreservesProtocolAudit(t *testing.T) {
	dir := t.TempDir()
	inner := &auditSpySink{}
	r := NewRecorder(inner, dir, "desktop")
	r.Emit(event.Event{
		Kind:     event.GuardianAssessment,
		ModelRef: "deepseek/deepseek-v4-flash",
		Guardian: event.GuardianResult{Usage: &provider.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
	})
	event.RecordProtocolRecovery(r, event.ProtocolRecoveryAudit{Kind: event.ProtocolRecoveryMissingReasoningRetryRecovered})
	flushRecorder(t, r)

	recs := readStatsRecords(t, dir)
	if len(recs) != 1 || recs[0].Total != 15 || recs[0].ModelRef != "deepseek/deepseek-v4-flash" {
		t.Fatalf("guardian usage = %+v, want 1 record total=15 for deepseek/deepseek-v4-flash", recs)
	}
	if len(inner.protocol) != 1 || inner.protocol[0].Kind != event.ProtocolRecoveryMissingReasoningRetryRecovered {
		t.Fatalf("protocol audit was not forwarded: %+v", inner.protocol)
	}
}

func TestRecorderSkipsZeroUsage(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(&spySink{}, dir, "desktop")
	r.Emit(usageEvent("m", 0, 0, 0, 0, 0, 0)) // TotalTokens <= 0 -> skipped
	r.Emit(turnEvent())
	flushRecorder(t, r)
	files := dailyJSONLFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("want 1 file (turn only), got %d", len(files))
	}
}

func TestRecorderPersistsRequestOnlyFailureWithoutForwardingReceipt(t *testing.T) {
	dir := t.TempDir()
	inner := &spySink{}
	r := NewRecorder(inner, dir, "desktop")
	r.Emit(event.Event{
		Kind:     event.Usage,
		ModelRef: "deepseek/deepseek-v4-pro",
		Usage:    &provider.Usage{RequestCount: 3},
	})
	flushRecorder(t, r)

	recs := readStatsRecords(t, dir)
	if len(recs) != 1 || recs[0].Requests != 3 || recs[0].Total != 0 {
		t.Fatalf("request-only record = %+v, want requests=3 total=0", recs)
	}
	if len(inner.events) != 0 {
		t.Fatalf("request-only usage forwarded %d zero-token receipts", len(inner.events))
	}
}

func TestRecorderNeverWaitsForStatsFileLock(t *testing.T) {
	dir := t.TempDir()
	release, err := filelock.Acquire(context.Background(), filepath.Join(dir, ".append.lock"))
	if err != nil {
		t.Fatalf("hold stats lock: %v", err)
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()

	inner := &spySink{}
	recorder := NewRecorder(inner, dir, "desktop")
	emitted := make(chan struct{})
	go func() {
		recorder.Emit(usageEvent("deepseek/model", 10, 4, 0, 0, 10, 14))
		close(emitted)
	}()

	select {
	case <-emitted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stats file lock blocked event forwarding")
	}
	if len(inner.events) != 1 {
		t.Fatalf("forwarded events = %d, want 1", len(inner.events))
	}

	release()
	locked = false
	flushRecorder(t, recorder)
	recs := readStatsRecords(t, dir)
	if len(recs) != 1 || recs[0].Total != 14 {
		t.Fatalf("tokens after lock release = %+v, want 1 record total=14", recs)
	}
}

func TestRecorderDisabledOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	r := NewRecorder(&spySink{}, "", "desktop")
	r.Emit(usageEvent("m", 1, 1, 0, 0, 0, 2))
	r.Emit(turnEvent())
	// No panic, nothing written.
	if files := dailyJSONLFiles(t, dir); len(files) != 0 {
		t.Fatalf("disabled recorder wrote %d files", len(files))
	}
}

func TestDecodeRecordsSkipsMalformed(t *testing.T) {
	// A torn or hand-edited line must not fail the whole day's read: it is
	// skipped and the surrounding valid records still come through.
	good := `{"ts":"2026-08-02T10:00:00+08:00","total":100}` + "\n"
	bad := `{"ts":"2026-08-02T10:00:00+08:00","total":` + "\n" // truncated JSON
	recs, err := decodeRecords(strings.NewReader(good + bad + bad + good))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 valid records, got %d", len(recs))
	}
	for _, r := range recs {
		if r.Total != 100 {
			t.Fatalf("record total: want 100, got %d", r.Total)
		}
	}
}

func TestAppendRepairsTornTrailingRecord(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir)
	now := time.Now()
	path := filepath.Join(dir, now.Format(dayLayout)+".jsonl")
	if err := os.WriteFile(path, []byte(`{"ts":"2026-08-02T10:00:00+08:00","total":`), 0o600); err != nil {
		t.Fatalf("seed torn record: %v", err)
	}
	if err := w.Append(record{Timestamp: now, ModelRef: "deepseek/deepseek-v4-flash", Total: 42}); err != nil {
		t.Fatalf("append after torn record: %v", err)
	}
	recs := readStatsRecords(t, dir)
	if len(recs) != 1 || recs[0].Total != 42 || recs[0].ModelRef != "deepseek/deepseek-v4-flash" {
		t.Fatalf("recovered records = %+v", recs)
	}
}

func TestConcurrentWritersAppendWholeRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	const writers = 8
	const perWriter = 40
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(model int) {
			defer wg.Done()
			w := NewWriter(dir)
			for j := 0; j < perWriter; j++ {
				if err := w.Append(record{Timestamp: now, ModelRef: fmt.Sprintf("provider/model-%d", model), Total: 1}); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	recs := readStatsRecords(t, dir)
	if len(recs) != writers*perWriter {
		t.Fatalf("records = %d, want %d", len(recs), writers*perWriter)
	}
}

// --- test helpers ---

func dailyJSONLFiles(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	files := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			files = append(files, entry)
		}
	}
	return files
}

type spySink struct{ events []event.Event }

func (s *spySink) Emit(e event.Event) { s.events = append(s.events, e) }

type auditSpySink struct {
	events   []event.Event
	protocol []event.ProtocolRecoveryAudit
}

func (s *auditSpySink) Emit(e event.Event) { s.events = append(s.events, e) }
func (s *auditSpySink) RecordProtocolRecovery(a event.ProtocolRecoveryAudit) {
	s.protocol = append(s.protocol, a)
}

func usageEvent(model string, prompt, completion, reasoning, hit, miss, total int) event.Event {
	return event.Event{
		Kind:     event.Usage,
		ModelRef: model,
		Usage: &provider.Usage{
			PromptTokens:     prompt,
			CompletionTokens: completion,
			ReasoningTokens:  reasoning,
			CacheHitTokens:   hit,
			CacheMissTokens:  miss,
			TotalTokens:      total,
		},
	}
}

func turnEvent() event.Event { return event.Event{Kind: event.TurnDone} }
