package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const HeaderName = "X-Request-Id"

type contextKey struct{}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(HeaderName)
		if id == "" {
			id = newID()
		}

		w.Header().Set(HeaderName, id)
		ctx := context.WithValue(r.Context(), contextKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func FromContext(ctx context.Context) string {
	id, ok := ctx.Value(contextKey{}).(string)
	if !ok || id == "" {
		return newID()
	}
	return id
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(b[:])
}
