package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ligson/water/water-be/internal/config"
)

func TestTaskWebSocketReplayAndLiveBroadcast(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Realtime task")

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := dialTaskWebSocket(t, server.URL, task.ID)
	defer conn.Close()

	replayed := readTaskWebSocketMessage(t, conn)
	if replayed.Type != "task.started" {
		t.Fatalf("expected replayed task.started event, got %q", replayed.Type)
	}
	if replayed.TaskID != task.ID {
		t.Fatalf("expected replayed event for task %q, got %q", task.ID, replayed.TaskID)
	}
	if replayed.Sequence != 1 {
		t.Fatalf("expected replayed sequence 1, got %d", replayed.Sequence)
	}

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/turns", `{"userInput":"hello"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected turn create status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	live := readTaskWebSocketMessage(t, conn)
	if live.Type != "turn.started" {
		t.Fatalf("expected live turn.started event, got %q", live.Type)
	}
	if live.Sequence != 2 {
		t.Fatalf("expected live sequence 2, got %d", live.Sequence)
	}
}

func TestTaskWebSocketBroadcastsToMultipleSubscribers(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Multi client task")

	server := httptest.NewServer(handler)
	defer server.Close()

	connA := dialTaskWebSocket(t, server.URL, task.ID)
	defer connA.Close()
	connB := dialTaskWebSocket(t, server.URL, task.ID)
	defer connB.Close()

	_ = readTaskWebSocketMessage(t, connA)
	_ = readTaskWebSocketMessage(t, connB)

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/turns", `{"userInput":"broadcast"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected turn create status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	liveA := readTaskWebSocketMessage(t, connA)
	liveB := readTaskWebSocketMessage(t, connB)
	if liveA.Type != "turn.started" || liveB.Type != "turn.started" {
		t.Fatalf("expected both subscribers to receive turn.started, got %q and %q", liveA.Type, liveB.Type)
	}
}

func TestTaskWebSocketReplayAfterSequence(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Resume task")

	turnRec := performJSON(handler, http.MethodPost, "/api/tasks/"+task.ID+"/turns", `{"userInput":"resume"}`)
	if turnRec.Code != http.StatusCreated {
		t.Fatalf("expected turn create status 201, got %d: %s", turnRec.Code, turnRec.Body.String())
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := dialTaskWebSocketWithQuery(t, server.URL, task.ID, "afterSequence=1")
	defer conn.Close()

	replayed := readTaskWebSocketMessage(t, conn)
	if replayed.Type != "turn.started" {
		t.Fatalf("expected replayed turn.started after sequence 1, got %q", replayed.Type)
	}
	if replayed.Sequence != 2 {
		t.Fatalf("expected replayed sequence 2, got %d", replayed.Sequence)
	}
}

func TestTaskWebSocketSendsPingAndAcceptsPong(t *testing.T) {
	oldWriteWait := taskWebSocketWriteWait
	oldPongWait := taskWebSocketPongWait
	oldPingPeriod := taskWebSocketPingPeriod
	taskWebSocketWriteWait = 100 * time.Millisecond
	taskWebSocketPongWait = 500 * time.Millisecond
	taskWebSocketPingPeriod = 20 * time.Millisecond
	defer func() {
		taskWebSocketWriteWait = oldWriteWait
		taskWebSocketPongWait = oldPongWait
		taskWebSocketPingPeriod = oldPingPeriod
	}()

	db := openTestDB(t)
	defer db.Close()

	handler := NewRouter(db, config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ws := createWorkspaceForTest(t, handler, "Water", "/tmp/water", "request_approval")
	task := createTaskForTest(t, handler, ws.ID, "Heartbeat task")

	server := httptest.NewServer(handler)
	defer server.Close()

	conn := dialTaskWebSocket(t, server.URL, task.ID)
	defer conn.Close()
	_ = readTaskWebSocketMessage(t, conn)

	pingSeen := make(chan struct{})
	conn.SetPingHandler(func(appData string) error {
		select {
		case <-pingSeen:
		default:
			close(pingSeen)
		}
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})

	readErr := make(chan error, 1)
	go func() {
		for {
			if _, _, err := conn.NextReader(); err != nil {
				readErr <- err
				return
			}
		}
	}()

	select {
	case <-pingSeen:
	case err := <-readErr:
		t.Fatalf("websocket closed before ping: %v", err)
	case <-time.After(time.Second):
		t.Fatal("expected websocket ping")
	}
}

func dialTaskWebSocket(t *testing.T, baseURL string, taskID string) *websocket.Conn {
	t.Helper()

	return dialTaskWebSocketWithQuery(t, baseURL, taskID, "")
}

func dialTaskWebSocketWithQuery(t *testing.T, baseURL string, taskID string, query string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/tasks/" + taskID
	if query != "" {
		wsURL += "?" + query
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readTaskWebSocketMessage(t *testing.T, conn *websocket.Conn) realtimeMessage {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket message: %v", err)
	}

	var msg realtimeMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatalf("decode websocket message: %v", err)
	}
	return msg
}

type realtimeMessage struct {
	EventID   string          `json:"eventId"`
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	TaskID    string          `json:"taskId"`
	Sequence  int             `json:"sequence"`
	Payload   json.RawMessage `json:"payload"`
}
