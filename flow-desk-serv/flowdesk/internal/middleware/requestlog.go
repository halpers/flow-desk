package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest/httpx"
	"github.com/zeromicro/go-zero/rest/router"
)

// NewRequestLogRouter records matched routes outside the framework middleware,
// so the status also reflects timeouts and rejections by that middleware.
func NewRequestLogRouter() httpx.Router {
	return requestLogRouter{Router: router.NewRouter()}
}

type requestLogRouter struct{ httpx.Router }
type requestLogIDKey struct{}

// RequestLogHeaders also populates the framework's buffered response writer,
// so handlers can read the same request ID when building response envelopes.
func RequestLogHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if requestID, ok := r.Context().Value(requestLogIDKey{}).(string); ok {
			setRequestLogHeaders(w, requestID)
		}
		next(w, r)
	}
}

func setRequestLogHeaders(w http.ResponseWriter, requestID string) {
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func (r requestLogRouter) Handle(method, route string, handler http.Handler) error {
	return r.Router.Handle(method, route, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		started := time.Now()
		requestID := uuid.NewString()
		setRequestLogHeaders(w, requestID)
		req = req.WithContext(context.WithValue(req.Context(), requestLogIDKey{}, requestID))

		response := &requestLogWriter{ResponseWriter: w}
		handler.ServeHTTP(response, req)
		status := response.status
		if status == 0 {
			status = http.StatusOK
		}
		// Health checks and scrapes should only appear when they fail.
		if status < 400 && (route == "/health/live" || route == "/health/ready" || route == "/metrics") {
			return
		}
		fields := []logx.LogField{
			logx.Field("request_id", requestID),
			logx.Field("method", method),
			logx.Field("route", route),
			logx.Field("status", status),
			logx.Field("duration_ms", time.Since(started).Milliseconds()),
		}
		if status >= 500 {
			logx.Errorw("http.request.failed", fields...)
		} else {
			logx.Infow("http.request.completed", fields...)
		}
	}))
}

type requestLogWriter struct {
	http.ResponseWriter
	status int
}

func (w *requestLogWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *requestLogWriter) WriteHeader(status int) {
	if w.status == 0 && (status >= 200 || status == http.StatusSwitchingProtocols) {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *requestLogWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

// go-zero's streaming wrappers require http.Flusher directly.
func (w *requestLogWriter) Flush() { _ = w.FlushError() }

func (w *requestLogWriter) FlushError() error {
	if err := http.NewResponseController(w.ResponseWriter).Flush(); err != nil {
		return err
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return nil
}
