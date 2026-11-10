// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"net/http"
)

func GetInvestigationAutomationHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		data, err := logic.GetInvestigationAutomation(r.Context(), s)
		writeBusiness(w, r, 200, data, err)
	}
}
