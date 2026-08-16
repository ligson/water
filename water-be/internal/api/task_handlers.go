package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/ligson/water/water-be/internal/agent"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/provider"
	"github.com/ligson/water/water-be/internal/realtime"
	"github.com/ligson/water/water-be/internal/requestid"
	"github.com/ligson/water/water-be/internal/task"
	"github.com/ligson/water/water-be/internal/uid"
	"github.com/ligson/water/water-be/internal/workspace"
)

type taskRequest struct {
	Title string `json:"title"`
}

type turnRequest struct {
	UserInput   string                  `json:"userInput"`
	Attachments []turnAttachmentRequest `json:"attachments"`
}

func (r *Router) handleWorkspaceTasks(w http.ResponseWriter, req *http.Request, workspaceID string) {
	switch req.Method {
	case http.MethodGet:
		r.listTasks(w, req, workspaceID)
	case http.MethodPost:
		r.createTask(w, req, workspaceID)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleTaskByID(w http.ResponseWriter, req *http.Request, rest string) {
	taskID, action, ok := splitTaskPath(rest)
	if !ok {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}

	if action == "turns" {
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.createTurn(w, req, taskID)
		return
	}

	if action == "events" {
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.listTaskEvents(w, req, taskID)
		return
	}

	if action == "attachments" {
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.readTaskAttachment(w, req, taskID)
		return
	}

	if action == "tools" {
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.executeTaskTool(w, req, taskID)
		return
	}

	if action == "cancel" {
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.cancelTask(w, req, taskID)
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.getTask(w, req, taskID)
	case http.MethodPut:
		r.updateTask(w, req, taskID)
	case http.MethodDelete:
		r.deleteTask(w, req, taskID)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) listTasks(w http.ResponseWriter, req *http.Request, workspaceID string) {
	if _, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID); errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	} else if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for tasks", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	items, err := task.NewStore(r.db).ListByWorkspace(req.Context(), workspaceID)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list tasks", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list tasks failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) createTask(w http.ResponseWriter, req *http.Request, workspaceID string) {
	input, ok := decodeTaskRequest(w, req)
	if !ok {
		return
	}
	if _, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID); errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	} else if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for task create", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}

	created, err := task.NewStore(r.db).Create(req.Context(), task.CreateInput{
		WorkspaceID: workspaceID,
		Title:       input.Title,
	})
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create task", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create task failed")
		return
	}

	if _, err := r.appendTaskEvent(req.Context(), event.AppendInput{
		RequestID:   requestid.FromContext(req.Context()),
		WorkspaceID: workspaceID,
		TaskID:      created.ID,
		Type:        "task.started",
		PayloadJSON: `{"title":` + quoteJSON(created.Title) + `}`,
	}); err != nil {
		r.logger.ErrorContext(req.Context(), "append task started event", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create task event failed")
		return
	}

	WriteJSON(req.Context(), w, http.StatusCreated, true, "task created", created)
}

func (r *Router) getTask(w http.ResponseWriter, req *http.Request, taskID string) {
	item, err := task.NewStore(r.db).Get(req.Context(), taskID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get task", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}
	WriteOK(req.Context(), w, "ok", item)
}

func (r *Router) updateTask(w http.ResponseWriter, req *http.Request, taskID string) {
	input, ok := decodeTaskRequest(w, req)
	if !ok {
		return
	}

	updated, err := task.NewStore(r.db).Update(req.Context(), taskID, task.UpdateInput{
		Title: input.Title,
	})
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "update task", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "update task failed")
		return
	}
	WriteOK(req.Context(), w, "task updated", updated)
}

func (r *Router) deleteTask(w http.ResponseWriter, req *http.Request, taskID string) {
	r.cancelTaskRun(taskID)
	if _, err := r.interruptActiveTurns(req.Context(), taskID, "task_deleted", "任务已删除，当前执行已停止。"); err != nil {
		r.logger.ErrorContext(req.Context(), "interrupt task turns before delete", "error", err)
	}

	err := task.NewStore(r.db).Delete(req.Context(), taskID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "delete task", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "delete task failed")
		return
	}
	WriteOK(req.Context(), w, "task deleted", map[string]interface{}{})
}

func (r *Router) createTurn(w http.ResponseWriter, req *http.Request, taskID string) {
	input, ok := decodeTurnRequest(w, req)
	if !ok {
		return
	}

	currentTask, err := task.NewStore(r.db).Get(req.Context(), taskID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get task for turn", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}
	ws, err := workspace.NewStore(r.db).Get(req.Context(), currentTask.WorkspaceID)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for turn attachments", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}
	attachments, attachmentDir, err := storeTurnAttachments(ws.RootPath, taskID, input.Attachments)
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}
	keepAttachments := false
	defer func() {
		if !keepAttachments && attachmentDir != "" {
			_ = os.RemoveAll(attachmentDir)
		}
	}()
	if input.UserInput == "" {
		input.UserInput = "请查看并处理本轮上传的附件。"
	}

	created, err := task.NewStore(r.db).CreateTurn(req.Context(), task.CreateTurnInput{
		TaskID:      taskID,
		UserInput:   input.UserInput,
		Attachments: attachments,
	})
	if errors.Is(err, task.ErrTaskHasActiveTurn) {
		WriteError(req.Context(), w, http.StatusConflict, "当前任务仍在执行，请等待完成、审批或打断后再继续")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create turn", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create turn failed")
		return
	}
	keepAttachments = true

	payload, err := json.Marshal(map[string]interface{}{
		"sequence":    created.Sequence,
		"userInput":   created.UserInput,
		"attachments": created.Attachments,
	})
	if err != nil {
		WriteError(req.Context(), w, http.StatusInternalServerError, "create turn event failed")
		return
	}
	if _, err := r.appendTaskEvent(req.Context(), event.AppendInput{
		RequestID:   requestid.FromContext(req.Context()),
		WorkspaceID: currentTask.WorkspaceID,
		TaskID:      taskID,
		TurnID:      created.ID,
		Type:        "turn.started",
		PayloadJSON: string(payload),
	}); err != nil {
		r.logger.ErrorContext(req.Context(), "append turn started event", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create turn event failed")
		return
	}

	r.startAgentTurn(req, currentTask, created)

	WriteJSON(req.Context(), w, http.StatusCreated, true, "turn created", created)
}

func (r *Router) appendTaskEvent(ctx context.Context, input event.AppendInput) (event.Event, error) {
	created, err := event.NewStore(r.db).Append(ctx, input)
	if err != nil {
		return event.Event{}, err
	}
	if r.hub != nil && created.TaskID != "" {
		r.hub.Publish(created.TaskID, realtime.FromEvent(created))
	}
	return created, nil
}

func (r *Router) startAgentTurn(req *http.Request, currentTask task.Task, turn task.Turn) {
	if r.agent == nil {
		return
	}
	if !r.hasProviderForTask(req.Context(), currentTask) {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	runID := uid.New("run")
	r.mu.Lock()
	if previous, ok := r.cancel[currentTask.ID]; ok {
		previous.cancel()
	}
	r.cancel[currentTask.ID] = taskRun{id: runID, cancel: cancel}
	r.mu.Unlock()

	input := agent.RunTurnInput{
		RequestID:    requestid.FromContext(req.Context()),
		TaskID:       currentTask.ID,
		TurnID:       turn.ID,
		TurnSequence: turn.Sequence,
		WorkspaceID:  currentTask.WorkspaceID,
		UserInput:    turn.UserInput,
		Attachments:  turn.Attachments,
	}
	go func() {
		defer func() {
			r.mu.Lock()
			if current, ok := r.cancel[currentTask.ID]; ok && current.id == runID {
				delete(r.cancel, currentTask.ID)
			}
			r.mu.Unlock()
		}()
		r.agent.RunTurn(ctx, input)
	}()
}

func (r *Router) hasProviderForTask(ctx context.Context, currentTask task.Task) bool {
	ws, err := workspace.NewStore(r.db).Get(ctx, currentTask.WorkspaceID)
	if err != nil {
		return false
	}
	if ws.DefaultProviderID != "" {
		return true
	}
	_, err = provider.NewStore(r.db).GetDefault(ctx)
	return err == nil
}

func (r *Router) cancelTask(w http.ResponseWriter, req *http.Request, taskID string) {
	if _, err := task.NewStore(r.db).Get(req.Context(), taskID); errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	} else if err != nil {
		r.logger.ErrorContext(req.Context(), "get task for cancel", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}

	interrupted, err := r.interruptActiveTurns(req.Context(), taskID, "user_cancelled", "你已打断任务，当前执行已停止。")
	if err != nil {
		r.logger.ErrorContext(req.Context(), "interrupt active turns", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "cancel task failed")
		return
	}
	ok := r.cancelTaskRun(taskID)

	if ok || interrupted {
		WriteOK(req.Context(), w, "task cancelled", map[string]interface{}{})
		return
	}
	WriteOK(req.Context(), w, "task is not running", map[string]interface{}{})
}

func (r *Router) interruptActiveTurns(ctx context.Context, taskID string, reason string, message string) (bool, error) {
	items, err := task.NewStore(r.db).ListActiveTurnsByTask(ctx, taskID)
	if err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}
	interruptedAny := false
	for _, item := range items {
		turn, err := task.NewStore(r.db).UpdateActiveTurnStatus(ctx, item.ID, task.TurnStatusInterrupted)
		if errors.Is(err, task.ErrTurnNotActive) {
			continue
		}
		if errors.Is(err, task.ErrNotFound) {
			continue
		}
		if err != nil {
			return interruptedAny, err
		}
		interruptedAny = true
		payload := map[string]interface{}{
			"reason":             reason,
			"message":            message,
			"canContinue":        true,
			"continuationPrompt": "继续上一轮任务，先确认已经完成的结果，再接着推进剩余工作，不要重复已完成内容。",
		}
		if reason == "task_deleted" {
			payload["canContinue"] = false
			payload["continuationPrompt"] = ""
		}
		raw, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return interruptedAny, marshalErr
		}
		if _, err := r.appendTaskEvent(ctx, event.AppendInput{
			RequestID:   requestid.FromContext(ctx),
			WorkspaceID: item.WorkspaceID,
			TaskID:      item.TaskID,
			TurnID:      item.ID,
			Type:        "turn.interrupted",
			PayloadJSON: string(raw),
		}); err != nil {
			r.logger.ErrorContext(ctx, "append interrupted event", "turnId", turn.ID, "error", err)
		}
	}
	return interruptedAny, nil
}

func (r *Router) cancelTaskRun(taskID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	run, ok := r.cancel[taskID]
	if ok {
		run.cancel()
		delete(r.cancel, taskID)
	}
	return ok
}

func (r *Router) listTaskEvents(w http.ResponseWriter, req *http.Request, taskID string) {
	if _, err := task.NewStore(r.db).Get(req.Context(), taskID); errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	} else if err != nil {
		r.logger.ErrorContext(req.Context(), "get task for events", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}

	items, err := event.NewStore(r.db).ListByTask(req.Context(), taskID)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list task events", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list task events failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) readTaskAttachment(w http.ResponseWriter, req *http.Request, taskID string) {
	attachmentID := strings.TrimSpace(req.URL.Query().Get("id"))
	if attachmentID == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "attachment id is required")
		return
	}
	attachment, err := task.NewStore(r.db).GetAttachment(req.Context(), taskID, attachmentID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "attachment not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get task attachment", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get attachment failed")
		return
	}
	file, err := os.Open(attachment.Path)
	if errors.Is(err, os.ErrNotExist) {
		WriteError(req.Context(), w, http.StatusNotFound, "attachment file not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "open task attachment", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "open attachment failed")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		WriteError(req.Context(), w, http.StatusInternalServerError, "read attachment failed")
		return
	}
	w.Header().Set("Content-Type", attachment.MIMEType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if attachment.Kind == "image" && strings.HasPrefix(attachment.MIMEType, "image/") {
		w.Header().Set("Content-Disposition", contentDispositionInline(attachment.Name))
	} else {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(attachment.Name))
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, req, attachment.Name, info.ModTime(), file)
}

func decodeTaskRequest(w http.ResponseWriter, req *http.Request) (taskRequest, bool) {
	defer req.Body.Close()

	var input taskRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return taskRequest{}, false
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "title is required")
		return taskRequest{}, false
	}
	return input, true
}

func decodeTurnRequest(w http.ResponseWriter, req *http.Request) (turnRequest, bool) {
	defer req.Body.Close()
	req.Body = http.MaxBytesReader(w, req.Body, maxTurnRequestBodyBytes)

	var input turnRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(req.Context(), w, http.StatusRequestEntityTooLarge, "附件请求超过 28 MiB")
			return turnRequest{}, false
		}
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return turnRequest{}, false
	}
	input.UserInput = strings.TrimSpace(input.UserInput)
	if input.UserInput == "" && len(input.Attachments) == 0 {
		WriteError(req.Context(), w, http.StatusBadRequest, "userInput or attachments is required")
		return turnRequest{}, false
	}
	return input, true
}

func splitTaskPath(rest string) (taskID string, action string, ok bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 && (parts[1] == "turns" || parts[1] == "events" || parts[1] == "attachments" || parts[1] == "tools" || parts[1] == "cancel") {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func quoteJSON(value string) string {
	b, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(b)
}

func intJSON(value int) string {
	b, err := json.Marshal(value)
	if err != nil {
		return "0"
	}
	return string(b)
}
