package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ligson/water/water-be/internal/llm"
	"github.com/ligson/water/water-be/internal/provider"
)

type providerRequest struct {
	Name                string  `json:"name"`
	Type                string  `json:"type"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              *string `json:"apiKey"`
	IsDefault           bool    `json:"isDefault"`
	Enabled             *bool   `json:"enabled"`
	ContextWindowTokens int     `json:"contextWindowTokens"`
	TimeoutMS           int     `json:"timeoutMs"`
	MaxRetries          int     `json:"maxRetries"`
	StreamIdleTimeoutMS int     `json:"streamIdleTimeoutMs"`
	HeadersJSON         string  `json:"headersJson"`
}

type providerModelsRequest struct {
	ProviderID *string `json:"providerId"`
	BaseURL    *string `json:"baseUrl"`
	APIKey     *string `json:"apiKey"`
}

func (r *Router) handleProviders(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.listProviders(w, req)
	case http.MethodPost:
		r.createProvider(w, req)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleProviderByID(w http.ResponseWriter, req *http.Request, rest string) {
	id, action, ok := splitProviderPath(rest)
	if !ok {
		WriteError(req.Context(), w, http.StatusNotFound, "not found")
		return
	}

	if action == "test" {
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.testProvider(w, req, id)
		return
	}

	if action == "default" {
		if req.Method != http.MethodPost {
			WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		r.setDefaultProvider(w, req, id)
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.getProvider(w, req, id)
	case http.MethodPut:
		r.updateProvider(w, req, id)
	case http.MethodDelete:
		r.deleteProvider(w, req, id)
	default:
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) handleProviderModels(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(req.Context(), w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	input, ok := decodeProviderModelsRequest(w, req)
	if !ok {
		return
	}

	p, err := r.resolveProviderModelSource(req.Context(), input)
	if err != nil {
		if errors.Is(err, provider.ErrNotFound) {
			WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
			return
		}
		r.logger.ErrorContext(req.Context(), "resolve provider for model list", "error", err)
		WriteError(req.Context(), w, http.StatusBadRequest, err.Error())
		return
	}

	items, err := llm.ListProviderModels(req.Context(), p)
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list provider models", "error", err)
		WriteError(req.Context(), w, http.StatusBadGateway, providerModelListErrorMessage(err))
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": items})
}

func (r *Router) listProviders(w http.ResponseWriter, req *http.Request) {
	providers, err := provider.NewStore(r.db).List(req.Context())
	if err != nil {
		r.logger.ErrorContext(req.Context(), "list providers", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "list providers failed")
		return
	}
	WriteOK(req.Context(), w, "ok", map[string]interface{}{"items": providers})
}

func (r *Router) createProvider(w http.ResponseWriter, req *http.Request) {
	input, ok := decodeProviderRequest(w, req)
	if !ok {
		return
	}

	created, err := provider.NewStore(r.db).Create(req.Context(), provider.CreateInput{
		Name:                input.Name,
		Type:                input.Type,
		BaseURL:             input.BaseURL,
		Model:               input.Model,
		APIKey:              derefString(input.APIKey),
		IsDefault:           input.IsDefault,
		Enabled:             enabledOrDefault(input.Enabled),
		ContextWindowTokens: input.ContextWindowTokens,
		TimeoutMS:           input.TimeoutMS,
		MaxRetries:          input.MaxRetries,
		StreamIdleTimeoutMS: input.StreamIdleTimeoutMS,
		HeadersJSON:         input.HeadersJSON,
	})
	if err != nil {
		r.logger.ErrorContext(req.Context(), "create provider", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "create provider failed")
		return
	}
	WriteJSON(req.Context(), w, http.StatusCreated, true, "provider created", created)
}

func (r *Router) getProvider(w http.ResponseWriter, req *http.Request, id string) {
	p, err := provider.NewStore(r.db).Get(req.Context(), id)
	if errors.Is(err, provider.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get provider", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get provider failed")
		return
	}
	WriteOK(req.Context(), w, "ok", p)
}

func (r *Router) updateProvider(w http.ResponseWriter, req *http.Request, id string) {
	input, ok := decodeProviderRequest(w, req)
	if !ok {
		return
	}

	updated, err := provider.NewStore(r.db).Update(req.Context(), id, provider.UpdateInput{
		Name:                input.Name,
		Type:                input.Type,
		BaseURL:             input.BaseURL,
		Model:               input.Model,
		APIKey:              input.APIKey,
		IsDefault:           input.IsDefault,
		Enabled:             enabledOrDefault(input.Enabled),
		ContextWindowTokens: input.ContextWindowTokens,
		TimeoutMS:           input.TimeoutMS,
		MaxRetries:          input.MaxRetries,
		StreamIdleTimeoutMS: input.StreamIdleTimeoutMS,
		HeadersJSON:         input.HeadersJSON,
	})
	if errors.Is(err, provider.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "update provider", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "update provider failed")
		return
	}
	WriteOK(req.Context(), w, "provider updated", updated)
}

func (r *Router) deleteProvider(w http.ResponseWriter, req *http.Request, id string) {
	err := provider.NewStore(r.db).Delete(req.Context(), id)
	if errors.Is(err, provider.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "delete provider", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "delete provider failed")
		return
	}
	WriteOK(req.Context(), w, "provider deleted", map[string]interface{}{})
}

func (r *Router) setDefaultProvider(w http.ResponseWriter, req *http.Request, id string) {
	p, err := provider.NewStore(r.db).SetDefault(req.Context(), id)
	if errors.Is(err, provider.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "set default provider", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "set default provider failed")
		return
	}
	WriteOK(req.Context(), w, "default provider updated", p)
}

func (r *Router) testProvider(w http.ResponseWriter, req *http.Request, id string) {
	p, err := provider.NewStore(r.db).Get(req.Context(), id)
	if errors.Is(err, provider.ErrNotFound) {
		WriteError(req.Context(), w, http.StatusNotFound, "provider not found")
		return
	}
	if err != nil {
		r.logger.ErrorContext(req.Context(), "get provider for test", "error", err)
		WriteError(req.Context(), w, http.StatusInternalServerError, "get provider failed")
		return
	}

	result := llm.TestProvider(req.Context(), p)
	if !result.OK {
		WriteJSON(req.Context(), w, http.StatusBadGateway, false, result.Message, result)
		return
	}
	WriteOK(req.Context(), w, result.Message, result)
}

func decodeProviderRequest(w http.ResponseWriter, req *http.Request) (providerRequest, bool) {
	defer req.Body.Close()

	var input providerRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return providerRequest{}, false
	}

	input.Name = strings.TrimSpace(input.Name)
	input.Type = strings.TrimSpace(input.Type)
	input.BaseURL = strings.TrimSpace(input.BaseURL)
	input.Model = strings.TrimSpace(input.Model)
	input.HeadersJSON = strings.TrimSpace(input.HeadersJSON)

	if input.Name == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "name is required")
		return providerRequest{}, false
	}
	if input.BaseURL == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "baseUrl is required")
		return providerRequest{}, false
	}
	if input.Model == "" {
		WriteError(req.Context(), w, http.StatusBadRequest, "model is required")
		return providerRequest{}, false
	}
	if input.Type != "" && input.Type != provider.TypeOpenAICompatible {
		WriteError(req.Context(), w, http.StatusBadRequest, "unsupported provider type")
		return providerRequest{}, false
	}
	if input.HeadersJSON == "" {
		input.HeadersJSON = "{}"
	}
	if input.ContextWindowTokens < 0 {
		WriteError(req.Context(), w, http.StatusBadRequest, "contextWindowTokens cannot be negative")
		return providerRequest{}, false
	}
	if !json.Valid([]byte(input.HeadersJSON)) {
		WriteError(req.Context(), w, http.StatusBadRequest, "headersJson must be valid json")
		return providerRequest{}, false
	}

	return input, true
}

func decodeProviderModelsRequest(w http.ResponseWriter, req *http.Request) (providerModelsRequest, bool) {
	defer req.Body.Close()

	var input providerModelsRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		WriteError(req.Context(), w, http.StatusBadRequest, "invalid json body")
		return providerModelsRequest{}, false
	}
	if input.ProviderID != nil {
		value := strings.TrimSpace(*input.ProviderID)
		if value == "" {
			input.ProviderID = nil
		} else {
			input.ProviderID = &value
		}
	}
	if input.BaseURL != nil {
		value := strings.TrimSpace(*input.BaseURL)
		if value == "" {
			input.BaseURL = nil
		} else {
			input.BaseURL = &value
		}
	}
	if input.APIKey != nil {
		value := strings.TrimSpace(*input.APIKey)
		if value == "" {
			input.APIKey = nil
		} else {
			input.APIKey = &value
		}
	}
	return input, true
}

func (r *Router) resolveProviderModelSource(ctx context.Context, input providerModelsRequest) (provider.Provider, error) {
	source := provider.Provider{
		Type:        provider.TypeOpenAICompatible,
		Enabled:     true,
		HeadersJSON: "{}",
	}

	if input.ProviderID != nil {
		p, err := provider.NewStore(r.db).Get(ctx, *input.ProviderID)
		if err != nil {
			return provider.Provider{}, err
		}
		source = p
	}

	if input.BaseURL != nil {
		source.BaseURL = *input.BaseURL
	}
	if input.APIKey != nil {
		source.APIKey = *input.APIKey
	}
	if source.BaseURL == "" {
		return provider.Provider{}, fmt.Errorf("baseUrl is required")
	}
	source.Enabled = true
	if source.HeadersJSON == "" {
		source.HeadersJSON = "{}"
	}
	return source, nil
}

func splitProviderPath(rest string) (id string, action string, ok bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		return parts[0], "", true
	}
	if len(parts) == 2 && (parts[1] == "test" || parts[1] == "default") {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func enabledOrDefault(value *bool) bool {
	if value == nil {
		return true
	}
	return *value
}

func providerModelListErrorMessage(err error) string {
	if err == nil {
		return "获取模型列表失败"
	}
	message := err.Error()
	lower := strings.ToLower(message)
	if strings.Contains(lower, "http 404") || strings.TrimSpace(lower) == "not found" {
		return "模型服务未提供 /models 列表接口，可以继续手动填写模型名"
	}
	if strings.Contains(lower, "http 401") || strings.Contains(lower, "http 403") || strings.Contains(lower, "invalid token") {
		return "模型服务认证失败，请检查 API Key 后重试"
	}
	return message
}
