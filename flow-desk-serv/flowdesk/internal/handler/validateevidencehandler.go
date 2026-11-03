// Code scaffolded by goctl. Safe to edit.
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/rest/httpx"
	"github.com/zeromicro/go-zero/rest/pathvar"
	"net/http"
)

func ValidateEvidenceHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.EvidenceReq
		r.Body = http.MaxBytesReader(w, r.Body, 300*1024)
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		req.RunId = pathvar.Vars(r)["run_id"]
		data, err := logic.ValidateEvidence(r.Context(), s, &req, r.Header.Get("X-Run-Capability"))
		writeBusiness(w, r, 200, data, err)
	}
}
