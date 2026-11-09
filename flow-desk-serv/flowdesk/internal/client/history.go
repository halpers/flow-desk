package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"flowdesk/internal/types"
	"go.opentelemetry.io/otel/propagation"
)

const maximumHistoryReceiptBytes int64 = 16 * 1024
const maximumHistoryReceiptRows int64 = 100000

type AgentHistoryReceipt struct {
	OperationId          string `json:"operation_id"`
	RunId                string `json:"run_id"`
	TenantId             string `json:"tenant_id"`
	Generation           int64  `json:"generation"`
	Target               string `json:"target"`
	CheckpointCount      int64  `json:"checkpoint_count"`
	CheckpointBlobCount  int64  `json:"checkpoint_blob_count"`
	CheckpointWriteCount int64  `json:"checkpoint_write_count"`
}

func (c *AgentClient) History(ctx context.Context, tenant string, operation types.RunHistoryOperationData) (AgentHistoryReceipt, error) {
	var result AgentHistoryReceipt
	raw, err := json.Marshal(map[string]any{"operation_id": operation.OperationId, "tenant_id": tenant, "generation": operation.Generation, "target": operation.Target})
	if err != nil {
		return result, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/v1/runs/"+operation.RunId+"/history", bytes.NewReader(raw))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := c.http.Do(request)
	if err != nil {
		return result, errors.New("history unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return result, errors.New("history rejected")
	}
	raw, err = io.ReadAll(io.LimitReader(response.Body, maximumHistoryReceiptBytes+1))
	if err != nil || int64(len(raw)) > maximumHistoryReceiptBytes {
		return result, errors.New("history limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil {
		return result, errors.New("history invalid")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return result, errors.New("history trailing")
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != 8 {
		return result, errors.New("history fields")
	}
	for _, value := range members {
		if bytes.Equal(value, []byte("null")) {
			return result, errors.New("history null")
		}
	}
	if result.OperationId != operation.OperationId || result.RunId != operation.RunId || result.TenantId != tenant || result.Generation != operation.Generation || result.Target != operation.Target {
		return result, errors.New("history mismatch")
	}
	for _, count := range []int64{result.CheckpointCount, result.CheckpointBlobCount, result.CheckpointWriteCount} {
		if count < 0 || count > maximumHistoryReceiptRows {
			return result, errors.New("history counts")
		}
	}
	if result.CheckpointCount+result.CheckpointBlobCount+result.CheckpointWriteCount > maximumHistoryReceiptRows {
		return result, errors.New("history total counts")
	}
	return result, nil
}
