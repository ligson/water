package api

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestTaskTurnStoresUploadedImageAttachment(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatalf("create git info: %v", err)
	}
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	createdTask := createTaskForTest(t, handler, ws.ID, "Inspect image")
	body := `{"userInput":"看看这张图","attachments":[{"name":"screen.png","mimeType":"image/png","dataUrl":"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}]}`
	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope turnEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	stored, err := task.NewStore(db).GetTurn(context.Background(), envelope.Data.ID)
	if err != nil {
		t.Fatalf("get stored turn: %v", err)
	}
	if len(stored.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", stored.Attachments)
	}
	attachment := stored.Attachments[0]
	if attachment.Name != "screen.png" || attachment.MIMEType != "image/png" || attachment.Kind != "image" {
		t.Fatalf("unexpected attachment metadata: %#v", attachment)
	}
	if !strings.HasPrefix(attachment.Path, filepath.Join(root, ".water", "attachments", createdTask.ID)) {
		t.Fatalf("expected attachment inside workspace storage, got %q", attachment.Path)
	}
	if _, err := os.Stat(attachment.Path); err != nil {
		t.Fatalf("expected stored attachment file: %v", err)
	}
	exclude, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read local git exclude: %v", err)
	}
	if !strings.Contains(string(exclude), ".water/attachments/") {
		t.Fatalf("expected attachment directory in local git exclude, got %q", string(exclude))
	}
}

func TestFailedAttachmentBatchKeepsExistingFiles(t *testing.T) {
	root := t.TempDir()
	valid := []turnAttachmentRequest{{
		Name:     "first.txt",
		MIMEType: "text/plain",
		DataURL:  "data:text/plain;base64,aGVsbG8=",
	}}
	stored, _, err := storeTurnAttachments(root, "task-1", valid)
	if err != nil {
		t.Fatalf("store first attachment: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected first attachment, got %#v", stored)
	}

	_, _, err = storeTurnAttachments(root, "task-1", []turnAttachmentRequest{{
		Name:     "broken.txt",
		MIMEType: "text/plain",
		DataURL:  "not-a-data-url",
	}})
	if err == nil {
		t.Fatal("expected invalid attachment batch to fail")
	}
	if _, err := os.Stat(stored[0].Path); err != nil {
		t.Fatalf("expected prior attachment to survive failed batch: %v", err)
	}
}

func TestTaskAttachmentForcesNonImageDownload(t *testing.T) {
	root := t.TempDir()
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", root, "request_approval")
	createdTask := createTaskForTest(t, handler, ws.ID, "Inspect file")
	body := `{"userInput":"查看文件","attachments":[{"name":"page.html","mimeType":"text/html","dataUrl":"data:text/html;base64,PGgxPmhlbGxvPC9oMT4="}]}`
	rec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope turnEnvelope
	decodeTestEnvelope(t, rec, &envelope)
	stored, err := task.NewStore(db).GetTurn(context.Background(), envelope.Data.ID)
	if err != nil {
		t.Fatalf("get stored turn: %v", err)
	}

	download := performJSON(handler, http.MethodGet, "/api/tasks/"+createdTask.ID+"/attachments?id="+stored.Attachments[0].ID, "")
	if download.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", download.Code, download.Body.String())
	}
	if disposition := download.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment;") {
		t.Fatalf("expected attachment disposition, got %q", disposition)
	}
	if got := download.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff header, got %q", got)
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

func TestAgentLoopCorrectsReadFileDirectoryToListDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("evidence"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm body: %v", err)
		}
		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 1 {
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{{
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": 0,
							"id":    "call_read_directory",
							"type":  "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": `{"path":"` + root + `"}`,
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
			return
		}
		messagesRaw, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(messagesRaw), "note.txt") {
			t.Fatalf("expected corrected directory listing in model context, got %s", messagesRaw)
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"目录中包含 note.txt。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Directory correction")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"查看工作区目录内容"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if !hasEventType(events, "tool.call.corrected") {
		t.Fatalf("expected tool.call.corrected event, got %#v", events)
	}
	if hasEventType(events, "tool.failed") {
		t.Fatalf("expected corrected directory call not to fail")
	}
}

func TestAgentLoopCompactsToolHistoryAndForcesFinalResponse(t *testing.T) {
	const defaultAgentToolRoundsForTest = 24

	root := t.TempDir()
	for index := 1; index < defaultAgentToolRoundsForTest; index++ {
		content := fmt.Sprintf("evidence-%02d %s", index, strings.Repeat(fmt.Sprintf("line-%02d ", index), 220))
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("evidence-%02d.log", index)), []byte(content), 0o644); err != nil {
			t.Fatalf("write evidence fixture: %v", err)
		}
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm body: %v", err)
		}
		count := int(atomic.AddInt32(&requestCount, 1))
		w.Header().Set("Content-Type", "text/event-stream")
		if count < defaultAgentToolRoundsForTest {
			if toolsValue, ok := body["tools"].([]interface{}); !ok || len(toolsValue) == 0 {
				t.Fatalf("expected tools before final request %d, got %#v", count, body["tools"])
			}
			arguments, _ := json.Marshal(map[string]interface{}{
				"path": filepath.Join(root, fmt.Sprintf("evidence-%02d.log", count)),
			})
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{{
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": 0,
							"id":    fmt.Sprintf("call-%02d", count),
							"type":  "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": string(arguments),
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
			return
		}

		if _, ok := body["tools"]; ok {
			t.Fatalf("expected tools to be disabled on final request, got %#v", body["tools"])
		}
		messagesRaw, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(messagesRaw), "工具已关闭") {
			t.Fatalf("expected forced final instruction, got %s", messagesRaw)
		}
		if !strings.Contains(string(messagesRaw), "本轮较早的工具上下文已压缩") {
			t.Fatalf("expected compacted tool history, got %s", messagesRaw)
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"结论：已根据现有证据完成收敛。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Agent context convergence")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"持续检查后给出结论"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if got := atomic.LoadInt32(&requestCount); got != defaultAgentToolRoundsForTest {
		t.Fatalf("expected %d model requests, got %d", defaultAgentToolRoundsForTest, got)
	}
	if !hasEventType(events, "context.turn.compacted") {
		t.Fatalf("expected context.turn.compacted event")
	}
	if hasEventType(events, "turn.interrupted") {
		t.Fatalf("expected forced final response to complete instead of interrupt")
	}
}

func TestAgentLoopContinuesWhenFinalRoundStillRequestsTool(t *testing.T) {
	const firstPhaseRounds = 24
	root := t.TempDir()
	for index := 1; index < firstPhaseRounds; index++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("phase-evidence-%02d.txt", index)), []byte(fmt.Sprintf("evidence-%02d", index)), 0o644); err != nil {
			t.Fatalf("write phase fixture: %v", err)
		}
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode llm body: %v", err)
		}
		count := int(atomic.AddInt32(&requestCount, 1))
		w.Header().Set("Content-Type", "text/event-stream")
		if count <= firstPhaseRounds {
			arguments, _ := json.Marshal(map[string]interface{}{
				"path": filepath.Join(root, fmt.Sprintf("phase-evidence-%02d.txt", min(count, firstPhaseRounds-1))),
			})
			chunk := map[string]interface{}{
				"choices": []map[string]interface{}{{
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": 0,
							"id":    fmt.Sprintf("call-phase-%02d", count),
							"type":  "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": string(arguments),
							},
						}},
					},
					"finish_reason": "tool_calls",
				}},
			}
			raw, _ := json.Marshal(chunk)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
			return
		}

		messagesRaw, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(messagesRaw), "自动续跑的第 2 个执行阶段") {
			t.Fatalf("expected clean phase restart instruction, got %s", messagesRaw)
		}
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"已基于持久化计划重新规划并给出结论。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Final tool call recovery")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"持续检查后给出分析结论"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if got := atomic.LoadInt32(&requestCount); got != firstPhaseRounds+1 {
		t.Fatalf("expected final tool request plus one continued phase request, got %d", got)
	}
	if !hasEventType(events, "agent.final_tool_calls.deferred") || !hasEventType(events, "agent.deferred_tool_calls.executing") || !hasEventType(events, "agent.execution.phase.continued") {
		t.Fatalf("expected final tool recovery events, got %#v", events)
	}
	if hasEventType(events, "turn.failed") {
		t.Fatalf("expected automatic phase continuation instead of failure")
	}
}

func TestAgentLoopPausesAfterAllExecutionPhasesAndAllowsNextTurn(t *testing.T) {
	const totalRoundBudget = 24 + 8 + 8
	root := t.TempDir()
	for index := 1; index <= totalRoundBudget; index++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("bounded-evidence-%02d.txt", index)), []byte(fmt.Sprintf("evidence-%02d", index)), 0o644); err != nil {
			t.Fatalf("write bounded execution fixture: %v", err)
		}
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := int(atomic.AddInt32(&requestCount, 1))
		arguments, _ := json.Marshal(map[string]interface{}{
			"path": filepath.Join(root, fmt.Sprintf("bounded-evidence-%02d.txt", min(count, totalRoundBudget))),
		})
		chunk := map[string]interface{}{
			"choices": []map[string]interface{}{{
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index": 0,
						"id":    fmt.Sprintf("call-bounded-%02d", count),
						"type":  "function",
						"function": map[string]interface{}{
							"name":      "read_file",
							"arguments": string(arguments),
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		}
		raw, _ := json.Marshal(chunk)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "Bounded execution pause")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"持续检查全部证据，满足条件后再结束"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}
	var createdTurn turnEnvelope
	decodeTestEnvelope(t, turnRec, &createdTurn)
	events := waitTaskEvents(t, handler, createdTask.ID, "turn.paused")
	if got := int(atomic.LoadInt32(&requestCount)); got != totalRoundBudget+1 {
		t.Fatalf("expected %d bounded requests plus one final judgment, got %d", totalRoundBudget, got)
	}
	if hasEventType(events, "turn.failed") || hasEventType(events, "turn.completed") {
		t.Fatalf("expected incomplete task to pause without failure or completion")
	}

	pausedTurn, err := task.NewStore(db).GetTurn(context.Background(), createdTurn.Data.ID)
	if err != nil {
		t.Fatalf("get paused turn: %v", err)
	}
	if pausedTurn.Status != task.TurnStatusPaused || pausedTurn.CompletedAt == nil {
		t.Fatalf("expected terminal paused turn, got %#v", pausedTurn)
	}
	nextTurn, err := task.NewStore(db).CreateTurn(context.Background(), task.CreateTurnInput{
		TaskID:    createdTask.ID,
		UserInput: "继续当前任务",
	})
	if err != nil {
		t.Fatalf("create continuation turn after pause: %v", err)
	}
	if nextTurn.Sequence != 2 {
		t.Fatalf("expected continuation sequence 2, got %d", nextTurn.Sequence)
	}
}

func TestAgentLoopRepeatedSameOutputForcesFinalResponse(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("loop"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}
		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 4 {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode final request: %v", err)
			}
			if _, ok := body["tools"]; ok {
				t.Fatalf("expected tools disabled after repeated output")
			}
			messagesRaw, _ := json.Marshal(body["messages"])
			if !strings.Contains(string(messagesRaw), "工具循环保护已触发") {
				t.Fatalf("expected loop guard final instruction, got %s", messagesRaw)
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"目录结果没有变化，停止重复检查。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
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
		if atomic.LoadInt32(&requestCount) >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&requestCount); got != 4 {
		t.Fatalf("expected 3 tool rounds and one final request, got %d", got)
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.completed")
	if hasEventType(events, "turn.failed") {
		t.Fatalf("expected repeated tool output to finalize instead of fail, got %#v", events)
	}
	if hasEventType(events, "turn.interrupted") {
		t.Fatalf("expected repeated tool output to produce a final response instead of interrupt")
	}
	if !hasEventType(events, "agent.loop.guard.triggered") {
		t.Fatalf("expected loop guard observability event")
	}
	var completedPayload map[string]interface{}
	for _, item := range events {
		if item.Type == "turn.completed" {
			completedPayload = item.Payload()
			break
		}
	}
	if completedPayload["forcedFinal"] != true || completedPayload["forcedFinalReason"] != "semantic_no_progress" {
		t.Fatalf("expected forced final completion payload, got %#v", completedPayload)
	}
}

func TestAgentLoopGuardPausesWhenFinalEvidenceIsInsufficient(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("loop"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var requestCount int32
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected llm path %s", r.URL.Path)
		}
		count := atomic.AddInt32(&requestCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 4 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"目前还没有完成实现，也没有通过验收。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
			return
		}
		chunk := map[string]interface{}{
			"choices": []map[string]interface{}{{
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index": 0,
						"id":    "call_list_dir",
						"type":  "function",
						"function": map[string]interface{}{
							"name":      "list_dir",
							"arguments": `{"path":"` + root + `"}`,
						},
					}},
				},
				"finish_reason": "tool_calls",
			}},
		}
		raw, _ := json.Marshal(chunk)
		_, _ = w.Write([]byte("data: " + string(raw) + "\n\ndata: [DONE]\n\n"))
	}))
	defer llmServer.Close()

	db := openTestDB(t)
	defer db.Close()
	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	provider := createProviderForTestWithBaseURL(t, handler, "Local", llmServer.URL+"/v1", true)
	ws := createWorkspaceForTestWithProvider(t, handler, "Water", root, "request_approval", provider.ID)
	createdTask := createTaskForTest(t, handler, ws.ID, "实现登录功能")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+createdTask.ID+"/turns", `{"userInput":"实现登录功能并运行测试"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.paused")
	if got := atomic.LoadInt32(&requestCount); got != 4 {
		t.Fatalf("expected 3 tool requests and one guarded final request, got %d", got)
	}
	if hasEventType(events, "agent.execution.phase.continued") {
		t.Fatalf("expected loop guard to stop the turn without starting another execution phase")
	}
	if hasEventType(events, "turn.completed") || hasEventType(events, "turn.failed") {
		t.Fatalf("expected insufficient evidence to pause, got %#v", events)
	}
}

func TestAgentLoopRepeatedPathFailureBlocksForRequiredInput(t *testing.T) {
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
										"arguments": `{"command":"ls -la /outside/workspace/demo-be"}`,
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
		case 3:
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode final request: %v", err)
			}
			if _, ok := body["tools"]; ok {
				t.Fatalf("expected tools disabled after repeated failure")
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"目标路径未授权，需要用户提供正确路径或授权。\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
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

	events := waitTaskEvents(t, handler, createdTask.ID, "turn.blocked")
	if got := atomic.LoadInt32(&requestCount); got != 3 {
		t.Fatalf("expected 2 tool requests and one final request, got %d", got)
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
	if hasEventType(events, "turn.completed") || hasEventType(events, "turn.interrupted") {
		t.Fatalf("expected repeated path failure to block instead of complete or interrupt")
	}
	if !hasEventType(events, "agent.loop.guard.triggered") {
		t.Fatalf("expected loop guard observability event")
	}
	var blockedPayload map[string]interface{}
	for _, item := range events {
		if item.Type == "turn.blocked" {
			blockedPayload = item.Payload()
			break
		}
	}
	missingInputs, ok := blockedPayload["missingInputs"].([]interface{})
	if !ok || len(missingInputs) == 0 {
		t.Fatalf("expected blocked event to list missing inputs, got %#v", blockedPayload)
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
