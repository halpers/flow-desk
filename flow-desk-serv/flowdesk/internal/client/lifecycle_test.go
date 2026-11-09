package client

import (
	"encoding/json"
	"testing"
	"time"

	"flowdesk/internal/types"
)

func TestLifecycleStrictProtocolRejectsMisleadingCountsAndFlags(t *testing.T) {
	baseline := types.AgentLifecycleData{Available: true, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), ObservationOnly: true, ProtectionReasons: []string{}}
	raw, _ := json.Marshal(baseline)
	if _, err := decodeLifecycle(raw); err != nil {
		t.Fatal("valid response rejected", err)
	}
	cases := []struct {
		Name   string
		Change func(map[string]any)
	}{
		{"missing-field", func(m map[string]any) { delete(m, "checkpoint_count") }},
		{"unknown-field", func(m map[string]any) { m["private_state"] = "sensitive" }},
		{"negative", func(m map[string]any) { m["checkpoint_count"] = -1 }},
		{"unsafe-integer", func(m map[string]any) { m["checkpoint_count"] = int64(9007199254740992) }},
		{"null", func(m map[string]any) { m["available"] = nil }},
		{"null-reasons", func(m map[string]any) { m["protection_reasons"] = nil }},
		{"not-observation", func(m map[string]any) { m["observation_only"] = false }},
		{"invalid-time", func(m map[string]any) { m["observed_at"] = "invalid" }},
		{"queued-without-reason", func(m map[string]any) { m["command_count"] = 1; m["queued_command_count"] = 1 }},
		{"reason-without-queued", func(m map[string]any) { m["protection_reasons"] = []string{"COMMAND_QUEUED"} }},
		{"unknown-reason", func(m map[string]any) { m["protection_reasons"] = []string{"DELETE_ALLOWED"} }},
		{"unavailable-count", func(m map[string]any) {
			m["available"] = false
			m["checkpoint_count"] = 1
			m["protection_reasons"] = []string{"AGENT_SCOPE_UNAVAILABLE"}
		}},
		{"unavailable-without-reason", func(m map[string]any) { m["available"] = false }},
		{"inconsistent-count", func(m map[string]any) {
			m["command_count"] = 0
			m["running_command_count"] = 1
			m["protection_reasons"] = []string{"COMMAND_RUNNING"}
		}},
	}
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			var data map[string]any
			_ = json.Unmarshal(raw, &data)
			test.Change(data)
			changed, _ := json.Marshal(data)
			if _, err := decodeLifecycle(changed); err == nil {
				t.Fatal("malformed Agent lifecycle accepted")
			}
		})
	}
	for _, suffix := range []string{"{}", "true", " trailing"} {
		if _, err := decodeLifecycle(append(raw, []byte(suffix)...)); err == nil {
			t.Fatal("trailing response accepted")
		}
	}
	baseline.Available = false
	baseline.ProtectionReasons = []string{"AGENT_SCOPE_UNAVAILABLE"}
	raw, _ = json.Marshal(baseline)
	if _, err := decodeLifecycle(raw); err != nil {
		t.Fatal("unbound observation rejected", err)
	}
}
