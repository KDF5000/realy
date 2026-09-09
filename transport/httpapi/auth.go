package httpapi

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/KDF5000/relay/controlplane"
)

type Token struct {
	Value string
	Scope controlplane.AccessScope
}
type Authenticator interface {
	Authenticate(string) (controlplane.AccessScope, bool)
}
type StaticTokens []Token

func (tokens StaticTokens) Authenticate(value string) (controlplane.AccessScope, bool) {
	for _, candidate := range tokens {
		if subtle.ConstantTimeCompare([]byte(value), []byte(candidate.Value)) == 1 {
			return candidate.Scope, true
		}
	}
	return controlplane.AccessScope{}, false
}
func authenticate(next http.Handler, auth Authenticator) http.Handler {
	if auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/console" || strings.HasPrefix(r.URL.Path, "/console/") || r.URL.Path == "/v1/console/session" {
			next.ServeHTTP(w, r)
			return
		}
		header := r.Header.Get("Authorization")
		value := ""
		if strings.HasPrefix(header, "Bearer ") {
			value = strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		} else if cookie, err := r.Cookie("relay_host_token"); err == nil {
			value = cookie.Value
		}
		if value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bearer token is required"})
			return
		}
		scope, ok := auth.Authenticate(value)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid bearer token"})
			return
		}
		nodeRoute := strings.HasPrefix(r.URL.Path, "/v1/attempts/") || (r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/nodes/"))
		if nodeRoute && scope.Kind != controlplane.AccessNode {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "node token is required"})
			return
		}
		if !nodeRoute && scope.Kind != controlplane.AccessHost {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "host token is required"})
			return
		}
		if scope.Kind == controlplane.AccessNode && strings.HasPrefix(r.URL.Path, "/v1/nodes/") {
			parts := strings.Split(r.URL.Path, "/")
			if len(parts) > 3 && parts[3] != "register" && scope.Subject != "" && parts[3] != scope.Subject {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "node identity mismatch"})
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(controlplane.WithAccess(r.Context(), scope)))
	})
}
