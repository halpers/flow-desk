// Code scaffolded by goctl. Safe to edit.
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"net/http"
)

func ListManagedRolesHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")

		data, err := logic.ListManagedRoles(r.Context(), s)
		writeBusiness(w, r, 200, data, err)
	}
}
