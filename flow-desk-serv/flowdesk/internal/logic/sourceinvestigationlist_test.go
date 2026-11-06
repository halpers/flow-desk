package logic

import (
	"context"
	"strings"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func seedInvestigationList(t *testing.T, s *svc.ServiceContext, ctx context.Context, count int) {
	t.Helper()
	actor := Current(ctx).User
	_, err := s.Conn.Exec(`insert into business.source_investigations
 (id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,created_at)
 select gen_random_uuid(),$1,md5('synthetic-worklist-'||n)||md5('private-worklist-'||n),'synthetic-private-store',
 'knowledge/'||$1::uuid::text||'/misc/'||n,case n%3 when 0 then 'open' when 1 then 'investigating' else 'closed' end,
 'PRIVATE_WORKLIST_NOTE',1,$2,$2,'2020-01-01T00:00:00Z'::timestamptz from generate_series(1,$3)n`, actor.TenantId, actor.Id, count)
	if err != nil {
		t.Fatal(err)
	}
}
func worklistKnown(t *testing.T, s *svc.ServiceContext, ctx context.Context, row *model.DocumentVersions, store *tenantInspectingSources, key string) types.SourceInvestigationData {
	t.Helper()
	if _, exists := store.objects[key]; !exists {
		store.objects[key] = []byte("synthetic")
	}
	req := investigationRequest(t, s, ctx, HashToken(store.ID()+"\n"+key))
	saved, err := SaveSourceInvestigation(ctx, s, req)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}
func TestInvestigationListStablePagingBeyondLegacyLimit(t *testing.T) {
	s, admin, _, row, store := tenantInventoryFixture(t)
	known := worklistKnown(t, s, admin, row, store, sourceKey(row))
	seedInvestigationList(t, s, admin, 108)
	var expected []string
	if err := s.Conn.QueryRows(&expected, "select object_ref from business.source_investigations order by created_at desc,id desc"); err != nil {
		t.Fatal(err)
	}
	req := &types.SourceInvestigationListReq{Limit: 11}
	first, err := ListSourceInvestigations(admin, s, req)
	if err != nil || len(first.Items) != 11 || !first.HasMore || len(first.AllowedStatuses) != 3 {
		t.Fatal(first, err)
	}
	// Metadata edits keep their immutable creation position; new records require refresh.
	update := &types.SaveSourceInvestigationReq{ObjectRef: known.ObjectRef, ExpectedRevision: 1, Status: investigationClosed, Note: "Updated after first list page", Confirm: true, IdempotencyKey: uuid.NewString()}
	if _, err = SaveSourceInvestigation(admin, s, update); err != nil {
		t.Fatal(err)
	}
	newRef := HashToken("new after page")
	if _, err = s.Conn.Exec(`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,created_by,updated_by)
 values(gen_random_uuid(),$1,$2,'synthetic-private-store',$3,'open','PRIVATE_NEW_NOTE',$4,$4)`, row.TenantId, newRef, "knowledge/"+row.TenantId+"/misc/new", Current(admin).User.Id); err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	page := first
	for index := 0; index < 20; index++ {
		for _, item := range page.Items {
			actual = append(actual, item.ObjectRef)
		}
		if !page.HasMore {
			break
		}
		req.Cursor = page.NextCursor
		page, err = ListSourceInvestigations(admin, s, req)
		if err != nil {
			t.Fatal(err)
		}
	}
	if marshal(expected) != marshal(actual) {
		t.Fatal("paging omission/duplicate or edit displaced cursor", len(actual), len(expected))
	}
	refreshed, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{})
	if err != nil || len(refreshed.Items) != 20 || refreshed.Items[0].ObjectRef != newRef || refreshed.Items[1].Revision != 2 {
		t.Fatal(refreshed, err)
	}
	raw := marshal(refreshed)
	for _, secret := range []string{"note", "object_key", "store_id", "knowledge/", "PRIVATE_", store.ID(), "selection_token"} {
		if strings.Contains(raw, secret) {
			t.Fatal("list disclosed private data", secret)
		}
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_investigation_list'"); err != nil || count != 11 {
		t.Fatal("audit count", count, err)
	}
}

func TestInvestigationListStatusFilteringOfflineAndNoStorageIO(t *testing.T) {
	s, admin, _, _, store := tenantInventoryFixture(t)
	seedInvestigationList(t, s, admin, 21)
	reads, puts, calls := store.reads, store.puts, store.calls
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	for _, status := range investigationStatuses() {
		req := &types.SourceInvestigationListReq{Status: status, Limit: 3}
		refs := map[string]bool{}
		for index := 0; index < 10; index++ {
			page, err := ListSourceInvestigations(admin, s, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range page.Items {
				if item.Status != status || refs[item.ObjectRef] {
					t.Fatal("filter or duplicate", item)
				}
				refs[item.ObjectRef] = true
			}
			if !page.HasMore {
				break
			}
			req.Cursor = page.NextCursor
		}
		if len(refs) != 7 {
			t.Fatal(status, len(refs))
		}
	}
	if store.reads != reads || store.puts != puts || store.calls != calls {
		t.Fatal("worklist touched storage")
	}
	var notes []string
	if err := s.Conn.QueryRows(&notes, "select summary from business.audit_events where operation='knowledge.source_investigation_list'"); err != nil || len(notes) != 9 || strings.Contains(marshal(notes), "PRIVATE_") {
		t.Fatal("audit scope", err)
	}
}

func TestInvestigationListHiddenPagesAndRestoredIdentityAcl(t *testing.T) {
	for _, scenario := range []string{"known", "restored_unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, row, store := tenantInventoryFixture(t)
			seedInvestigationList(t, s, admin, 1)
			key := sourceKey(row)
			restored := *row
			if scenario == "restored_unknown" {
				restored.Id = uuid.NewString()
				restored.JobId = uuid.NewString()
				key = "knowledge/" + row.TenantId + "/" + restored.Id + "/nonstandard.txt"
			}
			saved := worklistKnown(t, s, admin, row, store, key)
			if scenario == "restored_unknown" {
				if _, err := s.DocumentVersionsModel.Insert(admin, &restored); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, row.TenantId); err != nil {
				t.Fatal(err)
			}
			first, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Limit: 1})
			if err != nil || len(first.Items) != 0 || !first.HasMore || first.NextCursor == "" {
				t.Fatal("restricted page/cursor", first, err)
			}
			next, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Limit: 1, Cursor: first.NextCursor})
			if err != nil || len(next.Items) != 1 || next.HasMore || next.Items[0].ObjectRef == saved.ObjectRef {
				t.Fatal("empty page did not advance", next, err)
			}
		})
	}
}

func TestInvestigationListCursorScopesExpiryAndInvalidInputs(t *testing.T) {
	s, admin, _, _, _ := tenantInventoryFixture(t)
	seedInvestigationList(t, s, admin, 3)
	first, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []*types.SourceInvestigationListReq{
		{Limit: -1}, {Limit: 51}, {Status: "owned"}, {Cursor: "tampered"}, {Cursor: strings.Repeat("x", 4097)},
		{Cursor: first.NextCursor, Status: investigationClosed},
	} {
		if _, err = ListSourceInvestigations(admin, s, req); err == nil {
			t.Fatal("invalid request", req.Status, req.Limit)
		}
	}
	roles := managedRoles(t, s, admin)
	account := managedAccount(t, s, admin, roles["admin"])
	other, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	if _, err = ListSourceInvestigations(other, s, &types.SourceInvestigationListReq{Cursor: first.NextCursor}); err == nil {
		t.Fatal("cross-user cursor")
	}
	for _, position := range []string{"not-a-position", "bad-time|" + uuid.NewString(), time.Now().Format(time.RFC3339Nano) + "|not-an-id"} {
		token, _ := sealInventoryCursor(s.Config.JwtSecret, investigationListScope(Current(admin).User, ""), inventoryPosition{After: position, Expires: time.Now().Add(time.Minute).Unix()})
		if _, err = ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Cursor: token}); err == nil {
			t.Fatal("malformed sealed position")
		}
	}
	token, _ := sealInventoryCursor(s.Config.JwtSecret, investigationListScope(Current(admin).User, ""), inventoryPosition{After: "x", Expires: time.Now().Add(-time.Minute).Unix()})
	_, err = ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Cursor: token})
	assertAccountCode(t, err, "INVESTIGATION_CURSOR_EXPIRED")
	token, _ = sealInventoryCursor(s.Config.JwtSecret, "tenant-source-inventory-v1:other", inventoryPosition{After: "x", Expires: time.Now().Add(time.Minute).Unix()})
	if _, err = ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Cursor: token}); err == nil {
		t.Fatal("cross-interface cursor")
	}
	// Entire paging session retains its original expiry rather than extending it each page.
	position, err := openInventoryCursor(s.Config.JwtSecret, investigationListScope(Current(admin).User, ""), first.NextCursor, 0)
	if err != nil {
		t.Fatal(err)
	}
	next, err := ListSourceInvestigations(admin, s, &types.SourceInvestigationListReq{Limit: 1, Cursor: first.NextCursor})
	if err != nil || !next.HasMore {
		t.Fatal(err)
	}
	nextPosition, err := openInventoryCursor(s.Config.JwtSecret, investigationListScope(Current(admin).User, ""), next.NextCursor, 0)
	if err != nil || nextPosition.Expires != position.Expires {
		t.Fatal("cursor lifetime extended", err)
	}
}

type revokedInvestigationList struct {
	model.SourceInvestigationsModel
	session string
}

func (m revokedInvestigationList) ListPageTx(ctx context.Context, tx sqlx.Session, tenant, status, before, beforeID string, limit int64) ([]model.SourceInvestigationListRow, error) {
	rows, err := m.SourceInvestigationsModel.ListPageTx(ctx, tx, tenant, status, before, beforeID, limit)
	if err == nil {
		_, err = tx.ExecCtx(ctx, "update business.sessions set revoked_at=now() where id=$1", m.session)
	}
	return rows, err
}
func TestInvestigationListCurrentIdentityTenantIsolationAndAtomicFailure(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "during_query", "audit_failure", "foreign_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, _, _ := tenantInventoryFixture(t)
			seedInvestigationList(t, s, admin, 2)
			actor := admin
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Worklist delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
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
			case "during_query":
				s.SourceInvestigationsModel = revokedInvestigationList{s.SourceInvestigationsModel, Current(actor).Session.Id}
			case "audit_failure":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "foreign_tenant":
				foreign := *Current(admin).User
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.worklist"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-worklist", Name: "Synthetic foreign"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
					t.Fatal(err)
				}
				actor, _, _ = managedLogin(t, s, foreign.Account, "integration-password")
			}
			result, err := ListSourceInvestigations(actor, s, &types.SourceInvestigationListReq{Limit: 1})
			if scenario == "foreign_tenant" {
				if err != nil || len(result.Items) != 0 || result.HasMore || result.NextCursor != "" {
					t.Fatal("cross-tenant disclosure", result, err)
				}
				return
			}
			if err == nil || len(result.Items) != 0 || result.HasMore || result.NextCursor != "" {
				t.Fatal("partial unauthorized page", result, err)
			}
			var count int64
			if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_investigation_list'"); err != nil || count != 0 {
				t.Fatal("failed page audited", count, err)
			}
		})
	}
}
