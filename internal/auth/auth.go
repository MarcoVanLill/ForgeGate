package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/MarcoVanLill/ForgeGate/internal/httpx"
	"github.com/MarcoVanLill/ForgeGate/internal/storage"
)

type contextKey string

const tenantKey contextKey = "tenant"

type Authenticator interface {
	Authenticate(context.Context, [32]byte) (storage.Tenant, error)
}

func Middleware(authenticator Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			scheme, key, ok := strings.Cut(header, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(key) == "" {
				httpx.Error(w, r, http.StatusUnauthorized, "invalid_api_key", "a valid bearer API key is required")
				return
			}
			tenant, err := authenticator.Authenticate(r.Context(), storage.HashAPIKey(key))
			if err != nil {
				httpx.Error(w, r, http.StatusUnauthorized, "invalid_api_key", "the API key is invalid or expired")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantKey, tenant)))
		})
	}
}
func Tenant(ctx context.Context) (storage.Tenant, bool) {
	tenant, ok := ctx.Value(tenantKey).(storage.Tenant)
	return tenant, ok
}
