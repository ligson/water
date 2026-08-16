package taskplan

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/ligson/water/water-be/internal/hypothesisledger"
	"github.com/ligson/water/water-be/internal/store"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/workspace"
)

func TestBuildUserManagementPlan(t *testing.T) {
	plan := Build("task-1", "实现登录、注册和用户信息 CRUD", "code_change")
	if len(plan.Steps) != 6 {
		t.Fatalf("expected six user-management steps, got %d", len(plan.Steps))
	}
	want := []string{GateEvidence, GateRegister, GateLogin, GateUserCRUD, GateFrontendBuild, GateEndToEnd}
	for index, gate := range want {
		if plan.Steps[index].GateType != gate {
			t.Fatalf("step %d: expected gate %q, got %q", index+1, gate, plan.Steps[index].GateType)
		}
	}
}

func TestAssessReusesComprehensiveEvidenceAcrossAdjacentGates(t *testing.T) {
	db, taskID := openPlanTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	planStore := NewStore(db)
	plan, _, err := planStore.Ensure(ctx, taskID, goal, "code_change")
	if err != nil {
		t.Fatalf("ensure plan: %v", err)
	}

	ledger := hypothesisledger.NewStore(db)
	hypothesis, _, err := ledger.Ensure(ctx, taskID, goal, "current implementation state", nil)
	if err != nil {
		t.Fatalf("ensure hypothesis: %v", err)
	}
	evidence, err := ledger.AddEvidence(ctx, hypothesisledger.Evidence{
		TaskID: taskID, HypothesisID: hypothesis.ID, Kind: "file", Operation: "read",
		Source: "read_file", Resource: "WebSecurityConfig.java", Outcome: hypothesisledger.OutcomeNeutral,
	})
	if err != nil {
		t.Fatalf("add evidence: %v", err)
	}
	plan, advanced, err := planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "read_file", Succeeded: true, EvidenceID: evidence.ID,
	})
	if err != nil || !advanced || plan.Steps[0].Status != StatusCompleted || plan.Steps[1].Status != StatusInProgress {
		t.Fatalf("expected one direct observation to complete evidence step: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}

	output := `verify:register REGISTER_OK HTTP_STATUS:201 {"id":1}
verify:login LOGIN_OK HTTP_STATUS:200 {"token":"jwt"}
verify:user_crud USER_CRUD_OK`
	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", OutputText: output, Succeeded: true, EvidenceID: evidence.ID,
	})
	if err != nil || !advanced {
		t.Fatalf("assess comprehensive command: advanced=%v err=%v", advanced, err)
	}
	for index := 1; index <= 3; index++ {
		if plan.Steps[index].Status != StatusCompleted {
			t.Fatalf("expected step %d to be completed by shared evidence, got %q", index+1, plan.Steps[index].Status)
		}
	}
	if plan.Steps[4].Status != StatusInProgress {
		t.Fatalf("expected frontend build to remain current, got %q", plan.Steps[4].Status)
	}

	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Arguments: `{"command":"npm run build"}`, OutputText: "built successfully",
		Succeeded: true, EvidenceID: evidence.ID,
	})
	if err != nil || !advanced || plan.Steps[4].Status != StatusCompleted || plan.Steps[5].Status != StatusInProgress {
		t.Fatalf("expected frontend build gate to advance: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}

	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Purpose: "verify:e2e", OutputText: "WATER_E2E_OK",
		Succeeded: true, EvidenceID: evidence.ID,
	})
	if err != nil || !advanced || plan.Status != StatusCompleted || plan.Steps[5].Status != StatusCompleted {
		t.Fatalf("expected E2E evidence to complete plan: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}
}

func TestAssessParsesStructuredCommandOutput(t *testing.T) {
	db, taskID := openPlanTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	planStore := NewStore(db)
	plan, _, err := planStore.Ensure(ctx, taskID, goal, "code_change")
	if err != nil {
		t.Fatalf("ensure plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, StatusPending, plan.ID); err != nil {
		t.Fatalf("reset plan steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ? AND position = 2`, StatusInProgress, plan.ID); err != nil {
		t.Fatalf("activate register step: %v", err)
	}

	registerOutput := `{"command":"curl -X POST http://localhost:8889/api/users/register","output":"{\"id\":67,\"username\":\"testuser\"}\n201"}`
	plan, advanced, err := planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", OutputText: registerOutput, Succeeded: true,
	})
	if err != nil || !advanced || plan.Steps[1].Status != StatusCompleted || plan.Steps[2].Status != StatusInProgress {
		t.Fatalf("expected structured registration response to advance plan: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}

	loginOutput := `{"command":"curl -X POST http://localhost:8889/api/users/login","output":"{\"token\":\"jwt-value\"}\n200"}`
	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", OutputText: loginOutput, Succeeded: true,
	})
	if err != nil || !advanced || plan.Steps[2].Status != StatusCompleted || plan.Steps[3].Status != StatusInProgress {
		t.Fatalf("expected structured login response to advance plan: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}
}

func TestEndToEndGateRejectsCommandOnlyMarker(t *testing.T) {
	db, taskID := openPlanTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	planStore := NewStore(db)
	plan, _, err := planStore.Ensure(ctx, taskID, goal, "code_change")
	if err != nil {
		t.Fatalf("ensure plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, StatusCompleted, plan.ID); err != nil {
		t.Fatalf("complete fixture steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ? AND position = 6`, StatusInProgress, plan.ID); err != nil {
		t.Fatalf("activate E2E fixture step: %v", err)
	}
	plan, advanced, err := planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Arguments: `{"command":"./verify.sh # WATER_E2E_OK"}`,
		OutputText: "script exited without acceptance output", Succeeded: true,
	})
	if err != nil {
		t.Fatalf("assess command-only marker: %v", err)
	}
	if advanced || plan.Status == StatusCompleted {
		t.Fatalf("expected command-only marker not to complete E2E gate")
	}
}

func TestUserManagementIntegrationTestSatisfiesCRUDAndEndToEnd(t *testing.T) {
	db, taskID := openPlanTestDB(t)
	ctx := context.Background()
	goal := "实现登录、注册和用户信息 CRUD"
	planStore := NewStore(db)
	plan, _, err := planStore.Ensure(ctx, taskID, goal, "code_change")
	if err != nil {
		t.Fatalf("ensure plan: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ?`, StatusPending, plan.ID); err != nil {
		t.Fatalf("reset plan steps: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE task_plan_steps SET status = ? WHERE plan_id = ? AND position = 4`, StatusInProgress, plan.ID); err != nil {
		t.Fatalf("activate CRUD step: %v", err)
	}

	command := `/opt/maven/bin/mvn -q -Dtest=UserManagementIntegrationTest test`
	plan, advanced, err := planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Arguments: `{"command":"` + command + `"}`, Succeeded: true,
	})
	if err != nil || !advanced || plan.Steps[3].Status != StatusCompleted || plan.Steps[4].Status != StatusInProgress {
		t.Fatalf("expected integration test to complete CRUD gate: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}
	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Arguments: `{"command":"npm run build"}`, OutputText: "built successfully", Succeeded: true,
	})
	if err != nil || !advanced || plan.Steps[4].Status != StatusCompleted || plan.Steps[5].Status != StatusInProgress {
		t.Fatalf("expected frontend build gate to advance: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}
	plan, advanced, err = planStore.Assess(ctx, taskID, goal, ToolObservation{
		ToolName: "run_command", Arguments: `{"command":"` + command + `"}`, Succeeded: true,
	})
	if err != nil || !advanced || plan.Status != StatusCompleted {
		t.Fatalf("expected integration test after frontend build to complete E2E gate: advanced=%v err=%v plan=%#v", advanced, err, plan)
	}

	if isUserManagementIntegrationTest("mvn package -DskipTests UserManagementIntegrationTest") {
		t.Fatal("expected skipped tests not to satisfy integration acceptance")
	}
}

func openPlanTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "taskplan.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	ws, err := workspace.NewStore(db).Create(context.Background(), workspace.CreateInput{Name: "test", RootPath: t.TempDir()})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	item, err := task.NewStore(db).Create(context.Background(), task.CreateInput{WorkspaceID: ws.ID, Title: "test"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return db, item.ID
}
