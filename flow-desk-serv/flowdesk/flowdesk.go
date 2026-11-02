// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.2

package main

import (
 "context"
	"flag"
	"fmt"
	"net/http"

	"flowdesk/internal/config"
	"flowdesk/internal/handler"
	"flowdesk/internal/middleware"
	"flowdesk/internal/svc"
 "flowdesk/internal/worker"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/conf"
 "github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/rest"
)

var configFile = flag.String("f", "etc/flowdesk-api.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c, conf.UseEnv())
	if c.Mode != "dev" && !c.CookieSecure {
		panic("COOKIE_SECURE must be enabled outside dev mode")
	}
	if len(c.JwtSecret) < 32 || len(c.InternalToken) < 32 {
		panic("JWT_SECRET and INTERNAL_TOKEN must contain at least 32 characters")
	}

	server := rest.MustNewServer(c.RestConf)
	defer server.Stop()

	ctx := svc.NewServiceContext(c)
	ctx.SessionAuth = middleware.NewSessionAuthMiddleware(ctx).Handle
 ctx.ServiceAuth = middleware.NewServiceAuthMiddleware(c.InternalToken).Handle
 workerContext,cancelWorker:=context.WithCancel(context.Background())
 workerDone:=make(chan struct{})
 proc.AddShutdownListener(cancelWorker)
 go func(){defer close(workerDone);worker.Run(workerContext,ctx)}()
 defer func(){cancelWorker();<-workerDone}()
	server.Use(func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Request-ID", uuid.NewString())
			w.Header().Set("X-Content-Type-Options", "nosniff")
			next(w, r)
		}
	})
	handler.RegisterHandlers(server, ctx)

	fmt.Printf("Starting server at %s:%d...\n", c.Host, c.Port)
	server.Start()
}
