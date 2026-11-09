package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"time"

	"flowdesk/internal/types"
	"go.opentelemetry.io/otel/propagation"
)

const maxLifecycleBytes = 16 * 1024
const maxLifecycleCount int64 = 9007199254740991

func (c *AgentClient) Lifecycle(ctx context.Context, tenant, run string) (types.AgentLifecycleData, error) {
	var data types.AgentLifecycleData
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/runs/"+run+"/lifecycle?tenant_id="+tenant, nil)
	if err != nil {
		return data, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := c.http.Do(request)
	if err != nil {
		return data, errors.New("lifecycle unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return data, errors.New("lifecycle rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxLifecycleBytes+1))
	if err != nil || len(raw) > maxLifecycleBytes {
		return data, errors.New("lifecycle limit")
	}
	return decodeLifecycle(raw)
}

func decodeLifecycle(raw []byte) (types.AgentLifecycleData, error) {
	var data types.AgentLifecycleData
	invalid := errors.New("lifecycle invalid")
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return data, invalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return data, invalid
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		return data, invalid
	}
	value := reflect.ValueOf(data)
	metadata := value.Type()
	if len(members) != metadata.NumField() {
		return data, invalid
	}
	nonzero := false
	for index := range value.NumField() {
		name := metadata.Field(index).Tag.Get("json")
		member, ok := members[name]
		if !ok || bytes.Equal(bytes.TrimSpace(member), []byte("null")) {
			return data, invalid
		}
		if value.Field(index).Kind() == reflect.Int64 {
			count := value.Field(index).Int()
			if count < 0 || count > maxLifecycleCount {
				return data, invalid
			}
			nonzero = nonzero || count > 0
		}
	}
	if !data.ObservationOnly || data.ProtectionReasons == nil {
		return data, invalid
	}
	if _, err := time.Parse(time.RFC3339Nano, data.ObservedAt); err != nil {
		return data, invalid
	}
	if !data.Available {
		if nonzero || !slices.Equal(data.ProtectionReasons, []string{"AGENT_SCOPE_UNAVAILABLE"}) {
			return data, invalid
		}
		return data, nil
	}
	expected := []string{}
	checks := []struct {
		Count  int64
		Reason string
	}{
		{data.QueuedCommandCount, "COMMAND_QUEUED"}, {data.RunningCommandCount, "COMMAND_RUNNING"},
		{data.WaitingCommandCount, "COMMAND_WAITING"}, {data.LiveCommandLeaseCount, "COMMAND_LEASE_ACTIVE"},
		{data.UnacknowledgedCallbackCount, "CALLBACK_UNACKNOWLEDGED"}, {data.LiveCallbackLeaseCount, "CALLBACK_LEASE_ACTIVE"},
		{data.ReservedModelCallCount, "MODEL_RESERVED"},
	}
	for _, check := range checks {
		if check.Count > 0 {
			expected = append(expected, check.Reason)
		}
	}
	if slices.Contains(data.ProtectionReasons, "MODEL_STATE_UNKNOWN") {
		expected = append(expected, "MODEL_STATE_UNKNOWN")
	}
	if data.ReservedEmbeddingCallCount > 0 {
		expected = append(expected, "EMBEDDING_RESERVED")
	}
	if !slices.Equal(expected, data.ProtectionReasons) {
		return data, invalid
	}
	if data.QueuedCommandCount+data.RunningCommandCount+data.WaitingCommandCount > data.CommandCount || data.LiveCommandLeaseCount > data.CommandCount || data.UnacknowledgedCallbackCount > data.CallbackCount || data.LiveCallbackLeaseCount > data.CallbackCount || data.ReservedModelCallCount > data.ModelCallCount || data.ReservedEmbeddingCallCount > data.EmbeddingCallCount {
		return data, invalid
	}
	return data, nil
}
