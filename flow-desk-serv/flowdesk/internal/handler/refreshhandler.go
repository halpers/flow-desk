// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2
package handler

import (
	"flowdesk/internal/svc"
	"net/http"
)

func RefreshHandler(s *svc.ServiceContext) http.HandlerFunc { return refreshHandler(s) }
