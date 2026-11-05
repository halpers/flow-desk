package logic

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

type inspectingSources struct {
	*memorySources
	calls       int
	afterList   func()
	unavailable bool
}

func (s *inspectingSources) ListVersionObjects(_ context.Context, tenant, version, after string, limit int32) (client.SourceObjectPage, error) {
	s.calls++
	page := client.SourceObjectPage{Items: []client.SourceObjectMetadata{}}
	if s.afterList != nil {
		hook := s.afterList
		s.afterList = nil
		hook()
	}
	if s.unavailable {
		return page, fmt.Errorf("private-object-and-credential-diagnostic")
	}
	keys := []string{}
	for key := range s.objects {
		if strings.HasPrefix(key, "knowledge/"+tenant+"/"+version+"/") && key > after {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page.HasMore = len(keys) > int(limit)
	if page.HasMore {
		keys = keys[:limit]
	}
	for _, key := range keys {
		page.Items = append(page.Items, client.SourceObjectMetadata{Key: key, Size: int64(len(s.objects[key])), ModifiedAt: time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)})
	}
	return page, nil
}
func inventoryFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *inspectingSources) {
	t.Helper()
	s, admin, operator, row, event, memory := sourceFixture(t)
	inspector := &inspectingSources{memorySources: memory}
	s.SourceStore = inspector
	s.Config.FileStoreInventoryEnabled = true
	if err := ArchiveOriginalSource(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	legacy := fmt.Sprintf("knowledge/%s/%s/%s.pdf", row.TenantId, row.Id, row.ContentHash)
	inspector.objects[legacy] = slices.Clone(row.SourceBytes)
	return s, admin, operator, row, inspector
}
func inventoryRequest(row *model.DocumentVersions) *types.SourceInventoryReq {
	return &types.SourceInventoryReq{DocumentId: row.DocumentId, VersionId: row.Id, Limit: 1}
}

func TestSourceInventoryReconciliationPaginationAndNoMutations(t *testing.T) {
	s, admin, _, row, store := inventoryFixture(t)
	beforePuts := store.puts
	req := inventoryRequest(row)
	page, err := GetSourceInventory(admin, s, req)
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.ContentVerified {
		t.Fatal("first", page, err)
	}
	if page.Items[0].Reconciliation != "unregistered" || page.Items[0].KeyKind != "legacy" || page.Items[0].RegisteredId != "" {
		t.Fatal("legacy orphan", page)
	}
	if strings.Contains(page.NextCursor, "knowledge") || strings.Contains(marshal(page), "synthetic-store") {
		t.Fatal("locator exposed")
	}
	req.Cursor = page.NextCursor
	next, err := GetSourceInventory(admin, s, req)
	if err != nil || len(next.Items) != 1 || next.HasMore || next.NextCursor != "" || next.Items[0].Reconciliation != "current" || next.Items[0].RegisteredId == "" {
		t.Fatal("registered current", next, err)
	}
	if store.puts != beforePuts {
		t.Fatal("inventory mutated objects")
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.source_objects where version_id=$1", row.Id); err != nil || count != 1 {
		t.Fatal("inventory adopted orphan", err)
	}
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_inventory'"); err != nil || count != 2 {
		t.Fatal("read audit", err)
	}
	key := sourceKey(row)
	store.objects[key] = []byte("unexpected-size")
	next, err = GetSourceInventory(admin, s, req)
	if err != nil || next.Items[0].Reconciliation != "size_mismatch" {
		t.Fatal("size anomaly", next, err)
	}
	if _, err = s.Conn.Exec("update business.source_objects set status='retired' where version_id=$1", row.Id); err != nil {
		t.Fatal(err)
	}
	store.objects[key] = nil
	next, err = GetSourceInventory(admin, s, req)
	if err != nil || next.Items[0].Reconciliation != "retired_zero_bytes" || next.ContentVerified {
		t.Fatal("marker observation", next, err)
	}
	for _, value := range []string{"00", "-1", "9223372036854775808", "9007199254740992", "1/2"} {
		kind, _ := inventoryKeyKind(row, strings.TrimSuffix(key, "0.pdf")+value+".pdf")
		if kind != "unrecognized" {
			t.Fatal("ambiguous epoch accepted", value)
		}
	}
}

func TestSourceInventoryCursorAuthenticationExpiryAndEpoch(t *testing.T) {
	s, admin, _, row, _ := inventoryFixture(t)
	page, err := GetSourceInventory(admin, s, inventoryRequest(row))
	if err != nil {
		t.Fatal(err)
	}
	req := inventoryRequest(row)
	for _, cursor := range []string{"invalid", strings.Repeat("x", 4097), page.NextCursor[:len(page.NextCursor)/2]} {
		req.Cursor = cursor
		if _, err = GetSourceInventory(admin, s, req); err == nil {
			t.Fatal("tampered cursor accepted")
		}
	}
	scope := strings.Join([]string{"source-inventory-v1", row.TenantId, Current(admin).User.Id, row.DocumentId, row.Id, row.ContentHash, s.SourceStore.ID()}, ":")
	cursor, err := sealInventoryCursor(s.Config.JwtSecret, scope, inventoryPosition{After: sourceKey(row), Epoch: 0, Expires: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	req.Cursor = cursor
	_, err = GetSourceInventory(admin, s, req)
	assertAccountCode(t, err, "INVENTORY_CURSOR_EXPIRED")
	req.Cursor = page.NextCursor
	if _, err = RestoreOriginalSource(admin, s, &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	_, err = GetSourceInventory(admin, s, req)
	assertAccountCode(t, err, "SOURCE_CHANGED")
}

func TestSourceInventoryStateChangesAuthorizationAndFailure(t *testing.T) {
	s, admin, operator, row, store := inventoryFixture(t)
	req := inventoryRequest(row)
	if _, err := GetSourceInventory(operator, s, req); err == nil || store.calls != 0 {
		t.Fatal("ordinary account reached storage")
	}
	s.Config.FileStoreInventoryEnabled = false
	_, err := GetSourceInventory(admin, s, req)
	assertAccountCode(t, err, "SOURCE_INVENTORY_DISABLED")
	if store.calls != 0 {
		t.Fatal("disabled inventory reached storage")
	}
	s.Config.FileStoreInventoryEnabled = true
	store.unavailable = true
	_, err = GetSourceInventory(admin, s, req)
	assertAccountCode(t, err, "SOURCE_INVENTORY_UNAVAILABLE")
	if strings.Contains(fmt.Sprint(err), "private") {
		t.Fatal("provider diagnostic leaked")
	}
	store.unavailable = false
	saved := s.AuditEventsModel
	s.AuditEventsModel = failingAccountAudit{saved}
	if _, err = GetSourceInventory(admin, s, req); err == nil {
		t.Fatal("audit failure accepted")
	}
	s.AuditEventsModel = saved
	store.afterList = func() {
		if _, e := RestoreOriginalSource(admin, s, &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}); e != nil {
			t.Fatal(e)
		}
	}
	_, err = GetSourceInventory(admin, s, req)
	assertAccountCode(t, err, "SOURCE_CHANGED")
	store.afterList = func() {
		if _, e := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = GetSourceInventory(admin, s, req); err == nil {
		t.Fatal("revoked observation returned")
	}
	var count int64
	if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_inventory'"); err != nil || count != 0 {
		t.Fatal("failed inventory recorded success", err)
	}
}

func TestSourceInventoryCurrentTeamAclAndCrossTenant(t *testing.T) {
	s, admin, _, row, store := inventoryFixture(t)
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Inventory manager", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	account := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	doc, err := s.DocumentsModel.FindOne(admin, row.DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.KnowledgeBasesModel.FindOne(admin, doc.KnowledgeBaseId)
	if err != nil {
		t.Fatal(err)
	}
	team := newTeam(t, s, admin)
	updated, err := SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	req := inventoryRequest(row)
	if _, err = GetSourceInventory(manager, s, req); err == nil || store.calls != 0 {
		t.Fatal("team boundary")
	}
	memberChange(t, s, admin, team, account.Id, true)
	page, err := GetSourceInventory(manager, s, req)
	if err != nil {
		t.Fatal(err)
	}
	otherReq := *req
	otherReq.Cursor = page.NextCursor
	if _, err = GetSourceInventory(admin, s, &otherReq); err == nil {
		t.Fatal("cross viewer cursor")
	}
	store.afterList = func() {
		_, e := UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: updated.AclRevision})
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, err = GetSourceInventory(manager, s, req); err == nil {
		t.Fatal("ACL revoked during storage I/O")
	}
	foreign := *Current(admin).User
	foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.inventory"
	if _, err = s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-inventory", Name: "Synthetic foreign"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignCtx, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	before := store.calls
	if _, err = GetSourceInventory(foreignCtx, s, req); err == nil || store.calls != before {
		t.Fatal("cross tenant storage request")
	}
}
