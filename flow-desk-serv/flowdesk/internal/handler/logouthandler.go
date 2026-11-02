// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/svc"
	"net/http"
)

func LogoutHandler(s *svc.ServiceContext) http.HandlerFunc { return logoutHandler(s) }
