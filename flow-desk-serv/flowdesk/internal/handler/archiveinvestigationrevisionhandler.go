// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

// ArchiveInvestigationRevision | permission=knowledge:manage | auth=required | role=admin
func ArchiveInvestigationRevisionHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var req types.ArchiveInvestigationRevisionReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		data, err := logic.ArchiveInvestigationRevision(r.Context(), s, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
