package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"flowdesk/internal/config"
	"flowdesk/internal/middleware"
	"flowdesk/internal/svc"
	"github.com/golang-jwt/jwt/v4"
)

func TestSelfPasswordHandlerRejectsUntrustedOrAmbiguousBodiesWithoutEcho(t *testing.T) {
	service := &svc.ServiceContext{Config: config.Config{AllowedOrigins: "http://localhost:33000"}}
	valid := `{"current_password":"Synthetic-old-123","new_password":"Synthetic-new-456","confirm":true}`
	cases := []struct {
		name, body, origin, client, contentType string
		status                                  int
	}{
		{"origin", valid, "http://untrusted.invalid", "web", "application/json", 403},
		{"client", valid, "http://localhost:33000", "other", "application/json", 403},
		{"unknown tenant", `{"current_password":"Synthetic-old-123","new_password":"Synthetic-new-456","confirm":true,"tenant_id":"untrusted"}`, "http://localhost:33000", "web", "application/json", 400},
		{"trailing json", valid + `{}`, "http://localhost:33000", "web", "application/json", 400},
		{"body limit", `{"current_password":"` + strings.Repeat("A", 1100) + `"}`, "http://localhost:33000", "web", "application/json", 400},
		{"content type", valid, "http://localhost:33000", "web", "text/plain", 400},
		{"missing identity", valid, "http://localhost:33000", "web", "application/json", 401},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(item.body))
			request.Header.Set("Origin", item.origin)
			request.Header.Set("X-FlowDesk-Client", item.client)
			request.Header.Set("Content-Type", item.contentType)
			recorder := httptest.NewRecorder()
			ChangeOwnPasswordHandler(service)(recorder, request)
			if recorder.Code != item.status {
				t.Fatal("status", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("credential response cacheable")
			}
			if strings.Contains(recorder.Body.String(), "Synthetic-") || strings.Contains(recorder.Body.String(), "current_password") || strings.Contains(recorder.Body.String(), "new_password") {
				t.Fatal("credential echoed")
			}
		})
	}
}

func TestSelfPasswordMiddleware401IsNotCacheable(t *testing.T) {
	service := &svc.ServiceContext{Config: config.Config{AllowedOrigins: "http://localhost:33000", JwtSecret: "synthetic-test-signing-key-never-used-outside-tests"}}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "flowdesk", "aud": "flowdesk-web", "exp": time.Now().Add(-time.Minute).Unix()}).SignedString([]byte(service.Config.JwtSecret))
	if err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"", "Bearer invalid-synthetic-token", "Bearer " + expired} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(`{"current_password":"Synthetic-old-123","new_password":"Synthetic-new-456","confirm":true}`))
		request.Header.Set("Authorization", header)
		request.Header.Set("Origin", "http://localhost:33000")
		request.Header.Set("X-FlowDesk-Client", "web")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		middleware.NewSessionAuthMiddleware(service).Handle(ChangeOwnPasswordHandler(service))(recorder, request)
		if recorder.Code != http.StatusUnauthorized || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("middleware rejection must be an uncacheable 401", recorder.Code, recorder.Header())
		}
		if strings.Contains(recorder.Body.String(), "Synthetic-") || strings.Contains(recorder.Body.String(), "password") {
			t.Fatal("middleware echoed credential material")
		}
	}
}
