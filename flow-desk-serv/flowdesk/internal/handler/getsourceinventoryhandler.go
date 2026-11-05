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

// GetSourceInventory | permission=knowledge:manage | auth=required
func GetSourceInventoryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.SourceInventoryReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		data, err := logic.GetSourceInventory(r.Context(), svcCtx, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
