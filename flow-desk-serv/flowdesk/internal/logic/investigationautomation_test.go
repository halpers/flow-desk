package logic

import (
	"context"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"reflect"
	"sync"
	"testing"
	"time"
)

func automationRequest(revision int64, enabled bool) *types.UpdateInvestigationAutomationReq {
	return &types.UpdateInvestigationAutomationReq{ExpectedRevision: revision, Enabled: enabled, IdleDays: 7, IntervalMinutes: 5, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func automationFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, string) {
	t.Helper()
	s, admin, operator, version, store := tenantInventoryFixture(t)
	ref := HashToken(store.ID() + "\n" + sourceKey(version))
	req := investigationRequest(t, s, admin, ref)
	if _, err := SaveSourceInvestigation(admin, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.Exec(`update business.source_investigations set revision=revision+1,updated_at=clock_timestamp()-interval '8 days' where tenant_id=$1 and object_ref=$2`, version.TenantId, ref); err != nil {
		t.Fatal(err)
	}
	return s, admin, operator, version, ref
}
func automationDue(t *testing.T, s *svc.ServiceContext, tenant string) {
	t.Helper()
	if _, err := s.Conn.Exec(`update business.investigation_automation_policies set next_run_at=clock_timestamp()-interval '1 second' where tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
}
func automationDispatch(t *testing.T, s *svc.ServiceContext) {
	t.Helper()
	found, err := DispatchInvestigationAutomation(context.Background(), s)
	if err != nil || !found {
		t.Fatal(found, err)
	}
}
func automationEnable(t *testing.T, s *svc.ServiceContext, admin context.Context) {
	t.Helper()
	s.Config.InvestigationAutomationEnabled = true
	if _, err := UpdateInvestigationAutomation(admin, s, automationRequest(0, true)); err != nil {
		t.Fatal(err)
	}
	automationDue(t, s, Current(admin).User.TenantId)
}
func automationCheck(t *testing.T, s *svc.ServiceContext, tenant, ref, status string, revision, effects int64) {
	t.Helper()
	row, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRef(context.Background(), tenant, ref)
	if err != nil || row.Status != status || row.Revision != revision || row.Note != "Synthetic investigation note" {
		t.Fatal("current investigation", row, err)
	}
	for query, want := range map[string]int64{`select count(*) from business.source_investigation_revisions where investigation_id=$1`: revision, `select count(*) from business.audit_events where object_id=$1 and operation='knowledge.investigation_auto_return'`: effects} {
		var count int64
		if err = s.Conn.QueryRow(&count, query, row.Id); err != nil || count != want {
			t.Fatal(query, count, want, err)
		}
	}
}
func TestInvestigationAutomationPolicyConfirmationReplayAndIsolation(t *testing.T) {
	s, admin, operator, version, ref := automationFixture(t)
	initial, err := GetInvestigationAutomation(admin, s)
	if err != nil || initial.Enabled || initial.Revision != 0 || initial.IdleDays != 7 || initial.RuntimeEnabled {
		t.Fatal(initial, err)
	}
	_, err = GetInvestigationAutomation(operator, s)
	operationError(t, err, "PERMISSION_DENIED")
	for _, change := range []func(*types.UpdateInvestigationAutomationReq){func(r *types.UpdateInvestigationAutomationReq) { r.Confirm = false }, func(r *types.UpdateInvestigationAutomationReq) { r.IdleDays = 0 }, func(r *types.UpdateInvestigationAutomationReq) { r.IdleDays = 3651 }, func(r *types.UpdateInvestigationAutomationReq) { r.IntervalMinutes = 4 }, func(r *types.UpdateInvestigationAutomationReq) { r.IntervalMinutes = 10081 }, func(r *types.UpdateInvestigationAutomationReq) { r.ExpectedRevision = -1 }} {
		r := automationRequest(0, true)
		change(r)
		_, err = UpdateInvestigationAutomation(admin, s, r)
		operationError(t, err, "INVALID_ARGUMENT")
	}
	request := automationRequest(0, true)
	first, err := UpdateInvestigationAutomation(admin, s, request)
	if err != nil || first.Revision != 1 || !first.AuthorizationValid {
		t.Fatal(first, err)
	}
	automationDue(t, s, version.TenantId)
	found, err := DispatchInvestigationAutomation(context.Background(), s)
	if err != nil || found {
		t.Fatal("global gate", found, err)
	}
	disabled, err := UpdateInvestigationAutomation(admin, s, automationRequest(1, false))
	if err != nil || disabled.Enabled || disabled.Revision != 2 {
		t.Fatal(disabled, err)
	}
	replay, err := UpdateInvestigationAutomation(admin, s, request)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatal("original receipt", replay, err)
	}
	current, err := GetInvestigationAutomation(admin, s)
	if err != nil || current.Enabled || current.Revision != 2 {
		t.Fatal(current, err)
	}
	request.IdleDays = 8
	_, err = UpdateInvestigationAutomation(admin, s, request)
	operationError(t, err, "IDEMPOTENCY_CONFLICT")
	_, err = UpdateInvestigationAutomation(admin, s, automationRequest(1, true))
	operationError(t, err, "INVESTIGATION_AUTOMATION_CHANGED")
	s.Config.InvestigationAutomationEnabled = true
	automationDue(t, s, version.TenantId)
	found, err = DispatchInvestigationAutomation(context.Background(), s)
	if err != nil || found {
		t.Fatal("tenant gate", found, err)
	}
	foreignTenant := uuid.NewString()
	if _, err = s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: foreignTenant, Code: foreignTenant, Name: "Synthetic automation foreign"}); err != nil {
		t.Fatal(err)
	}
	foreign := scheduleOtherAdmin(t, s, admin, foreignTenant)
	other, err := GetInvestigationAutomation(foreign, s)
	if err != nil || other.Revision != 0 || other.OwnerUserId != "" || other.LastObjectRef != "" {
		t.Fatal("policy cross-tenant", other, err)
	}
	automationCheck(t, s, version.TenantId, ref, investigationActive, 2, 0)
}
func TestInvestigationAutomationConcurrentWorkersHistoryAndRestart(t *testing.T) {
	s, admin, _, version, ref := automationFixture(t)
	automationEnable(t, s, admin)
	var group sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		group.Go(func() { _, err := DispatchInvestigationAutomation(context.Background(), s); results <- err })
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	automationCheck(t, s, version.TenantId, ref, investigationOpen, 3, 1)
	policy, err := GetInvestigationAutomation(admin, s)
	if err != nil || policy.LastOutcome != "returned" || policy.LastObjectRef != ref || policy.LastRevision != 3 {
		t.Fatal(policy, err)
	}
	history, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || len(history.Items) != 3 || history.Items[0].Status != investigationOpen || history.Items[1].Status != investigationActive || history.Items[0].Note != history.Items[1].Note {
		t.Fatal(history, err)
	}
	restarted := automationRestart(s)
	found, err := DispatchInvestigationAutomation(context.Background(), restarted)
	if err != nil || found {
		t.Fatal("persistent next time", found, err)
	}
	for range 2 {
		automationDue(t, restarted, version.TenantId)
		automationDispatch(t, restarted)
	}
	automationCheck(t, s, version.TenantId, ref, investigationOpen, 3, 1)
	// Ordinary logout is not a revocation of an explicit persistent grant.
	if _, err = s.Conn.Exec(`update business.sessions set revoked_at=now() where user_id=$1`, Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec(`update business.source_investigations set status='investigating',revision=revision+1,updated_at=clock_timestamp()-interval '8 days' where tenant_id=$1 and object_ref=$2`, version.TenantId, ref); err != nil {
		t.Fatal(err)
	}
	automationDue(t, s, version.TenantId)
	automationDispatch(t, s)
	automationCheck(t, s, version.TenantId, ref, investigationOpen, 5, 2)
}
func TestInvestigationAutomationOwnerAndResourceAuthorization(t *testing.T) {
	for _, scenario := range []string{"credential_version", "disabled", "role", "acl"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, version, ref := automationFixture(t)
			automationEnable(t, s, admin)
			switch scenario {
			case "credential_version":
				_, _ = s.Conn.Exec(`update business.users set auth_version=auth_version+1 where id=$1`, Current(admin).User.Id)
			case "disabled":
				_, _ = s.Conn.Exec(`update business.users set enabled=false where id=$1`, Current(admin).User.Id)
			case "role":
				_, _ = s.Conn.Exec(`update business.users set role='operator' where id=$1`, Current(admin).User.Id)
			case "acl":
				_, _ = s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, version.TenantId)
			}
			automationDispatch(t, s)
			automationCheck(t, s, version.TenantId, ref, investigationActive, 2, 0)
			row, err := s.InvestigationAutomationPoliciesModel.FindOne(context.Background(), version.TenantId)
			want := "authorization_required"
			if scenario == "acl" {
				want = "skipped"
			}
			if err != nil || row.LastOutcome != want {
				t.Fatal(row, err)
			}
		})
	}
}

type automationCandidateHook struct {
	model.SourceInvestigationsModel
	hook func(*model.SourceInvestigations)
}

func (m automationCandidateHook) FindAutomationCandidateTx(ctx context.Context, tx sqlx.Session, tenant, after string, days int64) (*model.SourceInvestigations, error) {
	row, err := m.SourceInvestigationsModel.FindAutomationCandidateTx(ctx, tx, tenant, after, days)
	if err == nil {
		m.hook(row)
	}
	return row, err
}

type automationAuditFault struct {
	model.AuditEventsModel
	hook func()
}

func (m automationAuditFault) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if err := m.AuditEventsModel.InsertTx(ctx, tx, row); err != nil {
		return err
	}
	if row.Operation == "knowledge.investigation_auto_return" {
		if m.hook != nil {
			m.hook()
			return nil
		}
		return errors.New("synthetic late audit failure")
	}
	return nil
}

type automationCheckedFault struct {
	model.InvestigationAutomationPoliciesModel
}

func (m automationCheckedFault) UpdateCheckedTx(ctx context.Context, tx sqlx.Session, row *model.InvestigationAutomationPolicies, outcome string) error {
	if err := m.InvestigationAutomationPoliciesModel.UpdateCheckedTx(ctx, tx, row, outcome); err != nil {
		return err
	}
	return errors.New("synthetic post progress failure")
}
func TestInvestigationAutomationLateFailuresAndRevocationRollback(t *testing.T) {
	for _, scenario := range []string{"audit", "progress", "revocation"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, version, ref := automationFixture(t)
			automationEnable(t, s, admin)
			audits := s.AuditEventsModel
			policies := s.InvestigationAutomationPoliciesModel
			if scenario == "progress" {
				s.InvestigationAutomationPoliciesModel = automationCheckedFault{policies}
			} else {
				fault := automationAuditFault{AuditEventsModel: audits}
				if scenario == "revocation" {
					fault.hook = func() {
						if _, err := s.Conn.Exec(`update business.users set auth_version=auth_version+1 where id=$1`, Current(admin).User.Id); err != nil {
							t.Fatal(err)
						}
					}
				}
				s.AuditEventsModel = fault
			}
			found, err := DispatchInvestigationAutomation(context.Background(), s)
			if err == nil || !found {
				t.Fatal(found, err)
			}
			automationCheck(t, s, version.TenantId, ref, investigationActive, 2, 0)
			row, err := policies.FindOne(context.Background(), version.TenantId)
			if err != nil || row.LastOutcome != "never" || row.LastCheckedAt.Valid || row.LastInvestigationId.Valid {
				t.Fatal("progress escaped rollback", row, err)
			}
			s.AuditEventsModel = audits
			s.InvestigationAutomationPoliciesModel = policies
			restarted := automationRestart(s)
			automationDispatch(t, restarted)
			if scenario == "revocation" {
				automationCheck(t, s, version.TenantId, ref, investigationActive, 2, 0)
			} else {
				automationCheck(t, s, version.TenantId, ref, investigationOpen, 3, 1)
			}
		})
	}
}
func TestInvestigationAutomationManualUpdateWinsAndIdleThreshold(t *testing.T) {
	s, admin, _, version, ref := automationFixture(t)
	s.Config.InvestigationAutomationEnabled = true
	req := automationRequest(0, true)
	req.IdleDays = 9
	if _, err := UpdateInvestigationAutomation(admin, s, req); err != nil {
		t.Fatal(err)
	}
	automationDue(t, s, version.TenantId)
	automationDispatch(t, s)
	automationCheck(t, s, version.TenantId, ref, investigationActive, 2, 0)
	if _, err := UpdateInvestigationAutomation(admin, s, automationRequest(1, true)); err != nil {
		t.Fatal(err)
	}
	automationDue(t, s, version.TenantId)
	original := s.SourceInvestigationsModel
	s.SourceInvestigationsModel = automationCandidateHook{original, func(row *model.SourceInvestigations) {
		_, err := SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: row.Revision, Status: investigationClosed, Note: row.Note, Confirm: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
	}}
	automationDispatch(t, s)
	s.SourceInvestigationsModel = original
	automationCheck(t, s, version.TenantId, ref, investigationClosed, 3, 0)
	for range 2 {
		automationDue(t, s, version.TenantId)
		automationDispatch(t, s)
	}
	automationCheck(t, s, version.TenantId, ref, investigationClosed, 3, 0)
}
func TestInvestigationAutomationCurrentResultAclAndForeignCandidate(t *testing.T) {
	s, admin, _, version, ref := automationFixture(t)
	automationEnable(t, s, admin)
	foreign := uuid.NewString()
	if _, err := s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: foreign, Code: foreign, Name: "Synthetic automation scope"}); err != nil {
		t.Fatal(err)
	}
	other := scheduleOtherAdmin(t, s, admin, foreign)
	foreignRef := HashToken("foreign-automation")
	if _, err := s.Conn.Exec(`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,created_by,updated_by,updated_at) values($1,$2,$3,'synthetic',$4,'investigating','Synthetic investigation note',$5,$5,$6)`, uuid.NewString(), foreign, foreignRef, "knowledge/"+foreign+"/misc/test", Current(other).User.Id, time.Now().Add(-100*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	automationDispatch(t, s)
	automationCheck(t, s, foreign, foreignRef, investigationActive, 1, 0)
	receiptRequest := automationRequest(1, false)
	saved, err := UpdateInvestigationAutomation(admin, s, receiptRequest)
	if err != nil || saved.LastObjectRef != ref {
		t.Fatal(saved, err)
	}
	if _, err = s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, version.TenantId); err != nil {
		t.Fatal(err)
	}
	hidden, err := GetInvestigationAutomation(admin, s)
	if err != nil || hidden.LastObjectRef != "" || hidden.LastRevision != 0 {
		t.Fatal("revoked result leaked", hidden, err)
	}
	_, err = UpdateInvestigationAutomation(admin, s, automationRequest(1, false))
	operationError(t, err, "INVESTIGATION_AUTOMATION_CHANGED")
	// Replay of a receipt containing the now-restricted fingerprint is denied.
	_, err = UpdateInvestigationAutomation(admin, s, receiptRequest)
	operationError(t, err, "RESOURCE_NOT_FOUND")
}
func automationRestart(s *svc.ServiceContext) *svc.ServiceContext {
	config := s.Config
	config.FileStoreProvider = "postgres"
	config.FileStoreEndpoint = ""
	return svc.NewServiceContext(config)
}
