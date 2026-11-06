package logic

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

func restoreRequest(ref string, revision, expected int64) *types.RestoreInvestigationRevisionReq {
	return &types.RestoreInvestigationRevisionReq{ObjectRef: ref, Revision: revision, ExpectedRevision: expected, Confirm: true, IdempotencyKey: uuid.NewString()}
}

func TestInvestigationRestoreNewRevisionReplayAndOffline(t *testing.T) {
	s, admin, _, row, store, firstReq := historyFixture(t)
	first, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: firstReq.ObjectRef})
	if err != nil {
		t.Fatal(err)
	}
	second := historyUpdate(t, s, admin, firstReq.ObjectRef, 1)
	roles := managedRoles(t, s, admin)
	account := managedAccount(t, s, admin, roles["admin"])
	restorer, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	puts, reads, calls := store.puts, store.reads, store.calls
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	req := restoreRequest(firstReq.ObjectRef, 1, 2)
	result, err := RestoreInvestigationRevision(restorer, s, req)
	if err != nil || result.RestoredFromRevision != 1 || result.Investigation.Revision != 3 || result.Investigation.Note != first.Note || result.Investigation.Status != first.Status || result.Investigation.UpdatedBy != Current(restorer).User.Id {
		t.Fatal(result, err)
	}
	if instant, err := time.Parse(time.RFC3339Nano, result.Investigation.UpdatedAt); err != nil || instant.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("old actor/time reused", err)
	}
	fourth := historyUpdate(t, s, admin, firstReq.ObjectRef, 3)
	replay, err := RestoreInvestigationRevision(restorer, s, req)
	if err != nil || marshal(result) != marshal(replay) {
		t.Fatal("receipt rewritten", replay, err)
	}
	current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: firstReq.ObjectRef})
	if err != nil || marshal(current) != marshal(fourth) {
		t.Fatal("replay overwrote later edit", current, err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: firstReq.ObjectRef})
	if err != nil || len(page.Items) != 4 || page.Items[1].Note != first.Note || page.Items[1].ChangedBy != Current(restorer).User.Id || page.Items[2].Note != second.Note || page.Items[3].Note != first.Note || page.Items[3].ChangedBy != first.UpdatedBy {
		t.Fatal(page, err)
	}
	var summaries []string
	if err = s.Conn.QueryRows(&summaries, "select summary from business.audit_events where operation=$1", investigationRestoreOperation); err != nil || len(summaries) != 1 {
		t.Fatal(summaries, err)
	}
	raw := marshal(summaries)
	for _, secret := range []string{first.Note, second.Note, sourceKey(row), store.ID(), req.ObjectRef, firstReq.SelectionToken} {
		if strings.Contains(raw, secret) {
			t.Fatal("restore audit leaked content")
		}
	}
	if !strings.Contains(raw, "revision 1 as new revision 3") {
		t.Fatal("missing provenance", raw)
	}
	var receipts, count int64
	if err = s.Conn.QueryRow(&receipts, "select count(*) from business.api_idempotency where operation=$1", investigationRestoreOperation); err != nil || receipts != 1 {
		t.Fatal(receipts, err)
	}
	if err = s.Conn.QueryRow(&count, "select count(*) from business.source_objects"); err != nil || count != 1 || store.puts != puts || store.reads != reads || store.calls != calls {
		t.Fatal("restore touched storage/registration", count, err)
	}
}

func TestInvestigationRestoreBaselineAndMissingEarlierRevision(t *testing.T) {
	tenant, actor, investigation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ref := HashToken("synthetic restore baseline")
	hash, _ := bcrypt.GenerateFromPassword([]byte("integration-password"), bcrypt.MinCost)
	s := testService(t, func(database *sql.DB) {
		if err := goose.UpTo(database, filepath.Join("..", "..", "db", "migrations"), 2026110603); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{`insert into business.tenants(id,code,name) values($1,'restore-baseline','Synthetic baseline')`, []any{tenant}},
			{`insert into business.users(id,tenant_id,account,password_hash,display_name,role,enabled) values($1,$2,'restore.baseline',$3,'Synthetic','admin',true)`, []any{actor, tenant, string(hash)}},
			{`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,updated_at) values($1,$2,$3,'private-store',$4,'open',' Baseline retains exact whitespace ',7,$5,$5,'2020-01-01T00:00:00Z')`, []any{investigation, tenant, ref, "knowledge/" + tenant + "/misc/baseline", actor}},
		} {
			if _, err := database.Exec(statement.sql, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
	})
	admin, _, _ := managedLogin(t, s, "restore.baseline", "integration-password")
	historyUpdate(t, s, admin, ref, 7)
	missing, err := RestoreInvestigationRevision(admin, s, restoreRequest(ref, 1, 8))
	assertAccountCode(t, err, "RESOURCE_NOT_FOUND")
	if missing.Investigation.Note != "" {
		t.Fatal("partial missing history")
	}
	result, err := RestoreInvestigationRevision(admin, s, restoreRequest(ref, 7, 8))
	if err != nil || result.Investigation.Revision != 9 || result.Investigation.Note != " Baseline retains exact whitespace " || result.Investigation.Status != "open" || result.Investigation.UpdatedAt == "2020-01-01T00:00:00Z" {
		t.Fatal(result, err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || len(page.Items) != 3 || page.FirstRecordedRevision != 7 || !page.Items[2].IsBaseline || page.Items[0].IsBaseline || page.Items[0].Note != page.Items[2].Note {
		t.Fatal(page, err)
	}
}

func TestInvestigationRestoreValidationConflictAndKeyScope(t *testing.T) {
	s, admin, _, _, _, first := historyFixture(t)
	historyUpdate(t, s, admin, first.ObjectRef, 1)
	for _, scenario := range []string{"bad_ref", "zero_source", "negative_source", "zero_expected", "negative_expected", "not_confirmed", "bad_key", "current", "future", "stale", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			req := restoreRequest(first.ObjectRef, 1, 2)
			code := "INVALID_ARGUMENT"
			switch scenario {
			case "bad_ref":
				req.ObjectRef = "bad"
			case "zero_source":
				req.Revision = 0
			case "negative_source":
				req.Revision = -1
			case "zero_expected":
				req.ExpectedRevision = 0
			case "negative_expected":
				req.ExpectedRevision = -1
			case "not_confirmed":
				req.Confirm = false
			case "bad_key":
				req.IdempotencyKey = "bad"
			case "current":
				req.Revision = 2
				code = "INVESTIGATION_RESTORE_NOT_OLDER"
			case "future":
				req.Revision = 3
				code = "INVESTIGATION_RESTORE_NOT_OLDER"
			case "stale":
				req.ExpectedRevision = 1
				code = "INVESTIGATION_CHANGED"
			case "unknown":
				req.ObjectRef = strings.Repeat("f", 64)
				code = "RESOURCE_NOT_FOUND"
			}
			result, err := RestoreInvestigationRevision(admin, s, req)
			assertAccountCode(t, err, code)
			if result.Investigation.Note != "" {
				t.Fatal("partial failed restore")
			}
		})
	}
	req := restoreRequest(first.ObjectRef, 1, 2)
	// Same literal key used for an ordinary edit is a distinct operation scope.
	req.IdempotencyKey = first.IdempotencyKey
	if _, err := RestoreInvestigationRevision(admin, s, req); err != nil {
		t.Fatal(err)
	}
	changed := *req
	changed.ExpectedRevision = 3
	_, err := RestoreInvestigationRevision(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.source_investigation_revisions"); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}

type revokedRestoreSource struct {
	model.SourceInvestigationRevisionsModel
	session string
}

func (m revokedRestoreSource) FindOneRevisionTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, revision int64) (*model.SourceInvestigationRevisions, error) {
	row, err := m.SourceInvestigationRevisionsModel.FindOneRevisionTx(ctx, tx, tenant, investigation, revision)
	if err == nil {
		_, err = tx.ExecCtx(ctx, "update business.sessions set revoked_at=now() where id=$1", m.session)
	}
	return row, err
}
func TestInvestigationRestoreAtomicRollback(t *testing.T) {
	for _, fault := range []string{"snapshot", "audit", "receipt", "identity_after_source"} {
		t.Run(fault, func(t *testing.T) {
			s, admin, _, _, _, first := historyFixture(t)
			current := historyUpdate(t, s, admin, first.ObjectRef, 1)
			switch fault {
			case "snapshot":
				if _, err := s.Conn.Exec("alter table business.source_investigation_revisions add constraint synthetic_restore_fault check(revision<>3)"); err != nil {
					t.Fatal(err)
				}
			case "audit":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "receipt":
				s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
			case "identity_after_source":
				s.SourceInvestigationRevisionsModel = revokedRestoreSource{s.SourceInvestigationRevisionsModel, Current(admin).Session.Id}
			}
			result, err := RestoreInvestigationRevision(admin, s, restoreRequest(first.ObjectRef, 1, 2))
			if err == nil || result.Investigation.Note != "" {
				t.Fatal("partial failed restore", result, err)
			}
			actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: first.ObjectRef})
			if err != nil || marshal(actual) != marshal(current) {
				t.Fatal("current row escaped rollback", actual, err)
			}
			for query, want := range map[string]int64{
				"select count(*) from business.source_investigation_revisions":                                           2,
				"select count(*) from business.audit_events where operation='knowledge.source_investigation_restore'":    0,
				"select count(*) from business.api_idempotency where operation='knowledge.source_investigation_restore'": 0,
			} {
				var count int64
				if err = s.Conn.QueryRow(&count, query); err != nil || count != want {
					t.Fatal(query, count, err)
				}
			}
		})
	}
}

func TestInvestigationRestoreConcurrentWithEditorsAndRetries(t *testing.T) {
	for _, scenario := range []string{"same_key", "different_keys", "ordinary_edit"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, _, _, first := historyFixture(t)
			historyUpdate(t, s, admin, first.ObjectRef, 1)
			req := restoreRequest(first.ObjectRef, 1, 2)
			second := *req
			if scenario != "same_key" {
				second.IdempotencyKey = uuid.NewString()
			}
			start := make(chan struct{})
			var group sync.WaitGroup
			failures := make([]error, 2)
			results := make([]types.SourceInvestigationData, 2)
			group.Add(2)
			go func() {
				defer group.Done()
				<-start
				receipt, err := RestoreInvestigationRevision(admin, s, req)
				results[0], failures[0] = receipt.Investigation, err
			}()
			go func() {
				defer group.Done()
				<-start
				if scenario == "ordinary_edit" {
					results[1], failures[1] = SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: first.ObjectRef, ExpectedRevision: 2, Status: investigationClosed, Note: "Concurrent manual edit", Confirm: true, IdempotencyKey: uuid.NewString()})
				} else {
					receipt, err := RestoreInvestigationRevision(admin, s, &second)
					results[1], failures[1] = receipt.Investigation, err
				}
			}()
			close(start)
			group.Wait()
			successes := 0
			for index, err := range failures {
				if err == nil {
					successes++
					if results[index].Revision != 3 {
						t.Fatal(results)
					}
				} else {
					assertAccountCode(t, err, "INVESTIGATION_CHANGED")
				}
			}
			expected := 1
			if scenario == "same_key" {
				expected = 2
				if marshal(results[0]) != marshal(results[1]) {
					t.Fatal("same-key mismatch")
				}
			}
			if successes != expected {
				t.Fatal(successes, failures)
			}
			var snapshots, effects int64
			if err := s.Conn.QueryRow(&snapshots, "select count(*) from business.source_investigation_revisions"); err != nil || snapshots != 3 {
				t.Fatal(snapshots, err)
			}
			if err := s.Conn.QueryRow(&effects, "select count(*) from business.api_idempotency where operation in ('knowledge.source_investigate','knowledge.source_investigation_restore')"); err != nil || effects != 3 {
				t.Fatal(effects, err)
			}
		})
	}
}

func TestInvestigationRestoreCurrentAuthorizationAndReceiptRecheck(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "disabled", "foreign_tenant", "acl", "restored_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, store, first := historyFixture(t)
			if scenario == "restored_unknown" {
				restored := *row
				restored.Id, restored.JobId = uuid.NewString(), uuid.NewString()
				key := "knowledge/" + row.TenantId + "/" + restored.Id + "/unknown.txt"
				store.objects[key] = []byte("synthetic")
				first = investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+key))
				if _, err := SaveSourceInvestigation(admin, s, first); err != nil {
					t.Fatal(err)
				}
				row = &restored
			}
			historyUpdate(t, s, admin, first.ObjectRef, 1)
			original := restoreRequest(first.ObjectRef, 1, 2)
			if _, err := RestoreInvestigationRevision(admin, s, original); err != nil {
				t.Fatal(err)
			}
			actor := admin
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Restore delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
				if err != nil {
					t.Fatal(err)
				}
				account := managedAccount(t, s, admin, roles["operator"], role.Id)
				actor, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			case "revoked":
				if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
					t.Fatal(err)
				}
			case "role_changed":
				if _, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(admin).User.Id); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := s.Conn.Exec("update business.users set enabled=false where id=$1", Current(admin).User.Id); err != nil {
					t.Fatal(err)
				}
			case "foreign_tenant":
				foreign := *Current(admin).User
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.restore"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-restore", Name: "Synthetic"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
					t.Fatal(err)
				}
				actor, _, _ = managedLogin(t, s, foreign.Account, "integration-password")
			case "restored_unknown":
				if _, err := s.DocumentVersionsModel.Insert(admin, row); err != nil {
					t.Fatal(err)
				}
				fallthrough
			case "acl":
				if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, row.TenantId); err != nil {
					t.Fatal(err)
				}
			}
			for _, req := range []*types.RestoreInvestigationRevisionReq{original, restoreRequest(first.ObjectRef, 2, 3)} {
				result, err := RestoreInvestigationRevision(actor, s, req)
				if err == nil || result.Investigation.Note != "" || result.Investigation.ObjectRef != "" || result.RestoredFromRevision != 0 {
					t.Fatal("unauthorized restore/receipt", result, err)
				}
			}
			var count int64
			if err := s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation=$1", investigationRestoreOperation); err != nil || count != 1 {
				t.Fatal("unauthorized effect", count, err)
			}
		})
	}
}
