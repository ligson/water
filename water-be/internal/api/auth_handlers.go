package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ligson/water/water-be/internal/auth"
)

type authUnlockRequest struct {
	PIN string `json:"pin"`
}

type authChangePINRequest struct {
	CurrentPIN string `json:"currentPin"`
	NewPIN     string `json:"newPin"`
}

func (r *Router) handleAuth(w http.ResponseWriter, req *http.Request, rest string) {
	switch rest {
	case "status":
		if req.Method != http.MethodGet {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.getAuthStatus(w, req)
	case "unlock":
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.unlockAuth(w, req)
	case "lock":
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.lockAuth(w, req)
	case "change-pin":
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.changePIN(w, req)
	default:
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
	}
}

func (r *Router) getAuthStatus(w http.ResponseWriter, req *http.Request) {
	status, err := r.authStatus(req)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get auth status", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get auth status failed")
		return
	}
	WriteOK(req.Context(), w, "ok", status)
}

func (r *Router) unlockAuth(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeAuthUnlockRequest(w, req)
	if !ok {
		return
	}
	session, err := r.authStore().Unlock(req.Context(), input.PIN)
	if err != nil {
		if errors.Is(err, auth.ErrNotConfigured) {
			WriteError(req.Context(), w, http.StatusServiceUnavailable, "auth not configured")
			return
		}
		WriteError(req.Context(), w, http.StatusUnauthorized, "pin incorrect")
		return
	}
	WriteOK(req.Context(), w, "auth unlocked", map[string]interface{}{
		"accessToken": session.Token,
		"expiresAt":   session.ExpiresAt.Format(timeRFC3339),
	})
}

func (r *Router) lockAuth(w http.ResponseWriter, req *http.Request) {
	token := authTokenFromRequest(req)
	if token == "" {
		WriteError(req.Context(), w, http.StatusUnauthorized, "auth token required")
		return
	}
	if err := r.authStore().Lock(req.Context(), token); err != nil {
		if errors.Is(err, auth.ErrNotConfigured) {
			WriteError(req.Context(), w, http.StatusServiceUnavailable, "auth not configured")
			return
		}
		r.logger.ErrorContext(req.Context(), "lock auth", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "lock auth failed")
		return
	}
	WriteOK(req.Context(), w, "auth locked", map[string]interface{}{})
}

func (r *Router) changePIN(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeAuthChangePINRequest(w, req)
	if !ok {
		return
	}
	session, err := r.authStore().ChangePIN(req.Context(), input.CurrentPIN, input.NewPIN)
	if err != nil {
		if errors.Is(err, auth.ErrNotConfigured) {
			WriteError(req.Context(), w, http.StatusServiceUnavailable, "auth not configured")
			return
		}
		WriteError(req.Context(), w, http.StatusUnauthorized, "pin incorrect")
		return
	}
	WriteOK(req.Context(), w, "pin changed", map[string]interface{}{
		"accessToken": session.Token,
		"expiresAt":   session.ExpiresAt.Format(timeRFC3339),
	})
}

func (r *Router) authStatus(req *http.Request) (map[string]interface{}, error) {
	token := authTokenFromRequest(req)
	status, err := r.authStore().Status(req.Context(), token)
	if errors.Is(err, auth.ErrNotConfigured) {
		return map[string]interface{}{
			"configured":       false,
			"authenticated":    true,
			"locked":           false,
			"sessionExpiresAt": "",
			"lastUnlockedAt":   "",
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"configured":       status.Configured,
		"authenticated":    status.Authenticated,
		"locked":           !status.Authenticated,
		"sessionExpiresAt": nullableTime(status.SessionExpiresAt),
		"lastUnlockedAt":   nullableTime(status.LastUnlockedAt),
	}, nil
}

func decodeAuthUnlockRequest(w http.ResponseWriter, req *http.Request) (authUnlockRequest, bool) {
	defer req.Body.Close()
	var input authUnlockRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return authUnlockRequest{}, false
	}
	input.PIN = strings.TrimSpace(input.PIN)
	if input.PIN == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "pin is required")
		return authUnlockRequest{}, false
	}
	return input, true
}

func decodeAuthChangePINRequest(w http.ResponseWriter, req *http.Request) (authChangePINRequest, bool) {
	defer req.Body.Close()
	var input authChangePINRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return authChangePINRequest{}, false
	}
	input.CurrentPIN = strings.TrimSpace(input.CurrentPIN)
	input.NewPIN = strings.TrimSpace(input.NewPIN)
	if input.CurrentPIN == "" || input.NewPIN == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "currentPin and newPin are required")
		return authChangePINRequest{}, false
	}
	if input.NewPIN == input.CurrentPIN {
		WriteError(req.Context(), w, http.StatusBadRequest, "new pin must be different")
		return authChangePINRequest{}, false
	}
	return input, true
}
