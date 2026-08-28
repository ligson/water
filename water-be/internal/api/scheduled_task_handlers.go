package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/agent"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/provider"
	"github.com/ligson/water/water-be/internal/schedule"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/uid"
	"github.com/ligson/water/water-be/internal/workspace"
)

type scheduledTaskRequest struct {
	WorkspaceID          string `json:"workspaceId"`
	Name                 string `json:"name"`
	Prompt               string `json:"prompt"`
	ScheduleType         string `json:"scheduleType"`
	ScheduleExpression   string `json:"scheduleExpression"`
	Timezone             string `json:"timezone"`
	Enabled              bool   `json:"enabled"`
	MaxRetries           int    `json:"maxRetries"`
	RetryIntervalSeconds int    `json:"retryIntervalSeconds"`
}

func (r *Router) handleScheduledTasks(w http.ResponseWriter, req *http.Request) {
	store := schedule.NewStore(r.db)
	switch req.Method {
	case http.MethodGet:
		items, err := store.List(req.Context(), strings.TrimSpace(req.URL.Query().Get("workspaceId")))
		if err != nil {
			r.logger.ErrorContext(req.Context(), "list scheduled tasks", "error", err)
			WriteError(req.Context(), w, http.StatusInternalServerError, "list scheduled tasks failed")
			return
		}
		WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
	case http.MethodPost:
		input, ok := decodeScheduledTaskRequest(w, req)
		if !ok {
			return
		}
		if _, err := workspace.NewStore(r.db).Get(req.Context(), input.WorkspaceID); errors.Is(err, workspace.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
			return
		} else if err != nil {
			WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
			return
		}
		created, err := store.Create(req.Context(), scheduleInput(input))
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		r.scheduler.Start(context.Background())
		WriteJSON(req.Context(), w, http.StatusCreated, true, "scheduled task created", created)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleScheduledTaskByID(w http.ResponseWriter, req *http.Request, rest string) {
	id, action, ok := splitScheduledTaskPath(rest)
	if !ok {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}
	store := schedule.NewStore(r.db)
	if action != "" {
		r.handleScheduledTaskAction(w, req, store, id, action)
		return
	}
	switch req.Method {
	case http.MethodGet:
		item, err := store.Get(req.Context(), id)
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		WriteOK(req.Context(), w, "ok", item)
	case http.MethodPut:
		input, ok := decodeScheduledTaskRequest(w, req)
		if !ok {
			return
		}
		if _, err := workspace.NewStore(r.db).Get(req.Context(), input.WorkspaceID); err != nil {
			WriteError(req.Context(), w, http.StatusBadRequest, "workspace not found")
			return
		}
		updated, err := store.Update(req.Context(), id, scheduleInput(input))
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		WriteOK(req.Context(), w, "scheduled task updated", updated)
	case http.MethodDelete:
		if runs, listErr := store.ListRuns(req.Context(), id, 200); listErr == nil {
			for _, run := range runs {
				if !isScheduledRunActive(run.Status) || run.TaskID == "" {
					continue
				}
				r.cancelTaskRun(run.TaskID)
				if _, interruptErr := r.interruptActiveTurns(req.Context(), run.TaskID, "scheduled_task_deleted", "自动任务已删除，当前执行已停止。"); interruptErr != nil {
					r.logger.ErrorContext(req.Context(), "interrupt scheduled task before delete", "runId", run.ID, "error", interruptErr)
				}
			}
		} else {
			r.logger.ErrorContext(req.Context(), "list scheduled task runs before delete", "scheduledTaskId", id, "error", listErr)
		}
		if err := store.Delete(req.Context(), id); err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		WriteOK(req.Context(), w, "scheduled task deleted", map[string]interface{}{})
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func isScheduledRunActive(status string) bool {
	return status == schedule.RunQueued || status == schedule.RunRunning || status == schedule.RunWaitingApproval
}

func (r *Router) handleScheduledTaskAction(w http.ResponseWriter, req *http.Request, store *schedule.Store, id, action string) {
	if action == "runs" {
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if _, err := store.Get(req.Context(), id); err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		items, err := store.ListRuns(req.Context(), id, limit)
		if err != nil {
			WriteError(req.Context(), w, http.StatusInternalServerError, "list scheduled task runs failed")
			return
		}
		WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
		return
	}
	if req.Method != http.MethodPost {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := store.Get(req.Context(), id)
	if err != nil {
		writeScheduledTaskError(req.Context(), w, err)
		return
	}
	switch action {
	case "enable", "disable":
		updated, err := store.SetEnabled(req.Context(), id, action == "enable")
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		if updated.Enabled {
			r.scheduler.Start(context.Background())
		}
		WriteOK(req.Context(), w, "scheduled task state updated", updated)
	case "run-now":
		run, err := store.QueueManual(req.Context(), item)
		if errors.Is(err, schedule.ErrActiveRun) {
			WriteError(req.Context(), w, http.StatusConflict, "当前自动任务已有执行实例，请等待结束后再运行")
			return
		}
		if err != nil {
			WriteError(req.Context(), w, http.StatusInternalServerError, "queue scheduled task failed")
			return
		}
		r.scheduler.Start(context.Background())
		go r.scheduler.Wake(context.Background())
		WriteJSON(req.Context(), w, http.StatusAccepted, true, "scheduled task queued", run)
	default:
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
	}
}

func (r *Router) handleScheduledTaskRunByID(w http.ResponseWriter, req *http.Request, rest string) {
	parts := splitPath(rest)
	if len(parts) == 0 || len(parts) > 2 {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}
	store := schedule.NewStore(r.db)
	if len(parts) == 1 && req.Method == http.MethodGet {
		item, err := store.GetRun(req.Context(), parts[0])
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		WriteOK(req.Context(), w, "ok", item)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && req.Method == http.MethodPost {
		item, err := store.GetRun(req.Context(), parts[0])
		if err != nil {
			writeScheduledTaskError(req.Context(), w, err)
			return
		}
		if item.TaskID != "" {
			r.cancelTaskRun(item.TaskID)
			_, _ = r.interruptActiveTurns(req.Context(), item.TaskID, "scheduled_run_cancelled", "自动任务执行已取消。")
		}
		cancelled, err := store.CancelRun(req.Context(), item.ID)
		if errors.Is(err, schedule.ErrRunNotActive) {
			WriteError(req.Context(), w, http.StatusConflict, "scheduled task run is not active")
			return
		}
		if err != nil {
			WriteError(req.Context(), w, http.StatusInternalServerError, "cancel scheduled task run failed")
			return
		}
		WriteOK(req.Context(), w, "scheduled task run cancelled", cancelled)
		return
	}
	WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
}

func (r *Router) executeScheduledRun(ctx context.Context, item schedule.ScheduledTask, run schedule.Run) schedule.ExecutionResult {
	ws, err := workspace.NewStore(r.db).Get(ctx, item.WorkspaceID)
	if err != nil {
		return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "加载工作区失败：" + err.Error()}
	}
	if ws.DefaultProviderID == "" {
		if _, err := provider.NewStore(r.db).GetDefault(ctx); err != nil {
			return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "工作区没有可用的 Provider。"}
		}
	}
	requestID := "scheduled-" + run.ID
	title := fmt.Sprintf("[自动] %s · %s", item.Name, time.Now().In(mustLocation(item.Timezone)).Format("01-02 15:04"))
	createdTask, err := task.NewStore(r.db).Create(ctx, task.CreateInput{WorkspaceID: item.WorkspaceID, Title: title})
	if err != nil {
		return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "创建任务失败：" + err.Error()}
	}
	_, _ = r.appendTaskEvent(ctx, event.AppendInput{
		RequestID: requestID, WorkspaceID: item.WorkspaceID, TaskID: createdTask.ID,
		Type: "task.started", PayloadJSON: mustJSON(map[string]interface{}{
			"title": title, "source": "scheduled_task", "scheduledTaskId": item.ID, "scheduledRunId": run.ID,
		}),
	})
	turn, err := task.NewStore(r.db).CreateTurn(ctx, task.CreateTurnInput{TaskID: createdTask.ID, UserInput: run.PromptSnapshot})
	if err != nil {
		return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "创建执行轮次失败：" + err.Error()}
	}
	_, _ = r.appendTaskEvent(ctx, event.AppendInput{
		RequestID: requestID, WorkspaceID: item.WorkspaceID, TaskID: createdTask.ID, TurnID: turn.ID,
		Type: "turn.started", PayloadJSON: mustJSON(map[string]interface{}{
			"sequence": turn.Sequence, "userInput": turn.UserInput, "source": "scheduled_task", "scheduledRunId": run.ID,
		}),
	})
	if err := schedule.NewStore(r.db).BindRun(ctx, run.ID, createdTask.ID, turn.ID); err != nil {
		return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "绑定执行记录失败：" + err.Error()}
	}

	runCtx, cancel := context.WithCancel(ctx)
	runID := uid.New("run")
	r.mu.Lock()
	if previous, ok := r.cancel[createdTask.ID]; ok {
		previous.cancel()
	}
	r.cancel[createdTask.ID] = taskRun{id: runID, cancel: cancel}
	r.mu.Unlock()
	r.agent.RunTurn(runCtx, agent.RunTurnInput{
		RequestID: requestID, TaskID: createdTask.ID, TurnID: turn.ID,
		TurnSequence: turn.Sequence, WorkspaceID: item.WorkspaceID, UserInput: turn.UserInput,
	})
	cancel()
	r.mu.Lock()
	if current, ok := r.cancel[createdTask.ID]; ok && current.id == runID {
		delete(r.cancel, createdTask.ID)
	}
	r.mu.Unlock()

	updatedTurn, err := task.NewStore(r.db).GetTurn(context.Background(), turn.ID)
	if err != nil {
		return schedule.ExecutionResult{Status: schedule.RunFailed, ErrorMessage: "读取执行状态失败：" + err.Error()}
	}
	return schedule.ExecutionResult{
		Status:        runStatusFromTurn(updatedTurn.Status),
		ResultSummary: latestScheduledResult(context.Background(), r, createdTask.ID),
		ErrorMessage:  scheduledRunError(updatedTurn.Status),
	}
}

func latestScheduledResult(ctx context.Context, r *Router, taskID string) string {
	items, err := event.NewStore(r.db).ListByTask(ctx, taskID)
	if err != nil {
		return ""
	}
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Type != "agent.message.completed" {
			continue
		}
		var payload struct {
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(items[index].PayloadJSON), &payload) == nil && strings.TrimSpace(payload.Content) != "" {
			content := strings.TrimSpace(payload.Content)
			if len([]rune(content)) > 1200 {
				content = string([]rune(content)[:1200]) + "..."
			}
			return content
		}
	}
	return ""
}

func runStatusFromTurn(status string) string {
	switch status {
	case task.TurnStatusCompleted:
		return schedule.RunSucceeded
	case task.TurnStatusWaitingApproval:
		return schedule.RunWaitingApproval
	case task.TurnStatusInterrupted:
		return schedule.RunInterrupted
	default:
		return schedule.RunFailed
	}
}

func scheduledRunError(status string) string {
	switch status {
	case task.TurnStatusBlocked:
		return "自动任务缺少继续执行所需的信息。"
	case task.TurnStatusPaused:
		return "自动任务达到安全执行上限但尚未完成。"
	case task.TurnStatusFailed:
		return "自动任务执行失败，请查看完整任务日志。"
	case task.TurnStatusInterrupted:
		return "自动任务执行被中断。"
	default:
		return ""
	}
}

func decodeScheduledTaskRequest(w http.ResponseWriter, req *http.Request) (scheduledTaskRequest, bool) {
	defer req.Body.Close()
	var input scheduledTaskRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return scheduledTaskRequest{}, false
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.ScheduleType = strings.TrimSpace(input.ScheduleType)
	input.ScheduleExpression = strings.TrimSpace(input.ScheduleExpression)
	input.Timezone = strings.TrimSpace(input.Timezone)
	if input.Timezone == "" {
		input.Timezone = "Asia/Shanghai"
	}
	if input.RetryIntervalSeconds == 0 {
		input.RetryIntervalSeconds = 300
	}
	return input, true
}

func scheduleInput(input scheduledTaskRequest) schedule.CreateInput {
	return schedule.CreateInput{
		WorkspaceID: input.WorkspaceID, Name: input.Name, Prompt: input.Prompt,
		ScheduleType: input.ScheduleType, ScheduleExpression: input.ScheduleExpression,
		Timezone: input.Timezone, Enabled: input.Enabled, MaxRetries: input.MaxRetries,
		RetryIntervalSeconds: input.RetryIntervalSeconds,
	}
}

func splitScheduledTaskPath(rest string) (string, string, bool) {
	parts := splitPath(rest)
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func writeScheduledTaskError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schedule.ErrNotFound):
		WriteError(ctx, w, http.StatusNotFound, "scheduled task not found")
	case errors.Is(err, schedule.ErrInvalidSchedule):
		WriteError(ctx, w, http.StatusBadRequest, err.Error())
	default:
		WriteError(ctx, w, http.StatusInternalServerError, "scheduled task operation failed")
	}
}

func mustLocation(name string) *time.Location {
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return location
}
