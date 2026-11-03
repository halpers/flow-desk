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

func ListDocumentsHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req types.KnowledgeBaseReq
		r.Body = http.MaxBytesReader(w, r.Body, 300*1024)
		if err := httpx.Parse(r, &req); err != nil {
			writeBusiness(w, r, 400, nil, &logic.BusinessError{Status: 400, Code: "INVALID_ARGUMENT", Message: "输入格式不正确"})
			return
		}
		req.KnowledgeBaseId = pathvar.Vars(r)["knowledge_base_id"]
		data, err := logic.ListDocuments(r.Context(), s, req.KnowledgeBaseId)
		writeBusiness(w, r, 200, data, err)
	}
}
