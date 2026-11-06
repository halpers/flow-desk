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

// GetTenantSourceInventory | permission=knowledge:manage | auth=required | role=admin
func GetTenantSourceInventoryHandler(svcCtx *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.TenantSourceInventoryReq
		if err := httpx.Parse(r, &req); err != nil {
			httpx.ErrorCtx(r.Context(), w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		data, err := logic.GetTenantSourceInventory(r.Context(), svcCtx, &req)
		writeBusiness(w, r, 200, data, err)
	}
}
