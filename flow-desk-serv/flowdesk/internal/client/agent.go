package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"flowdesk/internal/types"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

const dispatchTimeout = 5 * time.Second

const maxObservationBytes = 256 * 1024

func (c *AgentClient) Observation(ctx context.Context, tenant, run string) (types.AgentTraceData, error) {
	var data types.AgentTraceData
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/runs/"+run+"/trace?tenant_id="+tenant, nil)
	if err != nil {
		return data, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := c.http.Do(request)
	if err != nil {
		return data, errors.New("observation unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return data, errors.New("observation rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxObservationBytes+1))
	if err != nil || len(raw) > maxObservationBytes {
		return data, errors.New("observation limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&data); err != nil {
		return types.AgentTraceData{}, errors.New("observation shape")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return types.AgentTraceData{}, errors.New("observation trailing data")
	}
	if data.Nodes == nil || data.Models == nil || data.Tools == nil || len(data.Nodes) > 100 || len(data.Models) > 100 || len(data.Tools) > 100 || data.Usage.CostStatus != "unknown" {
		return types.AgentTraceData{}, errors.New("observation invalid")
	}
	return data, nil
}

type AgentClient struct {
	baseURL, token string
	http           *http.Client
}

func NewAgentClient(baseURL, token string) *AgentClient {
	return &AgentClient{strings.TrimRight(baseURL, "/"), token, &http.Client{Timeout: dispatchTimeout}}
}
func (c *AgentClient) Dispatch(ctx context.Context, payload string) error {
	return c.dispatch(ctx, "/internal/v1/executions", payload)
}
func (c *AgentClient) DispatchKnowledge(ctx context.Context, payload string) error {
	return c.dispatch(ctx, "/internal/v1/knowledge/index", payload)
}
func (c *AgentClient) CleanupKnowledge(ctx context.Context, payload string) error {
	return c.dispatch(ctx, "/internal/v1/knowledge/cleanup", payload)
}
func (c *AgentClient) Cancel(ctx context.Context, run, payload string) error {
	return c.dispatch(ctx, "/internal/v1/executions/"+run+"/cancel", payload)
}
func (c *AgentClient) dispatch(ctx context.Context, path, payload string) error {
	var metadata struct {
		RunID string `json:"run_id"`
	}
	_ = json.Unmarshal([]byte(payload), &metadata)
	ctx, span := otel.Tracer("flowdesk.scheduler").Start(ctx, "agent.dispatch")
	defer span.End()
	if len(metadata.RunID) == 36 {
		span.SetAttributes(attribute.String("flowdesk.run_id", metadata.RunID))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewBufferString(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := c.http.Do(request)
	if err != nil {
		return errors.New("agent dispatch unavailable")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted {
		return errors.New("agent dispatch rejected")
	}
	return nil
}
