package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
	"strings"
	"time"
)

const refreshCookieName = "flowdesk_refresh"

func authError(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJsonCtx(r.Context(), w, http.StatusUnauthorized, map[string]any{"code": "UNAUTHENTICATED", "message": "账号、密码或会话无效", "request_id": w.Header().Get("X-Request-ID")})
}
func setRefresh(s *svc.ServiceContext, w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Value: token, Path: "/api/v1/auth", HttpOnly: true, Secure: s.Config.CookieSecure || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(logic.RefreshLifetime.Seconds())})
}
func cookieToken(r *http.Request) string {
	cookie, err := r.Cookie(refreshCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func trustedOrigin(s *svc.ServiceContext, r *http.Request) bool {
	if r.Header.Get("X-FlowDesk-Client") != "web" {
		return false
	}
	for _, origin := range strings.Split(s.Config.AllowedOrigins, ",") {
		if r.Header.Get("Origin") == strings.TrimSpace(origin) {
			return true
		}
	}
	return false
}

func loginHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !trustedOrigin(s, r) {
			httpx.WriteJson(w, 403, map[string]string{"code": "ORIGIN_REJECTED"})
			return
		}
		var req types.LoginReq
		if httpx.Parse(r, &req) != nil {
			authError(w, r)
			return
		}
		if !s.LoginLimiter.Allow() {
			httpx.WriteJson(w, 429, map[string]string{"code": "RATE_LIMITED"})
			return
		}
		resp, raw, err := logic.Login(r.Context(), s, &req)
		if err != nil {
			authError(w, r)
			return
		}
		resp.RequestId = w.Header().Get("X-Request-ID")
		setRefresh(s, w, r, raw)
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
func refreshHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !trustedOrigin(s, r) {
			httpx.WriteJson(w, 403, map[string]string{"code": "ORIGIN_REJECTED"})
			return
		}
		resp, raw, err := logic.Refresh(r.Context(), s, cookieToken(r))
		if err != nil {
			authError(w, r)
			return
		}
		resp.RequestId = w.Header().Get("X-Request-ID")
		setRefresh(s, w, r, raw)
		httpx.OkJsonCtx(r.Context(), w, resp)
	}
}
func logoutHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !trustedOrigin(s, r) {
			httpx.WriteJson(w, 403, map[string]string{"code": "ORIGIN_REJECTED"})
			return
		}
		if err := logic.Logout(r.Context(), s, cookieToken(r)); err != nil {
			httpx.WriteJson(w, 503, map[string]string{"code": "DATABASE_UNAVAILABLE"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Path: "/api/v1/auth", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
		httpx.OkJsonCtx(r.Context(), w, types.EmptyResp{Code: "OK", RequestId: w.Header().Get("X-Request-ID")})
	}
}
func meHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		current := logic.Current(r.Context())
		if current == nil {
			authError(w, r)
			return
		}
		u := current.User
		httpx.OkJsonCtx(r.Context(), w, types.UserResp{Code: "OK", RequestId: w.Header().Get("X-Request-ID"), Data: types.UserData{Id: u.Id, TenantId: u.TenantId, Account: u.Account, DisplayName: u.DisplayName, Role: u.Role, Permissions: logic.Permissions(u.Role)}})
	}
}
