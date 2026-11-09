package logic

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func restoreBatchRequest(t *testing.T, s *svc.ServiceContext, ctx context.Context, runs ...types.RunData) *types.CreateRunHistoryBatchReq {
	t.Helper()
	ids := make([]string, len(runs))
	for index, run := range runs {
		ids[index] = run.Id
	}
	slices.Sort(ids)
	preview, err := PreviewRunHistoryRestoreBatch(ctx, s, &types.PreviewRunHistoryBatchReq{RunIds: ids})
	if err != nil || !preview.CanCreate {
		t.Fatal(preview, err)
	}
	req := &types.CreateRunHistoryBatchReq{Confirm: true, IdempotencyKey: uuid.NewString()}
	for _, item := range preview.Items {
		req.Items = append(req.Items, types.RunHistoryBatchSelection{RunId: item.RunId, ExpectedVersion: item.ExpectedVersion, ExpectedGeneration: item.ExpectedGeneration})
	}
	return req
}
func archiveForRestoreBatch(t *testing.T, s *svc.ServiceContext, ctx context.Context, runs ...types.RunData) []types.RunHistoryOperationData {
	t.Helper()
	results := []types.RunHistoryOperationData{}
	for _, run := range runs {
		item, err := ChangeRunHistory(ctx, s, historyRequest(run, 0), "archived")
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, item)
	}
	return results
}
func TestRunHistoryRestoreBatchDurablePartialRoundTripAndOfflineOriginalKey(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	one, two := agedHistoryRun(t, s, owner), agedHistoryRun(t, s, owner)
	beforeMessages, beforeEvents := historySnapshots(t, s, owner, one)
	calls := 0
	failNext := false
	server := historyAgent(t, s, func(w http.ResponseWriter, r *http.Request, receipt client.AgentHistoryReceipt) bool {
		calls++
		if failNext {
			failNext = false
			w.WriteHeader(503)
			return true
		}
		return false
	})
	archiveForRestoreBatch(t, s, admin, one, two)
	req := restoreBatchRequest(t, s, admin, one, two)
	receipt, err := CreateRunHistoryRestoreBatch(admin, s, req)
	if err != nil || calls != 2 {
		t.Fatal("registration must not restore Agent payloads", receipt, calls, err)
	}
	batch, err := GetRunHistoryBatch(admin, s, receipt.BatchId)
	if err != nil || batch.Target != "live" || batch.CompletedCount != 0 || len(batch.Items) != 2 {
		t.Fatal(batch, err)
	}
	messages, _, hot := historyPayloadCounts(t, s, one.Id)
	if messages != 2 || hot != 0 {
		t.Fatal("registration moved Go payloads", messages, hot)
	}
	first, second := batch.Items[0], batch.Items[1]
	batch, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: first.OperationId})
	if err != nil || batch.CompletedCount != 1 {
		t.Fatal(batch, err)
	}
	failNext = true
	_, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: second.OperationId})
	operationError(t, err, "RUN_HISTORY_UNAVAILABLE")
	other := operationActor(t, s, owner, "admin", Current(owner).User.TenantId)
	batch, err = GetRunHistoryBatch(other, s, batch.BatchId)
	if err != nil || batch.CompletedCount != 1 || batch.Items[1].OperationId != second.OperationId {
		t.Fatal("partial restoration was lost", batch, err)
	}
	batch, err = ResumeRunHistoryBatch(other, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: second.OperationId})
	if err != nil || batch.Status != "completed" || batch.CompletedCount != 2 || calls != 5 {
		t.Fatal(batch, calls, err)
	}
	if _, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: first.OperationId}); err != nil || calls != 5 {
		t.Fatal("completed restoration called Agent again", calls, err)
	}
	afterMessages, afterEvents := historySnapshots(t, s, owner, one)
	if !reflect.DeepEqual(beforeMessages, afterMessages) || !reflect.DeepEqual(beforeEvents, afterEvents) {
		t.Fatal("public payloads changed after batch restoration")
	}
	_, err = RetryRun(owner, s, retryRequest(one))
	operationError(t, err, "RUN_HISTORY_FENCED")
	if _, err = ChangeRunHistory(admin, s, historyRequest(one, 2), "archived"); err != nil {
		t.Fatal(err)
	}
	replay, err := CreateRunHistoryRestoreBatch(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("old restoration key changed batch", replay, err)
	}
	current, err := GetRunHistory(admin, s, one.Id)
	if err != nil || current.State != "archived" || current.Generation != 3 {
		t.Fatal("restoration replay undid later archival", current, err)
	}
	server.Close()
	replay, err = CreateRunHistoryRestoreBatch(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("original restoration key required Agent online", replay, err)
	}
}
func TestRunHistoryRestoreBatchAtomicAdmissionLateFailureAndDirectionConstraint(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	one, two := agedHistoryRun(t, s, owner), agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	archived := archiveForRestoreBatch(t, s, admin, one, two)
	req := restoreBatchRequest(t, s, admin, one, two)
	req.Items[1].ExpectedGeneration++
	_, err := CreateRunHistoryRestoreBatch(admin, s, req)
	operationError(t, err, "RUN_HISTORY_CHANGED")
	b, i, o := historyBatchCounts(t, s)
	if b != 0 || i != 0 || o != 2 {
		t.Fatal("late member validation left an earlier intent", b, i, o)
	}
	req.Items[1].ExpectedGeneration--
	if _, err = s.Conn.Exec("CREATE FUNCTION business.synthetic_restore_batch_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='run.history.restore_batch' THEN RAISE EXCEPTION 'synthetic restore batch crash'; END IF; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("CREATE TRIGGER synthetic_restore_batch_fail BEFORE INSERT ON business.audit_events FOR EACH ROW EXECUTE FUNCTION business.synthetic_restore_batch_fail()"); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateRunHistoryRestoreBatch(admin, s, req); err == nil {
		t.Fatal("late audit failure accepted")
	}
	b, i, o = historyBatchCounts(t, s)
	if b != 0 || i != 0 || o != 2 {
		t.Fatal("failed restore batch did not roll back all intents", b, i, o)
	}
	if _, err = s.Conn.Exec("DROP TRIGGER synthetic_restore_batch_fail ON business.audit_events"); err != nil {
		t.Fatal(err)
	}
	// A direct mixed-target manifest must also fail when the transaction commits.
	err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		tenant, actor := Current(admin).User.TenantId, Current(admin).User.Id
		operation := &model.RunHistoryOperations{Id: uuid.NewString(), TenantId: tenant, RunId: two.Id, ActorUserId: actor, Generation: 2, Target: "live", Status: "pending"}
		if err := s.RunHistoryOperationsModel.InsertTx(ctx, tx, operation); err != nil {
			return err
		}
		batch := &model.RunHistoryBatches{Id: uuid.NewString(), TenantId: tenant, ActorUserId: actor, ItemCount: 2}
		if err := s.RunHistoryBatchesModel.InsertTx(ctx, tx, batch); err != nil {
			return err
		}
		for index, operationID := range []string{archived[0].OperationId, operation.Id} {
			if err := s.RunHistoryBatchItemsModel.InsertTx(ctx, tx, &model.RunHistoryBatchItems{Id: uuid.NewString(), TenantId: tenant, BatchId: batch.Id, OperationId: operationID, Ordinal: int64(index + 1)}); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		t.Fatal("mixed-direction batch committed")
	}
	b, i, o = historyBatchCounts(t, s)
	if b != 0 || i != 0 || o != 2 {
		t.Fatal("constraint failure left partial records", b, i, o)
	}
	if _, err = CreateRunHistoryRestoreBatch(admin, s, req); err != nil {
		t.Fatal(err)
	}
}
func TestRunHistoryRestoreBatchAuthorizationPreviewAndConcurrentIdentity(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	preview, err := PreviewRunHistoryRestoreBatch(admin, s, &types.PreviewRunHistoryBatchReq{RunIds: []string{run.Id}})
	if err != nil || preview.CanCreate || preview.Items[0].CanRestore || !slices.Contains(preview.Items[0].ProtectionReasons, "RUN_HISTORY_NOT_ARCHIVED") {
		t.Fatal("live history allowed restoration", preview, err)
	}
	if _, err = PreviewRunHistoryRestoreBatch(owner, s, &types.PreviewRunHistoryBatchReq{RunIds: []string{run.Id}}); !errors.Is(err, errForbidden) {
		t.Fatal(err)
	}
	archiveForRestoreBatch(t, s, admin, run)
	newTask(t, owner, s, run.ConversationId)
	lifecycle, err := GetRunLifecycle(admin, s, run.Id)
	if err != nil || lifecycle.ArchiveCandidate || !slices.Contains(lifecycle.ProtectionReasons, "CONVERSATION_ACTIVE") {
		t.Fatal("fixture did not exercise archival-only protection", lifecycle, err)
	}
	req := restoreBatchRequest(t, s, admin, run)
	req.Confirm = false
	if _, err = CreateRunHistoryRestoreBatch(admin, s, req); !errors.Is(err, errArgument) {
		t.Fatal(err)
	}
	req.Confirm = true
	results := make(chan types.RunHistoryBatchReceipt, 2)
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() { result, err := CreateRunHistoryRestoreBatch(admin, s, req); results <- result; failures <- err })
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	batchID := ""
	for receipt := range results {
		if batchID != "" && batchID != receipt.BatchId {
			t.Fatal("duplicate restore batch")
		}
		batchID = receipt.BatchId
	}
	b, i, o := historyBatchCounts(t, s)
	if b != 1 || i != 1 || o != 2 {
		t.Fatal(b, i, o)
	}
	batch, err := GetRunHistoryBatch(admin, s, batchID)
	if err != nil || batch.Target != "live" {
		t.Fatal(batch, err)
	}
	preview, err = PreviewRunHistoryRestoreBatch(admin, s, &types.PreviewRunHistoryBatchReq{RunIds: []string{run.Id}})
	if err != nil || preview.CanCreate || !slices.Contains(preview.Items[0].ProtectionReasons, "RUN_HISTORY_PENDING") {
		t.Fatal(preview, err)
	}
	_, err = CreateRunHistoryBatch(admin, s, req)
	operationError(t, err, "RUN_HISTORY_PENDING")
	req.Items[0].ExpectedGeneration++
	_, err = CreateRunHistoryRestoreBatch(admin, s, req)
	operationError(t, err, "IDEMPOTENCY_CONFLICT")
}
func TestRunHistoryRestoreBatchPostIORevocationAndOtherAdministratorRecovery(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	archiveForRestoreBatch(t, s, admin, run)
	receipt, err := CreateRunHistoryRestoreBatch(admin, s, restoreBatchRequest(t, s, admin, run))
	if err != nil {
		t.Fatal(err)
	}
	batch, err := GetRunHistoryBatch(admin, s, receipt.BatchId)
	if err != nil {
		t.Fatal(err)
	}
	other := operationActor(t, s, owner, "admin", Current(owner).User.TenantId)
	historyAgent(t, s, func(w http.ResponseWriter, r *http.Request, receipt client.AgentHistoryReceipt) bool {
		if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
			t.Error(err)
		}
		return false
	})
	result, err := ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: batch.Items[0].OperationId})
	if !errors.Is(err, ErrUnauthorized) || len(result.Items) != 0 {
		t.Fatal("revoked actor saw partial restore results", result, err)
	}
	current, err := GetRunHistoryBatch(other, s, batch.BatchId)
	if err != nil || current.CompletedCount != 0 {
		t.Fatal(current, err)
	}
	if _, err = ResumeRunHistoryBatch(other, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: batch.Items[0].OperationId}); err != nil {
		t.Fatal(err)
	}
	current, err = GetRunHistoryBatch(other, s, batch.BatchId)
	if err != nil || current.CompletedCount != 1 || current.Target != "live" {
		t.Fatal(current, err)
	}
}
