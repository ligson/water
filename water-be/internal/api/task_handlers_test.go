package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ligson/water/water-be/internal/config"
	"github.com/ligson/water/water-be/internal/task"
)

func TestTaskTurnEventFlow(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	createdTask := createTaskForTest(t, handler, ws.ID, "Build task chain")
	if createdTask.WorkspaceID != ws.ID {
		t.Fatalf("expected workspace id %q, got %q", ws.ID, createdTask.WorkspaceID)
	}

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"hello"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
	var turnEnvelope turnEnvelope
	decodeTestEnvelope(t, turnRec, &turnEnvelope)
	if turnEnvelope.Data.Sequence != 1 {
		t.Fatalf("expected first turn sequence 1, got %d", turnEnvelope.Data.Sequence)
	}

	eventsRec := performJSON(handler, http.MethodGet, "/api/tasks/"+createdTask.ID+"/events", "")
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", eventsRec.Code)
	}
	var events eventListEnvelope
	decodeTestEnvelope(t, eventsRec, &events)
	if len(events.Data.Items) != 2 {
		t.Fatalf("expected task.started and turn.started, got %d", len(events.Data.Items))
	}
	if events.Data.Items[0].Type != "task.started" {
		t.Fatalf("expected first event task.started, got %q", events.Data.Items[0].Type)
	}
	if events.Data.Items[1].Type != "turn.started" {
		t.Fatalf("expected second event turn.started, got %q", events.Data.Items[1].Type)
	}
	if events.Data.Items[0].Sequence != 1 || events.Data.Items[1].Sequence != 2 {
		t.Fatalf("expected event sequences 1,2 got %d,%d", events.Data.Items[0].Sequence, events.Data.Items[1].Sequence)
	}
}

func TestCreateTurnRunsAgentLoop(t *testing.T) {
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"你\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"好\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", "/tmp/water", "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Agent loop")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"hello"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if !hasEventType(events, "agent.message.delta") {
		t.Fatalf("expected agent.message.delta event, got %#v", events)
	}
	if !hasEventType(events, "agent.message.completed") {
		t.Fatalf("expected agent.message.completed event, got %#v", events)
	}
}

func TestAgentLoopExecutesReadOnlyToolCall(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("tool-result"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm body: %v", err)
		}
		if toolsValue, ok := body["tools"].([]interface{}); !ok || len(toolsValue) == 0 {
			t.Fatalf("expected tool definitions in request body, got %#v", body["tools"])
		}

		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch count {
		case 1:
			firstChunk := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": 0,
									"id":    "call_1",
									"type":  "function",
									"function": map[string]interface{}{
										"name":      "run_command",
										"arguments": `{"command":"df -h /`,
									},
								},
							},
						},
					},
				},
			}
			secondChunk := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": 0,
									"id":    "call_1",
									"type":  "function",
									"function": map[string]interface{}{
										"arguments": `"}`,
									},
								},
							},
						},
						"finish_reason": "tool_calls",
					},
				},
			}
			firstRaw, _ := json.Marshal(firstChunk)
			secondRaw, _ := json.Marshal(secondChunk)
			_, _ = w.Write([]byte("data: " + string(firstRaw) + "\n\n"))
			_, _ = w.Write([]byte("data: " + string(secondRaw) + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case 2:
			messagesRaw, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(messagesRaw), "\"role\":\"tool\"") {
				t.Fatalf("expected tool message in second request, got %s", string(messagesRaw))
			}
			if !strings.Contains(string(messagesRaw), "df -h /") {
				t.Fatalf("expected run_command result in second request, got %s", string(messagesRaw))
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"已查询硬盘容量\"},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected llm request count %d", count)
		}
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Agent tool loop")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"帮我查询电脑硬盘大小"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if !hasEventType(events, "tool.completed") {
		t.Fatalf("expected tool.completed event, got %#v", events)
	}
	summary := summaryPayloadFromEvents(t, events)
	commands, ok := summary["commands"].([]interface{})
	if !ok || len(commands) != 1 {
		t.Fatalf("expected one command in turn.summary, got %#v", summary)
	}
}

func TestAgentLoopRepeatedSameOutputInterruptsTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("loop"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}
		atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{
							{
								"index": 0,
								"id":    "call_list_dir",
								"type":  "function",
								"function": map[string]interface{}{
									"name":      "list_dir",
									"arguments": `{"path":"` + root + `"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Tool loop limit")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"反复查看目录"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&requestCount) >= 6 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requestCount); got != 6 {
		t.Fatalf("expected 6 llm requests before reading events, got %d", got)
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.interrupted")
	if hasEventType(events, "turn.failed") {
		t.Fatalf("expected repeated tool output to interrupt instead of fail, got %#v", events)
	}
	var interruptedPayload map[string]interface{}
	for _, item := range events {
		if item.Type == "turn.interrupted" {
			interruptedPayload = item.Payload()
			break
		}
	}
	message, ok := interruptedPayload["message"].(string)
	if !ok || !strings.Contains(message, "没有新的信息") {
		t.Fatalf("expected Chinese interruption message, got %#v", interruptedPayload)
	}
	if interruptedPayload["reason"] != "tool_repeated_output" || interruptedPayload["canContinue"] != true {
		t.Fatalf("expected continuable repeated-output payload, got %#v", interruptedPayload)
	}
	prompt, ok := interruptedPayload["continuationPrompt"].(string)
	if !ok || !strings.Contains(prompt, "换一个新的检查点") {
		t.Fatalf("expected continuation prompt, got %#v", interruptedPayload)
	}
}

func TestAgentLoopRepeatedPathFailureInterruptsTurn(t *testing.T) {
	root := t.TempDir()

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}

		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch count {
		case 1, 2:
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": 0,
									"id":    "call_bad_path",
									"type":  "function",
									"function": map[string]interface{}{
										"name":      "run_command",
										"arguments": `{"command":"ls -la /Users/ligson/workspace/dev/sdk/demo-be"}`,
									},
								},
							},
						},
						"finish_reason": "tool_calls",
					},
				},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected llm request count %d", count)
		}
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Bad path guard")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"帮我查看外部目录"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.interrupted")
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("expected 2 llm requests before interrupt, got %d", got)
	}
	if !hasEventType(events, "tool.failed") {
		t.Fatalf("expected tool.failed event, got %#v", events)
	}
	var failedPayload map[string]interface{}
	for _, item := range events {
		if item.Type == "tool.failed" {
			failedPayload = item.Payload()
			break
		}
	}
	if failedPayload == nil {
		t.Fatalf("expected tool.failed payload")
	}
	if hint, ok := failedPayload["hint"].(string); !ok || !strings.Contains(hint, "工作区根目录") {
		t.Fatalf("expected path hint in failed payload, got %#v", failedPayload)
	}
	var interruptedPayload map[string]interface{}
	for _, item := range events {
		if item.Type == "turn.interrupted" {
			interruptedPayload = item.Payload()
			break
		}
	}
	if interruptedPayload["reason"] != "tool_repeated_failure" {
		t.Fatalf("expected repeated failure reason, got %#v", interruptedPayload)
	}
}

func TestApprovalResolutionResumesAgentToolLoop(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "reports", "system-report.md")

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm body: %v", err)
		}

		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch count {
		case 1:
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"delta": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"index": 0,
									"id":    "call_write_report",
									"type":  "function",
									"function": map[string]interface{}{
										"name":      "write_file",
										"arguments": `{"path":"` + reportPath + `","content":"# 系统报告\n\nok"}`,
									},
								},
							},
						},
						"finish_reason": "tool_calls",
					},
				},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		case 2:
			messagesRaw, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(messagesRaw), "\"role\":\"tool\"") {
				t.Fatalf("expected approved tool result in resumed request, got %s", string(messagesRaw))
			}
			if !strings.Contains(string(messagesRaw), "call_write_report") {
				t.Fatalf("expected original tool call id in resumed request, got %s", string(messagesRaw))
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"报告已保存\"},\"finish_reason\":\"stop\"}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected llm request count %d", count)
		}
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "系统分析报告")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"帮我生成系统分析报告并保存"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "approval.requested")
	approvalID := approvalIDFromEvents(t, events)
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report should not be written before approval")
	}

	resolveRec := performJSON(handler, http.MethodPost, "/api/approvals/"+approvalID+"/resolve", `{"status":"approved","message":"同意"}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("expected approve status 200, got %d: %s", resolveRec.Code, resolveRec.Body.String())
	}

	events = waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if !hasEventType(events, "approval.continuation.started") {
		t.Fatalf("expected approval.continuation.started event, got %#v", events)
	}
	if !hasEventType(events, "tool.completed") {
		t.Fatalf("expected tool.completed event, got %#v", events)
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read approved report: %v", err)
	}
	if string(content) != "# 系统报告\n\nok" {
		t.Fatalf("unexpected report content %q", string(content))
	}
	summary := summaryPayloadFromEvents(t, events)
	files, ok := summary["changedFiles"].([]interface{})
	if !ok || len(files) != 1 {
		t.Fatalf("expected one changed file in turn.summary, got %#v", summary)
	}
	fileSummary, ok := files[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected changed file object, got %#v", files[0])
	}
	if fileSummary["displayPath"] != "reports/system-report.md" {
		t.Fatalf("unexpected changed file display path %#v", fileSummary)
	}
	if fileSummary["additions"] != float64(3) || fileSummary["deletions"] != float64(0) {
		t.Fatalf("unexpected changed file diff stats %#v", fileSummary)
	}
	if got := atomic.LoadInt32(&requestCount); got != 2 {
		t.Fatalf("expected 2 llm requests, got %d", got)
	}
}

func TestApprovalRejectionInterruptsWaitingTurn(t *testing.T) {
	root := t.TempDir()
	reportPath := filepath.Join(root, "reports", "rejected-report.md")

	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{
							{
								"index": 0,
								"id":    "call_write_rejected",
								"type":  "function",
								"function": map[string]interface{}{
									"name":      "write_file",
									"arguments": `{"path":"` + reportPath + `","content":"no"}`,
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "拒绝报告")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"帮我生成报告并保存"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "approval.requested")
	approvalID := approvalIDFromEvents(t, events)

	resolveRec := performJSON(handler, http.MethodPost, "/api/approvals/"+approvalID+"/resolve", `{"status":"rejected","message":"不要写"}`)
	if resolveRec.Code != http.StatusOK {
		t.Fatalf("expected reject status 200, got %d: %s", resolveRec.Code, resolveRec.Body.String())
	}

	events = waitTaskEvents(t, handler, createdTask.ID, "turn.interrupted")
	if hasEventType(events, "tool.completed") {
		t.Fatalf("expected rejected approval not to execute tool, got %#v", events)
	}
	if _, err := os.Stat(reportPath); !os.IsNotExist(err) {
		t.Fatalf("report should not be written after rejection")
	}
}

func TestTaskListAndEventsEmptyArrays(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/tasks", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", listRec.Code)
	}
	var listed taskListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if listed.Data.Items == nil {
		t.Fatalf("expected empty task array, got nil")
	}

	task := createTaskForTest(t, handler, ws.ID, "No turns yet")
	eventsRec := performJSON(handler, http.MethodGet, "/api/tasks/"+task.ID+"/events", "")
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", eventsRec.Code)
	}
	var events eventListEnvelope
	decodeTestEnvelope(t, eventsRec, &events)
	if len(events.Data.Items) != 1 {
		t.Fatalf("expected task.started event only, got %d", len(events.Data.Items))
	}
}

func TestTaskValidation(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")

	rec := performJSON(handler, http.MethodPost, "/api/workspaces/"+ws.ID+"/tasks", `{"title":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", rec.Code)
	}
}

func TestUpdateTaskTitle(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	createdTask := createTaskForTest(t, handler, ws.ID, "Old title")

	updateRec := performJSON(handler, http.MethodPut, "/api/tasks/"+createdTask.ID, `{"title":"New title"}`)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected update task status 200, got %d: %s", updateRec.Code, updateRec.Body.String())
	}
	var updated taskEnvelope
	decodeTestEnvelope(t, updateRec, &updated)
	if updated.Data.Title != "New title" {
		t.Fatalf("expected updated title, got %q", updated.Data.Title)
	}

	getRec := performJSON(handler, http.MethodGet, "/api/tasks/"+createdTask.ID, "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected get task status 200, got %d", getRec.Code)
	}
	var fetched taskEnvelope
	decodeTestEnvelope(t, getRec, &fetched)
	if fetched.Data.Title != "New title" {
		t.Fatalf("expected persisted title, got %q", fetched.Data.Title)
	}
}

func TestDeleteTaskRemovesTaskHistory(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	deletedTask := createTaskForTest(t, handler, ws.ID, "Delete me")
	keptTask := createTaskForTest(t, handler, ws.ID, "Keep me")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+deletedTask.ID+"/turns", `{"userInput":"hello"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected turn status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	deleteRec := performJSON(handler, http.MethodDelete, "/api/tasks/"+deletedTask.ID, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected delete task status 200, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}

	getRec := performJSON(handler, http.MethodGet, "/api/tasks/"+deletedTask.ID, "")
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("expected deleted task status 404, got %d", getRec.Code)
	}

	eventsRec := performJSON(handler, http.MethodGet, "/api/tasks/"+deletedTask.ID+"/events", "")
	if eventsRec.Code != http.StatusNotFound {
		t.Fatalf("expected deleted task events status 404, got %d", eventsRec.Code)
	}

	listRec := performJSON(handler, http.MethodGet, "/api/workspaces/"+ws.ID+"/tasks", "")
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list tasks status 200, got %d", listRec.Code)
	}
	var listed taskListEnvelope
	decodeTestEnvelope(t, listRec, &listed)
	if len(listed.Data.Items) != 1 || listed.Data.Items[0].ID != keptTask.ID {
		t.Fatalf("expected only kept task after delete, got %#v", listed.Data.Items)
	}
}

func TestWorkspaceDeleteCascadesTasks(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Cascade")

	deleteRec := performJSON(handler, http.MethodDelete, "/api/workspaces/"+ws.ID, "")
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("expected delete workspace status 200, got %d", deleteRec.Code)
	}

	taskRec := performJSON(handler, http.MethodGet, "/api/tasks/"+task.ID, "")
	if taskRec.Code != http.StatusNotFound {
		t.Fatalf("expected task to be deleted by cascade, got %d", taskRec.Code)
	}
}

func TestCancelTaskWhenNotRunning(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Cancel")

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/cancel", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateTurnRejectsWhenTaskHasActiveTurn(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	taskItem := createTaskForTest(t, handler, ws.ID, "Single active turn")
	turn := createTurnForTest(t, handler, taskItem.ID, "first")

	if _, err := task.NewStore(db).UpdateTurnStatus(context.Background(), turn.ID, task.TurnStatusRunning); err != nil {
		t.Fatalf("set running turn: %v", err)
	}

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+taskItem.ID+"/turns", `{"userInput":"second"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCancelRunningTaskMarksTurnInterrupted(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		once.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		llmServer.Close()
	}()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", "/tmp/water", "request_approval", provider.ID)
	task := createTaskForTest(t, handler, ws.ID, "Cancel running")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/turns", `{"userInput":"帮我做一个长任务"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected turn status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for llm request")
	}

	cancelRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/cancel", "")
	if cancelRec.Code != http.StatusOK {
		t.Fatalf("expected cancel status 200, got %d: %s", cancelRec.Code, cancelRec.Body.String())
	}

	events := waitTaskEvents(t, handler, task.ID, "turn.interrupted")
	if hasEventType(events, "turn.failed") {
		t.Fatalf("expected interrupted turn not failed, got %#v", events)
	}
}

func TestCancelStaleRunningTaskMarksTurnInterrupted(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	taskItem := createTaskForTest(t, handler, ws.ID, "Stale running")
	turn := createTurnForTest(t, handler, taskItem.ID, "stale running turn")

	if _, err := task.NewStore(db).UpdateTurnStatus(context.Background(), turn.ID, task.TurnStatusRunning); err != nil {
		t.Fatalf("set running turn: %v", err)
	}

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+taskItem.ID+"/cancel", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cancel status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	events := waitTaskEvents(t, handler, taskItem.ID, "turn.interrupted")
	if !hasEventType(events, "turn.interrupted") {
		t.Fatalf("expected interrupted event, got %#v", events)
	}

	turnRec := performJSON(handler, http.MethodGet, "/api/tasks/"+taskItem.ID+"/events", "")
	if turnRec.Code != http.StatusOK {
		t.Fatalf("expected events status 200, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
}

func TestRouterRecoversStaleRunningTurnsOnStartup(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	baseRouter := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, baseRouter, "Water", "/tmp/water", "request_approval")
	createdTask := createTaskForTest(t, baseRouter, ws.ID, "Recover me")
	createdTurn := createTurnForTest(t, baseRouter, createdTask.ID, "stale turn")

	if _, err := task.NewStore(db).UpdateTurnStatus(context.Background(), createdTurn.ID, task.TurnStatusRunning); err != nil {
		t.Fatalf("set running turn: %v", err)
	}

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	eventsRec := performJSON(handler, http.MethodGet, "/api/tasks/"+createdTask.ID+"/events", "")
	if eventsRec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", eventsRec.Code, eventsRec.Body.String())
	}
	var events eventListEnvelope
	decodeTestEnvelope(t, eventsRec, &events)
	if !hasEventType(events.Data.Items, "turn.interrupted") {
		t.Fatalf("expected stale running turn to be interrupted, got %#v", events.Data.Items)
	}
}

func createTaskForTest(t *testing.T, handler http.Handler, workspaceID string, title string) taskResponse {
	t.Helper()

	rec := performJSON(handler, http.MethodPost, "/api/workspaces/"+workspaceID+"/tasks", `{"title":"`+title+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: status %d body %s", rec.Code, rec.Body.String())
	}
	var envelope taskEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	return envelope.Data
}

func createTurnForTest(t *testing.T, handler http.Handler, taskID string, userInput string) turnResponse {
	t.Helper()

	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+taskID+"/turns", `{"userInput":"`+userInput+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create turn: status %d body %s", rec.Code, rec.Body.String())
	}
	var envelope turnEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	return envelope.Data
}

func waitTaskEvents(t *testing.T, handler http.Handler, taskID string, targetType string) []eventResponse {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		eventsRec := performJSON(handler, http.MethodGet, "/api/tasks/"+taskID+"/events", "")
		if eventsRec.Code != http.StatusOK {
			t.Fatalf("list events status %d: %s", eventsRec.Code, eventsRec.Body.String())
		}
		var events eventListEnvelope
		decodeTestEnvelope(t, eventsRec, &events)
		if hasEventType(events.Data.Items, targetType) {
			return events.Data.Items
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event type %q", targetType)
	return nil
}

func hasEventType(events []eventResponse, eventType string) bool {
	for _, item := range events {
		if item.Type == eventType {
			return true
		}
	}
	return false
}

func approvalIDFromEvents(t *testing.T, events []eventResponse) string {
	t.Helper()
	for _, item := range events {
		if item.Type != "approval.requested" {
			continue
		}
		payload := item.Payload()
		approvalValue, ok := payload["approval"].(map[string]interface{})
		if !ok {
			t.Fatalf("approval.requested payload missing approval: %#v", payload)
		}
		id, ok := approvalValue["id"].(string)
		if !ok || id == "" {
			t.Fatalf("approval payload missing id: %#v", approvalValue)
		}
		return id
	}
	t.Fatalf("approval.requested event not found")
	return ""
}

func summaryPayloadFromEvents(t *testing.T, events []eventResponse) map[string]interface{} {
	t.Helper()
	for _, item := range events {
		if item.Type == "turn.summary" {
			return item.Payload()
		}
	}
	t.Fatalf("turn.summary event not found")
	return nil
}

type taskResponse struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Title       string `json:"title"`
	Status      string `json:"status"`
}

type taskEnvelope struct {
	Success bool         `json:"success"`
	Data    taskResponse `json:"data"`
}

type taskListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []taskResponse `json:"items"`
	} `json:"data"`
}

type turnResponse struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	Sequence  int    `json:"sequence"`
	Status    string `json:"status"`
	UserInput string `json:"userInput"`
}

type turnEnvelope struct {
	Success bool         `json:"success"`
	Data    turnResponse `json:"data"`
}

type eventResponse struct {
	EventID     string `json:"eventId"`
	RequestID   string `json:"requestId"`
	WorkspaceID string `json:"workspaceId"`
	TaskID      string `json:"taskId"`
	TurnID      string `json:"turnId"`
	Sequence    int    `json:"sequence"`
	Type        string `json:"type"`
	PayloadJSON string `json:"payloadJson"`
}

func (e eventResponse) Payload() map[string]interface{} {
	var payload map[string]interface{}
	_ = json.Unmarshal([]byte(e.PayloadJSON), &payload)
	return payload
}

type eventListEnvelope struct {
	Success bool `json:"success"`
	Data    struct {
		Items []eventResponse `json:"items"`
	} `json:"data"`
}
