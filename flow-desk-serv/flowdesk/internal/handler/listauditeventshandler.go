// Code scaffolded by goctl. Safe to edit.
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

func ListAuditEventsHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.PageReq
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		data, err := logic.ListAuditEvents(r.Context(), s, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
