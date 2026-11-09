package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func TestHistoryClientRequiresExactReceiptAndTrustedOperation(t *testing.T) {
	tenant := uuid.NewString()
	operation := types.RunHistoryOperationData{OperationId: uuid.NewString(), RunId: uuid.NewString(), Generation: 1, Target: "archived"}
	for _, mode := range []string{"valid", "foreign", "missing", "negative", "extra", "null", "boolean", "trailing"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer synthetic" || r.URL.Path != "/internal/v1/runs/"+operation.RunId+"/history" {
					t.Error("untrusted maintenance envelope")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request["tenant_id"] != tenant || request["operation_id"] != operation.OperationId || request["target"] != "archived" || len(request) != 4 {
					t.Error("incorrect trusted command")
				}
				receipt := map[string]any{"operation_id": operation.OperationId, "run_id": operation.RunId, "tenant_id": tenant, "generation": 1, "target": "archived", "checkpoint_count": 2, "checkpoint_blob_count": 3, "checkpoint_write_count": 1}
				switch mode {
				case "foreign":
					receipt["tenant_id"] = uuid.NewString()
				case "missing":
					delete(receipt, "checkpoint_count")
				case "negative":
					receipt["checkpoint_count"] = -1
				case "extra":
					receipt["checkpoint"] = map[string]string{"private": "must not return"}
				case "null":
					receipt["checkpoint_count"] = nil
				case "boolean":
					receipt["generation"] = true
				}
				_ = json.NewEncoder(w).Encode(receipt)
				if mode == "trailing" {
					_, _ = w.Write([]byte("{}"))
				}
			}))
			defer server.Close()
			result, err := NewAgentClient(server.URL, "synthetic").History(context.Background(), tenant, operation)
			if mode == "valid" {
				if err != nil || result.CheckpointBlobCount != 3 {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("invalid receipt accepted", mode)
			}
		})
	}
}
