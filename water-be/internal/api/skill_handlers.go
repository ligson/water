package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ligson/water/water-be/internal/skill"
)

const maxSkillDownloadBytes = 20 * 1024 * 1024

type skillInstallRequest struct {
	URL string `json:"url"`
}

func (r *Router) handleSkills(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		items, err := r.skillStore().List(req.Context())
		if err != nil {
			r.logger.ErrorContext(req.Context(), "list skills", "error", err)
			WriteError(req.Context(), w, http.StatusInternalServerError, "list skills failed")
			return
		}
		WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
	case http.MethodPost:
		r.installSkillFromUpload(w, req)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleSkillByID(w http.ResponseWriter, req *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" || len(parts) > 2 {
		WriteError(req.Context(), w, http.StatusNotFound, "skill not found")
		return
	}
	id, err := url.PathUnescape(parts[0])
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid skill id")
		return
	}
	if len(parts) == 1 {
		if req.Method != http.MethodDelete {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := r.skillStore().Delete(req.Context(), id); errors.Is(err, skill.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "skill not found")
		} else if err != nil {
			r.logger.ErrorContext(req.Context(), "delete skill", "skillId", id, "error", err)
			WriteError(req.Context(), w, http.StatusInternalServerError, "delete skill failed")
		} else {
			WriteOK(req.Context(), w, "skill deleted", map[string]interface{}{})
		}
		return
	}
	if req.Method != http.MethodPost || (parts[1] != "enable" && parts[1] != "disable") {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, err := r.skillStore().SetEnabled(req.Context(), id, parts[1] == "enable")
	if errors.Is(err, skill.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "skill not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "set skill enabled", "skillId", id, "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "update skill failed")
		return
	}
	WriteOK(req.Context(), w, "skill updated", item)
}

func (r *Router) installSkillFromUpload(w http.ResponseWriter, req *http.Request) {
	if req.ContentLength > maxSkillDownloadBytes {
		WriteError(req.Context(), w, http.StatusRequestEntityTooLarge, "skill package exceeds 20 MiB")
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxSkillDownloadBytes+1024)
	if err := req.ParseMultipartForm(maxSkillDownloadBytes + 1024); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid skill upload")
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "skill upload requires a file field")
		return
	}
	defer file.Close()
	archive, err := io.ReadAll(io.LimitReader(file, maxSkillDownloadBytes+1))
	if err != nil || len(archive) > maxSkillDownloadBytes {
		WriteError(req.Context(), w, http.StatusRequestEntityTooLarge, "skill package exceeds 20 MiB")
		return
	}
	item, err := r.skillStore().InstallArchive(req.Context(), archive, "upload", header.Filename)
	if err != nil {
		r.writeSkillInstallError(w, req, err)
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "skill installed and disabled by default", item)
}

func (r *Router) installSkillFromURL(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var input skillInstallRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return
	}
	target := strings.TrimSpace(input.URL)
	parsedURL, err := url.Parse(target)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		WriteError(req.Context(), w, http.StatusBadRequest, "skill URL must use http or https")
		return
	}
	ctx, cancel := contextWithSkillDownloadTimeout(req.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid skill URL")
		return
	}
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		WriteError(req.Context(), w, http.StatusBadGateway, "download skill package failed")
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		WriteError(req.Context(), w, http.StatusBadGateway, fmt.Sprintf("skill download returned HTTP %d", response.StatusCode))
		return
	}
	if response.ContentLength > maxSkillDownloadBytes {
		WriteError(req.Context(), w, http.StatusRequestEntityTooLarge, "skill package exceeds 20 MiB")
		return
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, maxSkillDownloadBytes+1))
	if err != nil || len(archive) > maxSkillDownloadBytes {
		WriteError(req.Context(), w, http.StatusRequestEntityTooLarge, "skill package exceeds 20 MiB")
		return
	}
	item, err := r.skillStore().InstallArchive(req.Context(), archive, "url", publicSkillSourceURL(parsedURL))
	if err != nil {
		r.writeSkillInstallError(w, req, err)
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "skill installed and disabled by default", item)
}

func (r *Router) writeSkillInstallError(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, skill.ErrInvalidPackage) {
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}
	r.logger.ErrorContext(req.Context(), "install skill", "error", err)
	WriteError(req.Context(), w, http.StatusInternalServerError, "install skill failed")
}

func contextWithSkillDownloadTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}

func publicSkillSourceURL(input *url.URL) string {
	copy := *input
	copy.User = nil
	copy.RawQuery = ""
	copy.Fragment = ""
	return copy.String()
}
