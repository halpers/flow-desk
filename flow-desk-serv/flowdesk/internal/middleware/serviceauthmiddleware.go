// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package middleware

import (
	"crypto/subtle"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

type ServiceAuthMiddleware struct {
	token string
}

func NewServiceAuthMiddleware(token string) *ServiceAuthMiddleware {
	return &ServiceAuthMiddleware{token: token}
}

func (m *ServiceAuthMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if len(m.token) < 32 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+m.token)) != 1 {
			httpx.WriteJson(w, http.StatusUnauthorized, map[string]string{"code": "UNAUTHENTICATED"})
			return
		}
		next(w, r)
	}
}
