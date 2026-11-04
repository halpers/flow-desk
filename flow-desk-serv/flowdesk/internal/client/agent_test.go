package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestObservationRejectsPayloadLeaksAndUnboundedResponses(t *testing.T) {
	valid := `{"available":false,"truncated":false,"nodes":[],"models":[],"tools":[],"usage":{"cost_status":"unknown"}}`
	for _, item := range []struct {
		name, body string
		status     int
		valid      bool
	}{
		{"valid", valid, 200, true},
		{"unknown private field", strings.TrimSuffix(valid, "}") + `,"checkpoint":"PRIVATE_STATE"}`, 200, false},
		{"trailing payload", valid + valid, 200, false},
		{"oversized", strings.Repeat(" ", maxObservationBytes+1) + valid, 200, false},
		{"upstream unavailable", "PRIVATE_ERROR", 503, false},
		{"incomplete", "{}", 200, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(item.status)
				_, _ = w.Write([]byte(item.body))
			}))
			defer server.Close()
			_, err := NewAgentClient(server.URL, "test-token").Observation(context.Background(), "tenant", "run")
			if (err == nil) != item.valid {
				t.Fatal("unexpected validation", err)
			}
			if err != nil && strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("upstream body leaked")
			}
		})
	}
}
