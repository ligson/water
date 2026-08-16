package eval

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ligson/water/water-be/internal/event"
)

func TestAggregateReportsCompletionAndExecutionQuality(t *testing.T) {
	now := time.Now()
	tasks := []TaskReport{
		AssessTask("task-complete", []event.Event{
			{TaskID: "task-complete", TurnID: "turn-1", Type: "turn.started", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "tool.call.started", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "tool.call.corrected", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "tool.call.cached", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "agent.replan.requested", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "agent.recovery.suggested", CreatedAt: now},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "tool.failed", CreatedAt: now, PayloadJSON: `{"code":"target_is_directory"}`},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "tool.completed", CreatedAt: now, PayloadJSON: `{"name":"run_command","output":{"command":"go test ./...","output":"WATER_E2E_OK"}}`},
			{TaskID: "task-complete", TurnID: "turn-1", Type: "turn.completed", CreatedAt: now},
		}),
		AssessTask("task-blocked", []event.Event{
			{TaskID: "task-blocked", TurnID: "turn-2", Type: "turn.started", CreatedAt: now},
			{TaskID: "task-blocked", TurnID: "turn-2", Type: "turn.blocked", CreatedAt: now},
		}),
	}
	report := Aggregate(tasks)
	if report.CompletedTasks != 1 || report.BlockedTasks != 1 || report.EndToEndTasks != 1 {
		t.Fatalf("unexpected aggregate report: %#v", report)
	}
	if report.ObservedCompletion != 0.5 || report.Validations != 1 {
		t.Fatalf("expected completion and validation rates, got %#v", report)
	}
	if report.CorrectedToolCalls != 1 || report.CachedReads != 1 || report.StructuredErrors != 1 || report.Replans != 1 || report.RecoverySuggestions != 1 {
		t.Fatalf("expected recovery metrics, got %#v", report)
	}
	if _, err := MarshalReport(report); err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if _, err := json.Marshal(BuiltInCases()); err != nil {
		t.Fatalf("marshal built-in cases: %v", err)
	}
}
