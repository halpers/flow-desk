// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package middleware

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
	"strings"
)

type SessionAuthMiddleware struct{ svc *svc.ServiceContext }

func NewSessionAuthMiddleware(s *svc.ServiceContext) *SessionAuthMiddleware {
	return &SessionAuthMiddleware{svc: s}
}
func (m *SessionAuthMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set this before authentication so missing, expired and revoked credentials
		// receive the same non-cacheable response as the password handler.
		if r.URL.Path == "/api/v1/auth/password" {
			w.Header().Set("Cache-Control", "no-store")
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.WriteJson(w, 401, map[string]string{"code": "UNAUTHENTICATED"})
			return
		}
		identity, err := logic.Authenticate(r.Context(), m.svc, strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			httpx.WriteJson(w, 401, map[string]string{"code": "UNAUTHENTICATED"})
			return
		}
		next(w, r.WithContext(logic.WithIdentity(r.Context(), identity)))
	}
}
