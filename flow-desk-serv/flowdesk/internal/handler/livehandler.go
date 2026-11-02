// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

func LiveHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpx.OkJson(w, map[string]string{"status": "ok", "service": "flowdesk-api"})
	}
}
