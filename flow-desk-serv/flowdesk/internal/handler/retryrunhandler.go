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

func RetryRunHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.RetryRunReq
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		data, err := logic.RetryRun(r.Context(), s, &req)
		writeBusiness(w, r, 202, data, err)
	}
}
