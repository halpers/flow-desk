package logic

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func historyBatchRequest(t *testing.T, s *svc.ServiceContext, admin context.Context, runs ...types.RunData) *types.CreateRunHistoryBatchReq {
	t.Helper()
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.Id
	}
	slices.Sort(ids)
	preview, err := PreviewRunHistoryBatch(admin, s, &types.PreviewRunHistoryBatchReq{RunIds: ids})
	if err != nil || !preview.CanCreate {
		t.Fatal(preview, err)
	}
	req := &types.CreateRunHistoryBatchReq{Confirm: true, IdempotencyKey: uuid.NewString()}
	for _, item := range preview.Items {
		req.Items = append(req.Items, types.RunHistoryBatchSelection{RunId: item.RunId, ExpectedVersion: item.ExpectedVersion, ExpectedGeneration: item.ExpectedGeneration})
	}
	return req
}
func historyBatchCounts(t *testing.T, s *svc.ServiceContext) (int64, int64, int64) {
	t.Helper()
	var row struct {
		Batches    int64 `db:"batches"`
		Items      int64 `db:"items"`
		Operations int64 `db:"operations"`
	}
	if err := s.Conn.QueryRow(&row, "select (select count(*) from business.run_history_batches) batches,(select count(*) from business.run_history_batch_items) items,(select count(*) from business.run_history_operations) operations"); err != nil {
		t.Fatal(err)
	}
	return row.Batches, row.Items, row.Operations
}
func TestRunHistoryBatchDurablePartialCompletionRecoveryAndOriginalReceipt(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	one := agedHistoryRun(t, s, owner)
	two := agedHistoryRun(t, s, owner)
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
	req := historyBatchRequest(t, s, admin, one, two)
	receipt, err := CreateRunHistoryBatch(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("registering a batch called Agent maintenance")
	}
	batch, err := GetRunHistoryBatch(admin, s, receipt.BatchId)
	if err != nil || len(batch.Items) != 2 || batch.CompletedCount != 0 || batch.Status != "pending" {
		t.Fatal(batch, err)
	}
	ids := []string{batch.Items[0].OperationId, batch.Items[1].OperationId}
	batch, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: ids[0]})
	if err != nil || batch.CompletedCount != 1 {
		t.Fatal(batch, err)
	}
	failNext = true
	_, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: ids[1]})
	operationError(t, err, "RUN_HISTORY_UNAVAILABLE")
	batch, err = GetRunHistoryBatch(admin, s, batch.BatchId)
	if err != nil || batch.CompletedCount != 1 || batch.Items[1].OperationId != ids[1] {
		t.Fatal("partial completion lost", batch, err)
	}
	other := operationActor(t, s, owner, "admin", Current(owner).User.TenantId)
	batch, err = ResumeRunHistoryBatch(other, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: ids[1]})
	if err != nil || batch.CompletedCount != 2 || batch.Status != "completed" {
		t.Fatal(batch, err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if _, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: batch.BatchId, OperationId: ids[0]}); err != nil || calls != 3 {
		t.Fatal("completed child was called again", calls, err)
	}
	target := one
	if batch.Items[0].RunId == two.Id {
		target = two
	}
	if _, err = ChangeRunHistory(admin, s, historyRequest(target, 1), "live"); err != nil {
		t.Fatal(err)
	}
	replay, err := CreateRunHistoryBatch(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("old key changed original batch", replay, err)
	}
	batch, err = GetRunHistoryBatch(admin, s, batch.BatchId)
	if err != nil || batch.CompletedCount != 2 || batch.Items[0].OperationId != ids[0] {
		t.Fatal("restore changed old receipt", batch, err)
	}
	state, err := GetRunHistory(admin, s, target.Id)
	if err != nil || state.State != "live" {
		t.Fatal("replay rearchived task", state, err)
	}
	server.Close()
	replay, err = CreateRunHistoryBatch(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("committed key recovery depended on Agent", replay, err)
	}
	b, i, o := historyBatchCounts(t, s)
	if b != 1 || i != 2 || o != 3 {
		t.Fatal(b, i, o)
	}
}
func TestRunHistoryBatchAdmissionAndLateFailureRollBackEveryIntent(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	one := agedHistoryRun(t, s, owner)
	two := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	req := historyBatchRequest(t, s, admin, one, two)
	if _, err := s.Conn.Exec("update business.runs set updated_at=now() where id=$1", two.Id); err != nil {
		t.Fatal(err)
	}
	_, err := CreateRunHistoryBatch(admin, s, req)
	operationError(t, err, "RUN_HISTORY_PROTECTED")
	b, i, o := historyBatchCounts(t, s)
	if b+i+o != 0 {
		t.Fatal("partial admission", b, i, o)
	}
	if _, err = s.Conn.Exec("update business.runs set updated_at=now()-interval '31 days' where id=$1", two.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("CREATE FUNCTION business.synthetic_batch_fail() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='run.history.batch' THEN RAISE EXCEPTION 'synthetic batch crash'; END IF; RETURN NEW; END $$"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("CREATE TRIGGER synthetic_batch_fail BEFORE INSERT ON business.audit_events FOR EACH ROW EXECUTE FUNCTION business.synthetic_batch_fail()"); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateRunHistoryBatch(admin, s, req); err == nil {
		t.Fatal("late audit failure accepted")
	}
	b, i, o = historyBatchCounts(t, s)
	if b+i+o != 0 {
		t.Fatal("late failure left permanent fences", b, i, o)
	}
	if _, err = s.Conn.Exec("DROP TRIGGER synthetic_batch_fail ON business.audit_events"); err != nil {
		t.Fatal(err)
	}
	receipt, err := CreateRunHistoryBatch(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.run_history_batches set item_count=1 where id=$1", receipt.BatchId); err == nil {
		t.Fatal("batch definition changed")
	}
	if _, err = s.Conn.Exec("delete from business.run_history_batch_items where batch_id=$1", receipt.BatchId); err == nil {
		t.Fatal("batch membership erased")
	}
	if _, err = s.Conn.Exec("insert into business.run_history_batches(id,tenant_id,actor_user_id,item_count)values($1,$2,$3,1)", uuid.NewString(), Current(admin).User.TenantId, Current(admin).User.Id); err == nil {
		t.Fatal("incomplete batch committed")
	}
}
func TestRunHistoryBatchBoundsAuthorizationAndMemberBinding(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	tenant := uuid.NewString()
	if _, err := s.TenantsModel.Insert(owner, &model.Tenants{Id: tenant, Code: uuid.NewString(), Name: "Synthetic foreign"}); err != nil {
		t.Fatal(err)
	}
	foreign := operationActor(t, s, owner, "admin", tenant)
	run := agedHistoryRun(t, s, owner)
	foreignRun := agedHistoryRun(t, s, foreign)
	historyAgent(t, s, nil)
	for _, ids := range [][]string{nil, {run.Id, run.Id}, {run.Id, foreignRun.Id, run.Id}, slices.Repeat([]string{run.Id}, 11)} {
		if _, err := PreviewRunHistoryBatch(admin, s, &types.PreviewRunHistoryBatchReq{RunIds: ids}); err == nil {
			t.Fatal("invalid selection accepted", ids)
		}
	}
	ids := []string{run.Id, foreignRun.Id}
	slices.Sort(ids)
	preview, err := PreviewRunHistoryBatch(admin, s, &types.PreviewRunHistoryBatchReq{RunIds: ids})
	if err == nil || len(preview.Items) != 0 {
		t.Fatal("partial unauthorized preview", preview, err)
	}
	if _, err = PreviewRunHistoryBatch(owner, s, &types.PreviewRunHistoryBatchReq{RunIds: []string{run.Id}}); !errors.Is(err, errForbidden) {
		t.Fatal(err)
	}
	req := historyBatchRequest(t, s, admin, run)
	req.Confirm = false
	if _, err = CreateRunHistoryBatch(admin, s, req); !errors.Is(err, errArgument) {
		t.Fatal(err)
	}
	req.Confirm = true
	receipt, err := CreateRunHistoryBatch(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = GetRunHistoryBatch(owner, s, receipt.BatchId); !errors.Is(err, errForbidden) {
		t.Fatal(err)
	}
	if _, err = GetRunHistoryBatch(foreign, s, receipt.BatchId); err == nil {
		t.Fatal("cross-tenant batch exposed")
	}
	if _, err = ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: receipt.BatchId, OperationId: uuid.NewString()}); !errors.Is(err, errMissing) {
		t.Fatal("foreign operation accepted", err)
	}
	req.Items[0].ExpectedGeneration++
	_, err = CreateRunHistoryBatch(admin, s, req)
	operationError(t, err, "IDEMPOTENCY_CONFLICT")
}
func TestRunHistoryBatchConcurrentRegistrationHasOneDurableManifest(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	run := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	req := historyBatchRequest(t, s, admin, run)
	results := make(chan types.RunHistoryBatchReceipt, 2)
	failures := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() { result, err := CreateRunHistoryBatch(admin, s, req); results <- result; failures <- err })
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for result := range results {
		if id != "" && id != result.BatchId {
			t.Fatal("two manifests")
		}
		id = result.BatchId
	}
	b, i, o := historyBatchCounts(t, s)
	if b != 1 || i != 1 || o != 1 {
		t.Fatal(b, i, o)
	}
	req.IdempotencyKey = uuid.NewString()
	_, err := CreateRunHistoryBatch(admin, s, req)
	operationError(t, err, "RUN_HISTORY_PENDING")
}
func TestRunHistoryBatchPostIORevocationAndScopedPagination(t *testing.T) {
	s := testService(t)
	owner, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, owner)
	one := agedHistoryRun(t, s, owner)
	two := agedHistoryRun(t, s, owner)
	historyAgent(t, s, nil)
	first, err := CreateRunHistoryBatch(admin, s, historyBatchRequest(t, s, admin, one))
	if err != nil {
		t.Fatal(err)
	}
	second, err := CreateRunHistoryBatch(admin, s, historyBatchRequest(t, s, admin, two))
	if err != nil {
		t.Fatal(err)
	}
	page, err := ListRunHistoryBatches(admin, s, &types.ListRunHistoryBatchesReq{Limit: 1})
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.Items[0].BatchId != second.BatchId {
		t.Fatal(page, err)
	}
	next, err := ListRunHistoryBatches(admin, s, &types.ListRunHistoryBatchesReq{Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.HasMore || next.Items[0].BatchId != first.BatchId {
		t.Fatal(next, err)
	}
	other := operationActor(t, s, owner, "admin", Current(owner).User.TenantId)
	if _, err = ListRunHistoryBatches(other, s, &types.ListRunHistoryBatchesReq{Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor crossed actor scope")
	}
	before, err := GetRunHistoryBatch(admin, s, first.BatchId)
	if err != nil {
		t.Fatal(err)
	}
	historyAgent(t, s, func(w http.ResponseWriter, r *http.Request, receipt client.AgentHistoryReceipt) bool {
		if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
			t.Error(err)
		}
		return false
	})
	result, err := ResumeRunHistoryBatch(admin, s, &types.ResumeRunHistoryBatchReq{BatchId: first.BatchId, OperationId: before.Items[0].OperationId})
	if !errors.Is(err, ErrUnauthorized) || len(result.Items) != 0 {
		t.Fatal("revoked caller received batch", result, err)
	}
	pending, err := GetRunHistoryBatch(other, s, first.BatchId)
	if err != nil || pending.CompletedCount != 0 {
		t.Fatal(pending, err)
	}
	if _, err = ResumeRunHistoryBatch(other, s, &types.ResumeRunHistoryBatchReq{BatchId: first.BatchId, OperationId: before.Items[0].OperationId}); err != nil {
		t.Fatal(err)
	}
}
