package logic

import (
	"context"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"sync"
	"testing"
)

type automationPolicyAuditFault struct{ model.AuditEventsModel }

func (m automationPolicyAuditFault) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if err := m.AuditEventsModel.InsertTx(ctx, tx, row); err != nil {
		return err
	}
	if row.Operation == "knowledge.investigation_automation" {
		return errors.New("synthetic policy audit failure")
	}
	return nil
}
func TestInvestigationAutomationPolicyAtomicityAndConcurrentReceipts(t *testing.T) {
	s, admin, _, version, _ := automationFixture(t)
	audits := s.AuditEventsModel
	s.AuditEventsModel = automationPolicyAuditFault{audits}
	request := automationRequest(0, true)
	if _, err := UpdateInvestigationAutomation(admin, s, request); err == nil {
		t.Fatal("expected post audit failure")
	}
	for _, query := range []string{`select count(*) from business.investigation_automation_policies`, `select count(*) from business.audit_events where operation='knowledge.investigation_automation'`, `select count(*) from business.api_idempotency where operation='knowledge.investigation_automation'`} {
		var count int64
		if err := s.Conn.QueryRow(&count, query); err != nil || count != 0 {
			t.Fatal("policy escaped rollback", count, err)
		}
	}
	s.AuditEventsModel = audits
	var group sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		group.Go(func() {
			row, err := UpdateInvestigationAutomation(admin, s, request)
			if err == nil && (row.Revision != 1 || !row.Enabled) {
				err = errors.New("wrong concurrent receipt")
			}
			results <- err
		})
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := s.Conn.QueryRow(&count, `select count(*) from business.audit_events where tenant_id=$1 and operation='knowledge.investigation_automation'`, version.TenantId); err != nil || count != 1 {
		t.Fatal("multiple policy effects", count, err)
	}
}
func TestInvestigationAutomationUnknownIdentityBecomesRestricted(t *testing.T) {
	s, admin, _, version, knownRef := automationFixture(t)
	if _, err := SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: knownRef, ExpectedRevision: 2, Status: investigationClosed, Note: "Synthetic investigation note", Confirm: true, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	unknownVersion := uuid.NewString()
	key := "knowledge/" + version.TenantId + "/" + unknownVersion + "/malformed-synthetic-name"
	ref := HashToken("synthetic\n" + key)
	if _, err := s.Conn.Exec(`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,created_by,updated_by,updated_at) values($1,$2,$3,'synthetic',$4,'investigating','Synthetic investigation note',$5,$5,clock_timestamp()-interval '8 days')`, uuid.NewString(), version.TenantId, ref, key, Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	automationEnable(t, s, admin)
	automationDispatch(t, s)
	automationCheck(t, s, version.TenantId, ref, investigationOpen, 2, 1)
	if _, err := SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: 2, Status: investigationActive, Note: "Synthetic investigation note", Confirm: true, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.Exec(`update business.source_investigations set revision=revision+1,updated_at=clock_timestamp()-interval '8 days' where tenant_id=$1 and object_ref=$2`, version.TenantId, ref); err != nil {
		t.Fatal(err)
	}
	restored := *version
	restored.Id = unknownVersion
	restored.JobId = uuid.NewString()
	if _, err := s.DocumentVersionsModel.Insert(context.Background(), &restored); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, version.TenantId); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		automationDue(t, s, version.TenantId)
		automationDispatch(t, s)
	}
	automationCheck(t, s, version.TenantId, ref, investigationActive, 4, 1)
	result, err := GetInvestigationAutomation(admin, s)
	if err != nil || result.LastObjectRef != "" || result.LastRevision != 0 || result.LastOutcome != "skipped" {
		t.Fatal("new identity bypassed current ACL", result, err)
	}
}
