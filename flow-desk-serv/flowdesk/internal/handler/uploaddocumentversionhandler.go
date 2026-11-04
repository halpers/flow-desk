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

func UploadDocumentVersionHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.UploadVersionReq
		r.Body = http.MaxBytesReader(w, r.Body, logic.MaxKnowledgeRequestBytes)
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		req.DocumentId = pathvar.Vars(r)["document_id"]
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
		data, err := logic.UploadDocumentVersion(r.Context(), s, &req)
		writeBusiness(w, r, 202, data, err)
	}
}
