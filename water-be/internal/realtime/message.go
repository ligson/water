package realtime

import (
	"encoding/json"
	"time"

	"github.com/ligson/water/water-be/internal/event"
)

type Message struct {
	EventID     string          `json:"eventId"`
	Type        string          `json:"type"`
	RequestID   string          `json:"requestId"`
	WorkspaceID string          `json:"workspaceId,omitempty"`
	TaskID      string          `json:"taskId,omitempty"`
	TurnID      string          `json:"turnId,omitempty"`
	Sequence    int             `json:"sequence"`
	CreatedAt   time.Time       `json:"createdAt"`
	Payload     json.RawMessage `json:"payload"`
}

func FromEvent(e event.Event) Message {
	payload := json.RawMessage(e.PayloadJSON)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if !json.Valid(payload) {
		payload = json.RawMessage(`{}`)
	}

	return Message{
		EventID:     e.EventID,
		Type:        e.Type,
		RequestID:   e.RequestID,
		WorkspaceID: e.WorkspaceID,
		TaskID:      e.TaskID,
		TurnID:      e.TurnID,
		Sequence:    e.Sequence,
		CreatedAt:   e.CreatedAt,
		Payload:     payload,
	}
}
