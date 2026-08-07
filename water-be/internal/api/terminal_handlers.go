package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ligson/water/water-be/internal/terminal"
	"github.com/ligson/water/water-be/internal/workspace"
)

type terminalProfileRequest struct {
	Name            string `json:"name"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username"`
	AuthType        string `json:"authType"`
	Password        string `json:"password"`
	PrivateKey      string `json:"privateKey"`
	Passphrase      string `json:"passphrase"`
	DefaultCwd      string `json:"defaultCwd"`
	HostFingerprint string `json:"hostFingerprint"`
	Enabled         bool   `json:"enabled"`
}

type terminalSessionRequest struct {
	WorkspaceID string `json:"workspaceId"`
	ProfileID   string `json:"profileId"`
	Cwd         string `json:"cwd"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
}

func (r *Router) handleWorkspaceTerminalProfiles(w http.ResponseWriter, req *http.Request, workspaceID string) {
	switch req.Method {
	case http.MethodGet:
		r.listTerminalProfiles(w, req, workspaceID)
	case http.MethodPost:
		r.createTerminalProfile(w, req, workspaceID)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) listTerminalProfiles(w http.ResponseWriter, req *http.Request, workspaceID string) {
	if _, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID); err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
			return
		}
		r.logger.ErrorContext(req.Context(), "get workspace for terminal profiles", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}
	items, err := terminal.NewStore(r.db).ListProfiles(req.Context(), workspaceID)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list terminal profiles", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list terminal profiles failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) createTerminalProfile(w http.ResponseWriter, req *http.Request, workspaceID string) {
	if _, err := workspace.NewStore(r.db).Get(req.Context(), workspaceID); err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
			return
		}
		r.logger.ErrorContext(req.Context(), "get workspace for terminal profile", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}
	input, ok := decodeTerminalProfileRequest(w, req)
	if !ok {
		return
	}
	created, err := terminal.NewStore(r.db).CreateProfile(req.Context(), terminal.CreateProfileInput{
		WorkspaceID:     workspaceID,
		Name:            input.Name,
		Host:            input.Host,
		Port:            input.Port,
		Username:        input.Username,
		AuthType:        input.AuthType,
		Password:        input.Password,
		PrivateKey:      input.PrivateKey,
		Passphrase:      input.Passphrase,
		DefaultCwd:      input.DefaultCwd,
		HostFingerprint: input.HostFingerprint,
		Enabled:         input.Enabled,
	})
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create terminal profile", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create terminal profile failed")
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "terminal profile created", created)
}

func (r *Router) handleTerminalProfileByID(w http.ResponseWriter, req *http.Request, profileID string) {
	switch req.Method {
	case http.MethodPut:
		r.updateTerminalProfile(w, req, profileID)
	case http.MethodDelete:
		r.deleteTerminalProfile(w, req, profileID)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) updateTerminalProfile(w http.ResponseWriter, req *http.Request, profileID string) {
	input, ok := decodeTerminalProfileRequest(w, req)
	if !ok {
		return
	}
	var password *string
	if input.Password != "" {
		password = &input.Password
	}
	var privateKey *string
	if input.PrivateKey != "" {
		privateKey = &input.PrivateKey
	}
	var passphrase *string
	if input.Passphrase != "" {
		passphrase = &input.Passphrase
	}
	updated, err := terminal.NewStore(r.db).UpdateProfile(req.Context(), profileID, terminal.UpdateProfileInput{
		Name:            input.Name,
		Host:            input.Host,
		Port:            input.Port,
		Username:        input.Username,
		AuthType:        input.AuthType,
		Password:        password,
		PrivateKey:      privateKey,
		Passphrase:      passphrase,
		DefaultCwd:      input.DefaultCwd,
		HostFingerprint: input.HostFingerprint,
		Enabled:         input.Enabled,
	})
	if errors.Is(err, terminal.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "terminal profile not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "update terminal profile", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "update terminal profile failed")
		return
	}
	WriteOK(req.Context(), w, "terminal profile updated", updated)
}

func (r *Router) deleteTerminalProfile(w http.ResponseWriter, req *http.Request, profileID string) {
	err := terminal.NewStore(r.db).DeleteProfile(req.Context(), profileID)
	if errors.Is(err, terminal.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "terminal profile not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "delete terminal profile", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "delete terminal profile failed")
		return
	}
	WriteOK(req.Context(), w, "terminal profile deleted", map[string]interface{}{})
}

func (r *Router) handleTerminalSessions(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var input terminalSessionRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(input.WorkspaceID) == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "workspaceId is required")
		return
	}
	workspaceItem, err := workspace.NewStore(r.db).Get(req.Context(), input.WorkspaceID)
	if errors.Is(err, workspace.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "workspace not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get workspace for terminal session", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get workspace failed")
		return
	}
	cwd := strings.TrimSpace(input.Cwd)
	sessionProfileID := input.ProfileID
	if sessionProfileID != "" {
		profile, err := terminal.NewStore(r.db).GetProfile(req.Context(), sessionProfileID)
		if errors.Is(err, terminal.ErrNotFound) || profile.WorkspaceID != input.WorkspaceID {
			WriteError(req.Context(), w, http.StatusNotFound, "terminal profile not found")
			return
		}
		if err != nil {
			r.logger.ErrorContext(req.Context(), "get terminal profile for session", "error", err)
			WriteError(req.Context(), w, http.StatusInternalServerError, "get terminal profile failed")
			return
		}
		if cwd == "" && strings.TrimSpace(profile.DefaultCwd) != "" {
			cwd = profile.DefaultCwd
		}
	} else {
		localProfile, err := terminal.NewStore(r.db).EnsureLocalProfile(req.Context(), input.WorkspaceID, workspaceItem.RootPath)
		if err != nil {
			r.logger.ErrorContext(req.Context(), "ensure local terminal profile", "error", err)
			WriteError(req.Context(), w, http.StatusInternalServerError, "ensure local terminal profile failed")
			return
		}
		sessionProfileID = localProfile.ID
	}
	if cwd == "" {
		cwd = workspaceItem.RootPath
	}
	created, err := terminal.NewStore(r.db).CreateSession(req.Context(), terminal.CreateSessionInput{
		WorkspaceID: input.WorkspaceID,
		ProfileID:   sessionProfileID,
		Cwd:         cwd,
		Cols:        input.Cols,
		Rows:        input.Rows,
	})
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create terminal session", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create terminal session failed")
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "terminal session created", created)
}

func (r *Router) handleTerminalSessionByID(w http.ResponseWriter, req *http.Request, sessionID string) {
	if req.Method != http.MethodDelete {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := terminal.NewStore(r.db).UpdateSessionStatus(req.Context(), sessionID, terminal.SessionStatusClosed); err != nil {
		if errors.Is(err, terminal.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "terminal session not found")
			return
		}
		r.logger.ErrorContext(req.Context(), "close terminal session", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "close terminal session failed")
		return
	}
	WriteOK(req.Context(), w, "terminal session closed", map[string]interface{}{})
}

func decodeTerminalProfileRequest(w http.ResponseWriter, req *http.Request) (terminalProfileRequest, bool) {
	var input terminalProfileRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return terminalProfileRequest{}, false
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.AuthType = strings.TrimSpace(input.AuthType)
	if input.Name == "" || input.Host == "" || input.Username == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "name, host and username are required")
		return terminalProfileRequest{}, false
	}
	if input.AuthType == "" {
		input.AuthType = terminal.AuthTypePassword
	}
	if input.AuthType != terminal.AuthTypePassword && input.AuthType != terminal.AuthTypePrivateKey {
		WriteError(req.Context(), w, http.StatusBadRequest, "unsupported authType")
		return terminalProfileRequest{}, false
	}
	if input.Port <= 0 {
		input.Port = 22
	}
	return input, true
}
