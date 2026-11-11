package middleware

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest/handler"
)

func TestRequestLogRouterPreservesRequestIDAndStreaming(t *testing.T) {
	var logs bytes.Buffer
	previous := logx.Reset()
	t.Cleanup(func() {
		logx.Reset()
		logx.SetWriter(previous)
	})
	logx.SetWriter(logx.NewWriter(&logs))
	router := NewRequestLogRouter()

	failing := RequestLogHeaders(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = fmt.Fprint(w, w.Header().Get("X-Request-ID"))
	})
	if err := router.Handle(http.MethodPost, "/items/:id", handler.TimeoutHandler(time.Second)(failing)); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/items/private-id?token=private-query", strings.NewReader("private-body"))
	request.Header.Set("Authorization", "Bearer private-auth")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	requestID := response.Header().Get("X-Request-ID")
	if response.Code != http.StatusServiceUnavailable || requestID == "" || response.Body.String() != requestID {
		t.Fatalf("response status=%d request ID=%q body=%q", response.Code, requestID, response.Body.String())
	}

	if err := router.Handle(http.MethodGet, "/events", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "data: ready\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
	})); err != nil {
		t.Fatal(err)
	}
	stream := httptest.NewRecorder()
	router.ServeHTTP(stream, httptest.NewRequest(http.MethodGet, "/events", nil))
	if !stream.Flushed || stream.Body.String() != "data: ready\n\n" {
		t.Fatalf("SSE was not flushed: %q", stream.Body.String())
	}
	logged := logs.String()
	for _, want := range []string{"http.request.failed", "http.request.completed", "/items/:id", requestID} {
		if !strings.Contains(logged, want) {
			t.Errorf("missing log field %q", want)
		}
	}
	if strings.Contains(logged, "private-") || strings.Contains(logged, "data: ready") {
		t.Fatal("request or response content appeared in log")
	}
}
