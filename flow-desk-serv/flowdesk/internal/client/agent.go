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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
)

const dispatchTimeout = 5 * time.Second

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
