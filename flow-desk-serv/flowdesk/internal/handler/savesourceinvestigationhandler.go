// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"net/http"

	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
)

// SaveSourceInvestigation | permission=knowledge:manage | auth=required | role=admin
func SaveSourceInvestigationHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SaveSourceInvestigationReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		data, err := logic.SaveSourceInvestigation(r.Context(), s, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
