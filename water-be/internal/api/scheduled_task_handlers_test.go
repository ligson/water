package api

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/schedule"
)

func TestScheduledTaskCRUDAndManualRun(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Scheduled", t.TempDir(), "request_approval")

	createBody := `{
  "workspaceId":"` + ws.ID + `",
  "name":"每日测试",
  "prompt":"运行项目测试并总结失败原因",
  "scheduleType":"daily",
  "scheduleExpression":"09:30",
  "timezone":"Asia/Shanghai",
  "enabled":false,
  "maxRetries":1,
  "retryIntervalSeconds":60
}`
	createRec := performJSON(handler, http.MethodPost, "/api/scheduled-tasks", createBody)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created scheduledTaskEnvelope
	decodeTestEnvelope(t, createRec, &created)
	if created.Data.Enabled || created.Data.NextRunAt != nil {
		t.Fatalf("expected disabled task without next run, got %#v", created.Data)
	}

	enableRec := performJSON(handler, http.MethodPost, "/api/scheduled-tasks/"+created.Data.ID+"/enable", "")
	if enableRec.Code != http.StatusOK {
		t.Fatalf("enable status %d: %s", enableRec.Code, enableRec.Body.String())
	}
	var enabled scheduledTaskEnvelope
	decodeTestEnvelope(t, enableRec, &enabled)
	if !enabled.Data.Enabled || enabled.Data.NextRunAt == nil {
		t.Fatalf("expected enabled task with next run, got %#v", enabled.Data)
	}

	listRec := performJSON(handler, http.MethodGet, "/api/scheduled-tasks?workspaceId="+ws.ID, "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed scheduledTaskListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 || listed.Data.Items[0].ID != created.Data.ID {
		t.Fatalf("unexpected scheduled task list %#v", listed.Data.Items)
	}

	runRec := performJSON(handler, http.MethodPost, "/api/scheduled-tasks/"+created.Data.ID+"/run-now", "")
	if runRec.Code != http.StatusAccepted {
		t.Fatalf("run now status %d: %s", runRec.Code, runRec.Body.String())
	}
	var queued scheduledTaskRunEnvelope
	decodeTestEnvelope(t, runRec, &queued)
	if queued.Data.TriggerType != schedule.TriggerManual {
		t.Fatalf("expected manual trigger, got %#v", queued.Data)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runsRec := performJSON(handler, http.MethodGet, "/api/scheduled-tasks/"+created.Data.ID+"/runs", "")
		if runsRec.Code != http.StatusOK {
			t.Fatalf("runs status %d: %s", runsRec.Code, runsRec.Body.String())
		}
		var runs scheduledTaskRunListEnvelope
		decodeTestEnvelope(t, runsRec, &runs)
		if len(runs.Data.Items) >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manual scheduled run was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestScheduledTaskRejectsUnsafeInterval(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Scheduled", t.TempDir(), "request_approval")
	body := `{"workspaceId":"` + ws.ID + `","name":"Too fast","prompt":"check","scheduleType":"interval","scheduleExpression":"30","timezone":"Asia/Shanghai","enabled":true,"retryIntervalSeconds":60}`
	rec := performJSON(handler, http.MethodPost, "/api/scheduled-tasks", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe interval status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

type scheduledTaskEnvelope struct {
	Data schedule.ScheduledTask `json:"data"`
}

type scheduledTaskListEnvelope struct {
	Data struct {
		Items []schedule.ScheduledTask `json:"items"`
	} `json:"data"`
}

type scheduledTaskRunEnvelope struct {
	Data schedule.Run `json:"data"`
}

type scheduledTaskRunListEnvelope struct {
	Data struct {
		Items []schedule.Run `json:"items"`
	} `json:"data"`
}
