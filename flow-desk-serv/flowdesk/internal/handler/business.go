package handler

import (
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/logic"
	"github.com/lib/pq"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

func writeBusiness(w http.ResponseWriter, r *http.Request, status int, data any, err error) {
	requestID := w.Header().Get("X-Request-ID")
	if err == nil {
		httpx.WriteJsonCtx(r.Context(), w, status, map[string]any{"code": "OK", "message": "", "request_id": requestID, "data": data})
		return
	}
	problem := &logic.BusinessError{Status: 503, Code: "UPSTREAM_UNAVAILABLE", Message: "服务暂时不可用"}
	var specified *logic.BusinessError
	var postgresError *pq.Error
	switch {
	case errors.As(err, &specified):
		problem = specified
	case errors.Is(err, logic.ErrUnauthorized):
		problem = &logic.BusinessError{Status: 401, Code: "UNAUTHENTICATED", Message: "会话已失效，请重新登录"}
	case errors.Is(err, model.ErrNotFound):
		problem = &logic.BusinessError{Status: 404, Code: "RESOURCE_NOT_FOUND", Message: "资源不存在或不可访问"}
	case errors.Is(err, model.ErrVersionConflict):
		problem = &logic.BusinessError{Status: 409, Code: "VERSION_CONFLICT", Message: "状态已变化，请刷新后重试"}
	case errors.As(err, &postgresError) && postgresError.Code == "23505":
		problem = &logic.BusinessError{Status: 409, Code: "VERSION_CONFLICT", Message: "请求与现有记录冲突"}
	}
	httpx.WriteJsonCtx(r.Context(), w, problem.Status, map[string]any{"code": problem.Code, "message": problem.Message, "request_id": requestID, "data": problem.Data})
}
