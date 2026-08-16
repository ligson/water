package taskreplay

import (
	"encoding/json"
	"testing"

	"github.com/ligson/water/water-be/internal/event"
)

func TestAssessDetectsRepeatedReadsAcrossTurns(t *testing.T) {
	events := []event.Event{
		{TurnID: "turn-1", Type: "tool.completed", PayloadJSON: payload(t, "read_file", map[string]interface{}{"path": "/workspace/WebSecurityConfig.java"})},
		{TurnID: "turn-2", Type: "tool.completed", PayloadJSON: payload(t, "read_file", map[string]interface{}{"path": "/workspace/WebSecurityConfig.java"})},
		{TurnID: "turn-3", Type: "tool.completed", PayloadJSON: payload(t, "read_file", map[string]interface{}{"path": "/workspace/WebSecurityConfig.java"})},
	}

	report := Assess(events)
	if report.Turns != 3 || report.RepeatedReads != 2 {
		t.Fatalf("expected cross-turn repeated reads to be counted, got %#v", report)
	}
	if report.Score >= 55 {
		t.Fatalf("expected no-validation/no-e2e replay to receive a low score, got %d", report.Score)
	}
}

func TestAssessRecognizesEndToEndAcceptance(t *testing.T) {
	events := []event.Event{{
		TurnID: "turn-1", Type: "tool.completed",
		PayloadJSON: payload(t, "run_command", map[string]interface{}{
			"command": "./scripts/verify-e2e.sh # verify:e2e",
			"output":  "WATER_E2E_OK",
		}),
	}}
	report := Assess(events)
	if !report.EndToEndVerified || report.Validations != 1 || report.Score != 100 {
		t.Fatalf("expected complete E2E evidence, got %#v", report)
	}
}

func TestAssessCountsPausedTurnsSeparately(t *testing.T) {
	report := Assess([]event.Event{{TurnID: "turn-1", Type: "turn.paused"}})
	if report.PausedTurns != 1 || report.InterruptedTurns != 0 || report.FailedTurns != 0 {
		t.Fatalf("expected paused turn to remain distinct, got %#v", report)
	}
}

func TestAssessTreatsExplicitFalseSuccessAsFailedValidation(t *testing.T) {
	events := []event.Event{{
		TurnID: "turn-1", Type: "tool.completed",
		PayloadJSON: payload(t, "run_command", map[string]interface{}{
			"command": "go test ./...",
			"success": false,
			"output":  "FAIL",
		})}}
	report := Assess(events)
	if report.Validations != 1 || report.FailedValidations != 1 {
		t.Fatalf("expected success=false to fail validation, got %#v", report)
	}
}

func TestAssessCountsRecoverySuggestions(t *testing.T) {
	report := Assess([]event.Event{{TurnID: "turn-1", Type: "agent.recovery.suggested"}})
	if report.RecoverySuggestions != 1 {
		t.Fatalf("expected one recovery suggestion, got %#v", report)
	}
}

func payload(t *testing.T, name string, output map[string]interface{}) string {
	t.Helper()
	value, err := json.Marshal(map[string]interface{}{"name": name, "output": output})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return string(value)
}
