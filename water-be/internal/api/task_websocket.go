package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ligson/water/water-be/internal/event"
	"github.com/ligson/water/water-be/internal/realtime"
	"github.com/ligson/water/water-be/internal/task"
)

var taskWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	taskWebSocketWriteWait  = 10 * time.Second
	taskWebSocketPongWait   = 60 * time.Second
	taskWebSocketPingPeriod = 45 * time.Second
)

func (r *Router) handleTaskWebSocket(w http.ResponseWriter, req *http.Request, taskID string) {
	if req.Method != http.MethodGet {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	afterSequence, ok := parseWebSocketAfterSequence(w, req)
	if !ok {
		return
	}

	_, err := task.NewStore(r.db).Get(req.Context(), taskID)
	if errors.Is(err, task.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get task for websocket", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get task failed")
		return
	}

	conn, err := taskWebSocketUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "upgrade task websocket", "error", err)
		return
	}
	defer conn.Close()
	conn.SetReadLimit(1024)
	if err := conn.SetReadDeadline(time.Now().Add(taskWebSocketPongWait)); err != nil {
		r.logger.ErrorContext(req.Context(), "set websocket read deadline", "error", err)
		return
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(taskWebSocketPongWait))
	})

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	liveEvents, unsubscribe := r.hub.Subscribe(ctx, taskID)
	defer unsubscribe()

	history, err := event.NewStore(r.db).ListByTaskAfterSequence(ctx, taskID, afterSequence)
	if err != nil {
		r.logger.ErrorContext(ctx, "list task events for websocket", "error", err)
		return
	}

	lastSequence := afterSequence
	for _, item := range history {
		if item.Sequence > lastSequence {
			lastSequence = item.Sequence
		}
		if err := writeTaskWebSocketMessage(conn, realtime.FromEvent(item)); err != nil {
			r.logger.ErrorContext(ctx, "write task websocket history", "error", err)
			return
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		for {
			if _, _, err := conn.NextReader(); err != nil {
				return
			}
		}
	}()

	pingTicker := time.NewTicker(taskWebSocketPingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-pingTicker.C:
			if err := writeTaskWebSocketPing(conn); err != nil {
				r.logger.ErrorContext(ctx, "write task websocket ping", "error", err)
				return
			}
		case msg, ok := <-liveEvents:
			if !ok {
				return
			}
			if msg.Sequence <= lastSequence {
				continue
			}
			if err := writeTaskWebSocketMessage(conn, msg); err != nil {
				r.logger.ErrorContext(ctx, "write task websocket live event", "error", err)
				return
			}
			lastSequence = msg.Sequence
		}
	}
}

func parseWebSocketAfterSequence(w http.ResponseWriter, req *http.Request) (int, bool) {
	raw := req.URL.Query().Get("afterSequence")
	if raw == "" {
		raw = req.URL.Query().Get("sinceSequence")
	}
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		WriteError(req.Context(), w, http.StatusBadRequest, "afterSequence must be a non-negative integer")
		return 0, false
	}
	return value, true
}

func writeTaskWebSocketMessage(conn *websocket.Conn, msg realtime.Message) error {
	if err := conn.SetWriteDeadline(time.Now().Add(taskWebSocketWriteWait)); err != nil {
		return err
	}
	return conn.WriteJSON(msg)
}

func writeTaskWebSocketPing(conn *websocket.Conn) error {
	deadline := time.Now().Add(taskWebSocketWriteWait)
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline)
}
