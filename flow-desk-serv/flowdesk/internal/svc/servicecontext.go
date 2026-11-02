// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package svc

import (
	"flowdesk/db/model"
	"flowdesk/internal/config"

	_ "github.com/lib/pq"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"github.com/zeromicro/go-zero/rest"
	"golang.org/x/time/rate"
)

const loginRequestsPerSecond = 2
const loginBurst = 10

type ServiceContext struct {
	Config        config.Config
	Conn          sqlx.SqlConn
	UsersModel    model.UsersModel
	TenantsModel  model.TenantsModel
	SessionsModel model.SessionsModel
	LoginLimiter  *rate.Limiter
	SessionAuth   rest.Middleware
}

func NewServiceContext(c config.Config) *ServiceContext {
	// SQL parameters include credential hashes; never log expanded statements.
	sqlx.DisableLog()
	conn := sqlx.NewSqlConn("postgres", c.DatabaseUrl)
	return &ServiceContext{
		Config:        c,
		Conn:          conn,
		UsersModel:    model.NewUsersModel(conn),
		TenantsModel:  model.NewTenantsModel(conn),
		SessionsModel: model.NewSessionsModel(conn),
		LoginLimiter:  rate.NewLimiter(rate.Limit(loginRequestsPerSecond), loginBurst),
	}
}
