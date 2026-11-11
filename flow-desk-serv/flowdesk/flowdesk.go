// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package main

import (
	"context"
	"flag"
	"os"

	"flowdesk/internal/config"
	"flowdesk/internal/handler"
	"flowdesk/internal/middleware"
	"flowdesk/internal/svc"
	"flowdesk/internal/worker"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/flowdesk-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	if mode := os.Getenv("APP_MODE"); mode != "" {
		switch mode {
		case "dev", "test", "rt", "pre", "pro":
			c.Mode = mode
		default:
			panic("APP_MODE is invalid")
		}
	}
	if c.Mode != "dev" && !c.CookieSecure {
		panic("COOKIE_SECURE must be enabled outside dev mode")
	}
	if len(c.JwtSecret) < 32 || len(c.InternalToken) < 32 {
		panic("JWT_SECRET and INTERNAL_TOKEN must contain at least 32 characters")
	}

	// Authentication bodies contain credentials. Native error/slow-request
	// logging can dump them; metadata tracing and metrics remain enabled.
	c.Middlewares.Log = false
	server := rest.MustNewServer(c.RestConf,
		rest.WithRouter(middleware.NewRequestLogRouter()),
		rest.WithNotFoundHandler(nil),
	)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	ctx.SessionAuth = middleware.NewSessionAuthMiddleware(ctx).Handle
	ctx.ServiceAuth = middleware.NewServiceAuthMiddleware(c.InternalToken).Handle
	workerContext, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	proc.AddShutdownListener(cancelWorker)
	go func() { defer close(workerDone); worker.Run(workerContext, ctx) }()
	defer func() { cancelWorker(); <-workerDone }()
	server.Use(middleware.RequestLogHeaders)
	handler.RegisterHandlers(server, ctx)

	logx.Infow("api.starting", logx.Field("port", c.Port), logx.Field("mode", c.Mode))
	server.Start()
}
