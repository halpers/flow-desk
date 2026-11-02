// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/svc"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

func ReadyHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		db, err := s.Conn.RawDB()
		if err != nil || db.PingContext(r.Context()) != nil {
			httpx.WriteJson(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		httpx.OkJson(w, map[string]string{"status": "ok", "service": "flowdesk-api"})
	}
}
