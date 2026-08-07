package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/ligson/water/water-be/internal/requestid"
	"github.com/ligson/water/water-be/internal/terminal"
)

var terminalWebSocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

var (
	terminalWebSocketWriteWait  = 10 * time.Second
	terminalWebSocketPongWait   = 60 * time.Second
	terminalWebSocketPingPeriod = 45 * time.Second
)

type terminalSocketMessage struct {
	Type      string                 `json:"type"`
	SessionID string                 `json:"sessionId,omitempty"`
	RequestID string                 `json:"requestId,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

func (r *Router) handleTerminalWebSocket(w http.ResponseWriter, req *http.Request, sessionID string) {
	if req.Method != http.MethodGet {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	termSession, err := terminal.NewStore(r.db).GetSession(req.Context(), sessionID)
	if errors.Is(err, terminal.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "terminal session not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get terminal session for websocket", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get terminal session failed")
		return
	}

	conn, err := terminalWebSocketUpgrader.Upgrade(w, req, nil)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "upgrade terminal websocket", "error", err)
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	var writeMu sync.Mutex
	writeMessage := func(messageType string, payload map[string]interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := conn.SetWriteDeadline(time.Now().Add(terminalWebSocketWriteWait)); err != nil {
			return err
		}
		return conn.WriteJSON(terminalSocketMessage{
			Type:      messageType,
			SessionID: termSession.ID,
			RequestID: requestid.FromContext(req.Context()),
			Timestamp: time.Now().Format(time.RFC3339Nano),
			Payload:   payload,
		})
	}

	if err := terminal.NewStore(r.db).UpdateSessionStatus(ctx, termSession.ID, terminal.SessionStatusConnecting); err != nil {
		_ = writeMessage("terminal.error", map[string]interface{}{"message": err.Error()})
		return
	}

	shell, err := terminal.StartLocalShell(ctx, termSession.Cwd, termSession.Cols, termSession.Rows)
	if err != nil {
		_ = terminal.NewStore(r.db).UpdateSessionStatus(context.Background(), termSession.ID, terminal.SessionStatusError)
		_ = writeMessage("terminal.error", map[string]interface{}{"message": err.Error()})
		return
	}

	_ = terminal.NewStore(r.db).UpdateSessionStatus(ctx, termSession.ID, terminal.SessionStatusActive)
	if err := writeMessage("terminal.ready", map[string]interface{}{
		"label": "后端服务器",
		"cwd":   termSession.Cwd,
		"cols":  termSession.Cols,
		"rows":  termSession.Rows,
	}); err != nil {
		return
	}

	done := make(chan struct{})
	errCh := make(chan error, 4)
	go func() {
		errCh <- streamTerminalOutput(ctx, shell, func(chunk string) error {
			return writeMessage("terminal.output", map[string]interface{}{"chunk": chunk})
		})
	}()
	go func() {
		errCh <- shell.Wait()
	}()
	go func() {
		defer close(done)
		defer cancel()
		readTerminalInput(conn, shell, shell, errCh)
	}()

	pingTicker := time.NewTicker(terminalWebSocketPingPeriod)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = terminal.NewStore(r.db).UpdateSessionStatus(context.Background(), termSession.ID, terminal.SessionStatusClosed)
			_ = writeMessage("terminal.closed", map[string]interface{}{})
			return
		case <-done:
			_ = terminal.NewStore(r.db).UpdateSessionStatus(context.Background(), termSession.ID, terminal.SessionStatusClosed)
			_ = writeMessage("terminal.closed", map[string]interface{}{})
			return
		case <-pingTicker.C:
			writeMu.Lock()
			deadline := time.Now().Add(terminalWebSocketWriteWait)
			if err := conn.SetWriteDeadline(deadline); err != nil {
				writeMu.Unlock()
				return
			}
			err := conn.WriteControl(websocket.PingMessage, []byte("ping"), deadline)
			writeMu.Unlock()
			if err != nil {
				r.logger.ErrorContext(ctx, "write terminal websocket ping", "error", err)
				return
			}
		case err := <-errCh:
			if err != nil && !errors.Is(err, io.EOF) {
				_ = terminal.NewStore(r.db).UpdateSessionStatus(context.Background(), termSession.ID, terminal.SessionStatusError)
				_ = writeMessage("terminal.error", map[string]interface{}{"message": err.Error()})
				return
			}
			_ = writeMessage("terminal.exit", map[string]interface{}{})
			cancel()
		}
	}
}

func streamTerminalOutput(ctx context.Context, reader io.Reader, write func(string) error) error {
	buffer := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		n, err := reader.Read(buffer)
		if n > 0 {
			if writeErr := write(string(buffer[:n])); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			return err
		}
	}
}

type terminalResizer interface {
	Resize(cols int, rows int) error
}

func readTerminalInput(conn *websocket.Conn, stdin io.Writer, shell terminalResizer, errCh chan<- error) {
	conn.SetReadLimit(64 * 1024)
	if err := conn.SetReadDeadline(time.Now().Add(terminalWebSocketPongWait)); err != nil {
		errCh <- err
		return
	}
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(terminalWebSocketPongWait))
	})
	for {
		var msg terminalSocketMessage
		if err := conn.ReadJSON(&msg); err != nil {
			errCh <- err
			return
		}
		switch msg.Type {
		case "terminal.input":
			chunk, _ := msg.Payload["data"].(string)
			if chunk != "" {
				if _, err := io.WriteString(stdin, chunk); err != nil {
					errCh <- err
					return
				}
			}
		case "terminal.resize":
			cols := intFromPayload(msg.Payload, "cols")
			rows := intFromPayload(msg.Payload, "rows")
			if cols > 0 && rows > 0 {
				if err := shell.Resize(cols, rows); err != nil {
					errCh <- err
					return
				}
			}
		case "terminal.close":
			errCh <- nil
			return
		}
	}
}

func intFromPayload(payload map[string]interface{}, key string) int {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}
