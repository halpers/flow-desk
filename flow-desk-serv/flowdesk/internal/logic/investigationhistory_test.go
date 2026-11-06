package logic

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
)

func historyFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *tenantInspectingSources, *types.SaveSourceInvestigationReq) {
	t.Helper()
	s, admin, operator, row, store := tenantInventoryFixture(t)
	req := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+sourceKey(row)))
	req.Note = "PRIVATE_HISTORY_FIRST <script>literal</script>"
	if _, err := SaveSourceInvestigation(admin, s, req); err != nil {
		t.Fatal(err)
	}
	return s, admin, operator, row, store, req
}
func historyUpdate(t *testing.T, s *svc.ServiceContext, ctx context.Context, ref string, revision int64) types.SourceInvestigationData {
	t.Helper()
	result, err := SaveSourceInvestigation(ctx, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: revision, Status: investigationClosed, Note: fmt.Sprintf("PRIVATE_HISTORY_REVISION_%d", revision+1), Confirm: true, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestInvestigationHistoryCaptureImmutableReplayAndOffline(t *testing.T) {
	s, admin, _, row, store, req := historyFixture(t)
	puts, reads, calls := store.puts, store.reads, store.calls
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	second := historyUpdate(t, s, admin, req.ObjectRef, 1)
	replay, err := SaveSourceInvestigation(admin, s, req)
	if err != nil || replay.Revision != 1 || replay.Note != req.Note {
		t.Fatal("old receipt changed", err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef})
	if err != nil || page.CurrentRevision != 2 || page.SnapshotRevision != 2 || page.FirstRecordedRevision != 1 || len(page.Items) != 2 || page.HasMore || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	first := page.Items[0]
	previous := page.Items[1]
	if first.Revision != 2 || first.Note != second.Note || first.Status != second.Status || first.ChangedBy != second.UpdatedBy || first.ChangedAt != second.UpdatedAt || first.IsBaseline || previous.Note != req.Note || previous.Revision != 1 {
		t.Fatal(page)
	}
	for _, query := range []string{
		`update business.source_investigation_revisions set note='overwritten'`,
		`delete from business.source_investigation_revisions`,
		`truncate table business.source_investigation_revisions`,
		`update business.source_investigations set note='without new revision'`,
		`update business.source_investigations set revision=revision+2`,
		`update business.source_investigations set revision=revision+1,object_key=object_key||'/changed'`,
	} {
		if _, err = s.Conn.Exec(query); err == nil {
			t.Fatal("history mutation accepted", query)
		}
	}
	var snapshots int64
	if err = s.Conn.QueryRow(&snapshots, "select count(*) from business.source_investigation_revisions"); err != nil || snapshots != 2 {
		t.Fatal(snapshots, err)
	}
	var summaries []string
	if err = s.Conn.QueryRows(&summaries, "select summary from business.audit_events where operation='knowledge.source_investigation_history'"); err != nil || len(summaries) != 1 {
		t.Fatal(err)
	}
	for _, value := range []string{req.Note, second.Note, sourceKey(row), store.ID(), req.SelectionToken} {
		if strings.Contains(marshal(summaries), value) {
			t.Fatal("history audit leaked private content")
		}
	}
	if strings.Contains(marshal(page), sourceKey(row)) || strings.Contains(marshal(page), store.ID()) {
		t.Fatal("history exposed locator")
	}
	if puts != store.puts || reads != store.reads || calls != store.calls {
		t.Fatal("history used storage")
	}
	investigationCounts(t, s, 1, 2)
}

func TestInvestigationHistoryStablePagingWithConcurrentNewRevisions(t *testing.T) {
	s, admin, _, _, _, req := historyFixture(t)
	for revision := int64(1); revision < 113; revision++ {
		historyUpdate(t, s, admin, req.ObjectRef, revision)
	}
	first, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Limit: 11})
	if err != nil || len(first.Items) != 11 || !first.HasMore {
		t.Fatal(err)
	}
	scope := investigationHistoryScope(Current(admin).User, req.ObjectRef)
	firstPosition, err := openInventoryCursor(s.Config.JwtSecret, scope, first.NextCursor, 0)
	if err != nil {
		t.Fatal(err)
	}
	historyUpdate(t, s, admin, req.ObjectRef, 113)
	page := first
	var actual []int64
	for index := 0; index < 20; index++ {
		if page.SnapshotRevision != 113 || page.FirstRecordedRevision != 1 {
			t.Fatal("paging upper bound moved", page)
		}
		for _, item := range page.Items {
			actual = append(actual, item.Revision)
		}
		if !page.HasMore {
			break
		}
		position, parseErr := openInventoryCursor(s.Config.JwtSecret, scope, page.NextCursor, 0)
		if parseErr != nil || position.Expires != firstPosition.Expires {
			t.Fatal("cursor expiry extended", parseErr)
		}
		page, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Limit: 11, Cursor: page.NextCursor})
		if err != nil || page.CurrentRevision != 114 {
			t.Fatal(err)
		}
	}
	if len(actual) != 113 {
		t.Fatal("history truncated", len(actual))
	}
	for index, revision := range actual {
		if revision != int64(113-index) {
			t.Fatal("duplicate or omitted revision", actual)
		}
	}
	fresh, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef})
	if err != nil || fresh.SnapshotRevision != 114 || len(fresh.Items) != 20 || fresh.Items[0].Revision != 114 {
		t.Fatal(fresh, err)
	}
}

func TestInvestigationHistoryMigrationBaselinePreservesOnlyKnownLatest(t *testing.T) {
	tenant, actor, investigation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ref := HashToken("legacy synthetic investigation")
	hash, _ := bcrypt.GenerateFromPassword([]byte("integration-password"), bcrypt.MinCost)
	s := testService(t, func(database *sql.DB) {
		if err := goose.UpTo(database, filepath.Join("..", "..", "db", "migrations"), 2026110603); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`insert into business.tenants(id,code,name) values($1,'legacy-history','Synthetic legacy')`, tenant); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`insert into business.users(id,tenant_id,account,password_hash,display_name,role,enabled) values($1,$2,'history.baseline',$3,'Legacy administrator','admin',true)`, actor, tenant, string(hash)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,updated_at) values($1,$2,$3,'old-private-store',$4,'investigating','Only known legacy snapshot',7,$5,$5,'2020-01-01T00:00:00Z')`, investigation, tenant, ref, "knowledge/"+tenant+"/misc/legacy", actor); err != nil {
			t.Fatal(err)
		}

		// An old writer is in flight while the new migration starts.
		pending, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer pending.Rollback()
		if _, err = pending.Exec("update business.source_investigations set revision=8 where id=$1", investigation); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- goose.Up(database, filepath.Join("..", "..", "db", "migrations")) }()
		deadline := time.Now().Add(5 * time.Second)
		waiting := false
		for time.Now().Before(deadline) {
			var count int64
			if err = database.QueryRow(`select count(*) from pg_locks where relation='business.source_investigations'::regclass and mode='ShareRowExclusiveLock' and not granted`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count > 0 {
				waiting = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !waiting {
			t.Fatal("migration did not serialize the old writer")
		}
		if err = pending.Commit(); err != nil {
			t.Fatal(err)
		}
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	})
	admin, _, _ := managedLogin(t, s, "history.baseline", "integration-password")
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || page.FirstRecordedRevision != 8 || len(page.Items) != 1 || !page.Items[0].IsBaseline || page.Items[0].ChangedAt != "2020-01-01T00:00:00Z" || page.Items[0].ChangedBy != actor {
		t.Fatal(page, err)
	}
	historyUpdate(t, s, admin, ref, 8)
	page, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || len(page.Items) != 2 || page.Items[0].Revision != 9 || page.Items[0].IsBaseline || !page.Items[1].IsBaseline {
		t.Fatal(page, err)
	}
	database, err := sql.Open("postgres", s.Config.DatabaseUrl)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = goose.Up(database, filepath.Join("..", "..", "db", "migrations")); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.source_investigation_revisions"); err != nil || count != 2 {
		t.Fatal("migration replay duplicated history", err)
	}
}

func TestInvestigationHistoryCursorValidationAndBoundaries(t *testing.T) {
	s, admin, _, _, _, req := historyFixture(t)
	historyUpdate(t, s, admin, req.ObjectRef, 1)
	first, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []*types.InvestigationHistoryReq{
		{ObjectRef: req.ObjectRef, Limit: -1}, {ObjectRef: req.ObjectRef, Limit: 51}, {ObjectRef: "bad"},
		{ObjectRef: req.ObjectRef, Cursor: "bad"}, {ObjectRef: req.ObjectRef, Cursor: strings.Repeat("x", 4097)},
	} {
		if result, err := ListInvestigationHistory(admin, s, invalid); err == nil || len(result.Items) != 0 {
			t.Fatal("invalid history request", err)
		}
	}
	scope := investigationHistoryScope(Current(admin).User, req.ObjectRef)
	for _, after := range []string{"x", "0|1", "2|0", "2|3", "3|2", "02|1", "2|01", "9223372036854775808|1", "2|1|extra"} {
		cursor, _ := sealInventoryCursor(s.Config.JwtSecret, scope, inventoryPosition{After: after, Expires: time.Now().Add(time.Minute).Unix()})
		if _, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Cursor: cursor}); err == nil {
			t.Fatal("malformed sealed cursor", after)
		}
	}
	cursor, _ := sealInventoryCursor(s.Config.JwtSecret, scope, inventoryPosition{After: "2|1", Expires: time.Now().Add(-time.Minute).Unix()})
	_, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Cursor: cursor})
	assertAccountCode(t, err, "INVESTIGATION_HISTORY_CURSOR_EXPIRED")
	list, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	cursor, _ = sealInventoryCursor(s.Config.JwtSecret, investigationListScope(Current(admin).User, ""), inventoryPosition{After: "2|1", Expires: time.Now().Add(time.Minute).Unix()})
	_ = list
	if _, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Cursor: cursor}); err == nil {
		t.Fatal("cross-interface cursor")
	}
	roles := managedRoles(t, s, admin)
	account := managedAccount(t, s, admin, roles["admin"])
	other, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	if _, err = ListInvestigationHistory(other, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef, Cursor: first.NextCursor}); err == nil {
		t.Fatal("cross-user cursor")
	}
	seedInvestigationList(t, s, admin, 1)
	var otherRef string
	if err = s.Conn.QueryRow(&otherRef, "select object_ref from business.source_investigations where object_ref<>$1", req.ObjectRef); err != nil {
		t.Fatal(err)
	}
	if _, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: otherRef, Cursor: first.NextCursor}); err == nil {
		t.Fatal("cross-object cursor")
	}
	result, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: strings.Repeat("e", 64)})
	if err == nil || len(result.Items) != 0 {
		t.Fatal("unknown investigation exposed history")
	}
}

type revokedInvestigationHistory struct {
	model.SourceInvestigationRevisionsModel
	session string
}

func (m revokedInvestigationHistory) ListPageTx(ctx context.Context, tx sqlx.Session, tenant, investigation string, upper, before, limit int64) ([]model.SourceInvestigationRevisions, error) {
	rows, err := m.SourceInvestigationRevisionsModel.ListPageTx(ctx, tx, tenant, investigation, upper, before, limit)
	if err == nil {
		_, err = tx.ExecCtx(ctx, "update business.sessions set revoked_at=now() where id=$1", m.session)
	}
	return rows, err
}
func TestInvestigationHistoryCurrentAuthorizationAndNoPartialResponses(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "disabled", "foreign_tenant", "acl", "restored_unknown", "during_query", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, store, req := historyFixture(t)
			actor := admin
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "History delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
				if err != nil {
					t.Fatal(err)
				}
				account := managedAccount(t, s, admin, roles["operator"], role.Id)
				actor, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			case "revoked":
				if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(actor).Session.Id); err != nil {
					t.Fatal(err)
				}
			case "role_changed":
				if _, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(actor).User.Id); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := s.Conn.Exec("update business.users set enabled=false where id=$1", Current(actor).User.Id); err != nil {
					t.Fatal(err)
				}
			case "foreign_tenant":
				foreign := *Current(admin).User
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.history"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-history", Name: "Synthetic foreign"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
					t.Fatal(err)
				}
				actor, _, _ = managedLogin(t, s, foreign.Account, "integration-password")
			case "restored_unknown":
				restored := *row
				restored.Id, restored.JobId = uuid.NewString(), uuid.NewString()
				key := "knowledge/" + row.TenantId + "/" + restored.Id + "/unknown.txt"
				store.objects[key] = []byte("synthetic")
				req = investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+key))
				if _, err := SaveSourceInvestigation(admin, s, req); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DocumentVersionsModel.Insert(admin, &restored); err != nil {
					t.Fatal(err)
				}
				fallthrough
			case "acl":
				if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, row.TenantId); err != nil {
					t.Fatal(err)
				}
			case "during_query":
				s.SourceInvestigationRevisionsModel = revokedInvestigationHistory{s.SourceInvestigationRevisionsModel, Current(actor).Session.Id}
			case "audit_failure":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			}
			result, err := ListInvestigationHistory(actor, s, &types.InvestigationHistoryReq{ObjectRef: req.ObjectRef})
			if err == nil || len(result.Items) != 0 || result.ObjectRef != "" || result.HasMore || result.NextCursor != "" || result.CurrentRevision != 0 {
				t.Fatal("partial unauthorized history", result, err)
			}
			var count int64
			if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_investigation_history'"); err != nil || count != 0 {
				t.Fatal("failed read was audited", count, err)
			}
		})
	}
}
