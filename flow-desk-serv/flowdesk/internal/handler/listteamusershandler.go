// Code scaffolded by goctl. Safe to edit.
package handler

import (
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"net/http"
)

func ListTeamUsersHandler(s *svc.ServiceContext) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := logic.ListTeamUsers(r.Context(), s)
		writeBusiness(w, r, 200, data, err)
	}
}
