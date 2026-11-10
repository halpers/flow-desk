package handler

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"time"

	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

const maximumPasswordChangeBodyBytes = 1024

func ChangeOwnPasswordHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !trustedOrigin(s, r) {
			writeBusiness(w, r, 403, nil, &logic.BusinessError{Status: 403, Code: "ORIGIN_REJECTED", Message: "请求来源不可信"})
			return
		}
		mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maximumPasswordChangeBodyBytes))
		decoder.DisallowUnknownFields()
		var req types.ChangeOwnPasswordReq
		if mediaErr != nil || mediaType != "application/json" || decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		if err := logic.ChangeOwnPassword(r.Context(), s, &req); err != nil {
			writeBusiness(w, r, 200, nil, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: refreshCookieName, Path: "/api/v1/auth", HttpOnly: true, Secure: s.Config.CookieSecure || r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
		httpx.OkJsonCtx(r.Context(), w, types.EmptyResp{Code: "OK", RequestId: w.Header().Get("X-Request-ID")})
	}
}
