package api

import (
	"net/http"
	"strings"
	"time"
)

const timeRFC3339 = time.RFC3339Nano

func authTokenFromRequest(req *http.Request) string {
	if raw := strings.TrimSpace(req.Header.Get("Authorization")); raw != "" {
		parts := strings.Fields(raw)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return parts[1]
		}
	}
	if token := strings.TrimSpace(req.Header.Get("X-Water-Auth")); token != "" {
		return token
	}
	if token := strings.TrimSpace(req.URL.Query().Get("accessToken")); token != "" {
		return token
	}
	return ""
}

func nullableTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(timeRFC3339)
}
