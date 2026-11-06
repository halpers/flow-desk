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

// ArchiveInvestigationBatch | permission=knowledge:manage | auth=required | role=admin
func ArchiveInvestigationBatchHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var req types.ArchiveInvestigationBatchReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		data, err := logic.ArchiveInvestigationBatch(r.Context(), s, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
