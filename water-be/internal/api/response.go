package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/ligson/water/water-be/internal/requestid"
)

type Envelope struct {
	Success   bool        `json:"success"`
	RequestID string      `json:"requestId"`
	Message   string      `json:"message"`
	HTTPCode  int         `json:"httpCode"`
	Data      interface{} `json:"data"`
}

func WriteOK(ctx context.Context, w http.ResponseWriter, message string, data interface{}) {
	WriteJSON(ctx, w, http.StatusOK, true, message, data)
}

func WriteJSON(ctx context.Context, w http.ResponseWriter, httpCode int, success bool, message string, data interface{}) {
	if data == nil {
		data = map[string]interface{}{}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpCode)

	_ = json.NewEncoder(w).Encode(Envelope{
		Success:   success,
		RequestID: requestid.FromContext(ctx),
		Message:   message,
		HTTPCode:  httpCode,
		Data:      data,
	})
}

func WriteError(ctx context.Context, w http.ResponseWriter, httpCode int, message string) {
	WriteJSON(ctx, w, httpCode, false, message, map[string]interface{}{})
}
