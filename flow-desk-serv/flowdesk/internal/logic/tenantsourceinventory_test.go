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

type tenantInspectingSources struct {
	*inspectingSources
	tenants []string
}

func (s *tenantInspectingSources) ListTenantObjects(_ context.Context, tenant, after string, limit int32) (client.SourceObjectPage, error) {
	s.calls++
	s.tenants = append(s.tenants, tenant)
	if s.afterList != nil {
		hook := s.afterList
		s.afterList = nil
		hook()
	}
	page := client.SourceObjectPage{Items: []client.SourceObjectMetadata{}}
	if s.unavailable {
		return page, fmt.Errorf("private-bucket-secret")
	}
	keys := []string{}
	for key := range s.objects {
		if strings.HasPrefix(key, "knowledge/"+tenant+"/") && key > after {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	page.HasMore = len(keys) > int(limit)
	if page.HasMore {
		keys = keys[:limit]
	}
	for _, key := range keys {
		page.Items = append(page.Items, client.SourceObjectMetadata{Key: key, Size: int64(len(s.objects[key])), ModifiedAt: time.Now()})
	}
	return page, nil
}
func tenantInventoryFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *tenantInspectingSources) {
	t.Helper()
	s, admin, operator, row, store := inventoryFixture(t)
	inspector := &tenantInspectingSources{inspectingSources: store}
	s.SourceStore = inspector
	s.Config.FileStoreTenantInventoryEnabled = true
	return s, admin, operator, row, inspector
}

func TestTenantInventoryUnknownKnownMalformedAndTenantIsolation(t *testing.T) {
	s, admin, _, row, store := tenantInventoryFixture(t)
	unknown := uuid.NewString()
	prefix := "knowledge/" + row.TenantId + "/"
	store.objects[prefix+unknown+"/"+row.ContentHash+"/0.pdf"] = []byte("untrusted")
	store.objects[prefix+"misc/private.txt"] = []byte("untrusted")
	store.objects[prefix+row.Id+"/bad-suffix.txt"] = []byte("nonstandard known version")
	store.objects[prefix+row.Id+"/"+strings.Repeat("a", 64)+".pdf"] = []byte("wrong hash")
	store.objects["knowledge/"+uuid.NewString()+"/"+unknown+"/"+row.ContentHash+".pdf"] = []byte("foreign")
	puts, reads := store.puts, store.reads
	req := &types.TenantSourceInventoryReq{Limit: 2}
	items := []types.TenantSourceInventoryItem{}
	pages := 0
	for {
		page, err := GetTenantSourceInventory(admin, s, req)
		if err != nil {
			t.Fatal(err)
		}
		if page.ContentVerified || page.ScannedCount > 2 {
			t.Fatal(page)
		}
		items = append(items, page.Items...)
		pages++
		if !page.HasMore {
			break
		}
		req.Cursor = page.NextCursor
		if pages > 5 {
			t.Fatal("pagination loop")
		}
	}
	if len(items) != 6 || pages != 3 {
		t.Fatal("scope or paging", len(items), pages)
	}
	classes := map[string]int{}
	reconciliations := map[string]int{}
	refs := map[string]bool{}
	for _, item := range items {
		classes[item.Association]++
		reconciliations[item.Reconciliation]++
		if refs[item.ObjectRef] {
			t.Fatal("duplicate")
		}
		refs[item.ObjectRef] = true
		if item.Association != "known_version" && (item.VersionId != "" || item.DocumentId != "" || item.RegisteredId != "") {
			t.Fatal("untrusted identity exposed", item)
		}
	}
	if classes["known_version"] != 4 || classes["unknown_version"] != 1 || classes["unrecognized_key"] != 1 || reconciliations["version_mismatch"] != 1 || reconciliations["current"] != 1 {
		t.Fatal(classes, reconciliations)
	}
	raw := marshal(items)
	if strings.Contains(raw, "knowledge/") || strings.Contains(raw, unknown) || strings.Contains(raw, row.ContentHash) || strings.Contains(raw, "synthetic-store") {
		t.Fatal("locator or untrusted identity exposed")
	}
	if store.puts != puts || store.reads != reads {
		t.Fatal("metadata inventory read or wrote content")
	}
	for _, tenant := range store.tenants {
		if tenant != row.TenantId {
			t.Fatal("untrusted tenant")
		}
	}
	var count int64
	if err := s.Conn.QueryRow(&count, "select count(*) from business.source_objects where version_id=$1", row.Id); err != nil || count != 1 {
		t.Fatal("automatic registration", err)
	}
	if err := s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.tenant_source_inventory'"); err != nil || count != 3 {
		t.Fatal("audit", err, count)
	}
}

func TestTenantInventoryCurrentAdminOnlyAndFailureFences(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "disabled", "store_failure", "revoked", "role_changed", "flag_changed", "store_changed", "audit_failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, store := tenantInventoryFixture(t)
			viewer := admin
			expected := ""
			switch scenario {
			case "operator":
				viewer = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Tenant inventory delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
				if err != nil {
					t.Fatal(err)
				}
				account := managedAccount(t, s, admin, roles["operator"], role.Id)
				viewer, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			case "disabled":
				s.Config.FileStoreTenantInventoryEnabled = false
				expected = "TENANT_SOURCE_INVENTORY_DISABLED"
			case "store_failure":
				store.unavailable = true
				expected = "SOURCE_INVENTORY_UNAVAILABLE"
			case "revoked":
				store.afterList = func() {
					if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
						t.Fatal(err)
					}
				}
			case "role_changed":
				store.afterList = func() {
					if _, err := s.Conn.Exec("update business.users set role='operator' where id=$1", Current(admin).User.Id); err != nil {
						t.Fatal(err)
					}
				}
			case "flag_changed":
				store.afterList = func() { s.Config.FileStoreTenantInventoryEnabled = false }
				expected = "TENANT_SOURCE_INVENTORY_DISABLED"
			case "store_changed":
				store.afterList = func() { s.SourceStore = nil }
				expected = "SOURCE_UNAVAILABLE"
			case "audit_failure":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			}
			page, err := GetTenantSourceInventory(viewer, s, &types.TenantSourceInventoryReq{})
			if err == nil || len(page.Items) != 0 || page.NextCursor != "" {
				t.Fatal("failed page released", page, err)
			}
			if expected != "" {
				assertAccountCode(t, err, expected)
			}
			if strings.Contains(fmt.Sprint(err), "private") {
				t.Fatal("provider diagnostics exposed")
			}
			if (scenario == "operator" || scenario == "delegated" || scenario == "disabled") && store.calls != 0 {
				t.Fatal("unauthorized storage access")
			}
			var count int64
			if err = s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.tenant_source_inventory' and object_id=$1", row.TenantId); err != nil || count != 0 {
				t.Fatal("success audit for failure", err, count)
			}
		})
	}
}

func TestTenantInventoryAclHidesKnownItemsButAdvancesCursor(t *testing.T) {
	s, admin, _, row, store := tenantInventoryFixture(t)
	doc, err := s.DocumentsModel.FindOne(admin, row.DocumentId)
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.KnowledgeBasesModel.FindOne(admin, doc.KnowledgeBaseId)
	if err != nil {
		t.Fatal(err)
	}
	store.objects["knowledge/"+row.TenantId+"/"+row.Id+"/bad-suffix.txt"] = []byte("restricted malformed object")
	store.afterList = func() {
		if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1 and id=$2`, row.TenantId, base.Id); err != nil {
			t.Fatal(err)
		}
	}
	first, err := GetTenantSourceInventory(admin, s, &types.TenantSourceInventoryReq{Limit: 1})
	if err != nil || len(first.Items) != 0 || first.ScannedCount != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatal("ACL or empty-page cursor", first, err)
	}
	second, err := GetTenantSourceInventory(admin, s, &types.TenantSourceInventoryReq{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 0 || !second.HasMore {
		t.Fatal("hidden middle page", second, err)
	}
	third, err := GetTenantSourceInventory(admin, s, &types.TenantSourceInventoryReq{Limit: 1, Cursor: second.NextCursor})
	if err != nil || len(third.Items) != 0 || third.HasMore {
		t.Fatal("hidden last page", third, err)
	}
}

func TestTenantInventoryCursorScopesExpiryAndArguments(t *testing.T) {
	s, admin, _, row, store := tenantInventoryFixture(t)
	req := &types.TenantSourceInventoryReq{Limit: 1}
	page, err := GetTenantSourceInventory(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"malformed", strings.Repeat("x", 4097), page.NextCursor[:len(page.NextCursor)/2]} {
		req.Cursor = value
		if _, err = GetTenantSourceInventory(admin, s, req); err == nil {
			t.Fatal("bad cursor")
		}
	}
	scope := strings.Join([]string{"tenant-source-inventory-v1", row.TenantId, Current(admin).User.Id, store.ID()}, ":")
	expired, err := sealInventoryCursor(s.Config.JwtSecret, scope, inventoryPosition{After: sourceKey(row), Expires: time.Now().Add(-time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	req.Cursor = expired
	_, err = GetTenantSourceInventory(admin, s, req)
	assertAccountCode(t, err, "INVENTORY_CURSOR_EXPIRED")
	versionPage, err := GetSourceInventory(admin, s, inventoryRequest(row))
	if err != nil {
		t.Fatal(err)
	}
	req.Cursor = versionPage.NextCursor
	if _, err = GetTenantSourceInventory(admin, s, req); err == nil {
		t.Fatal("cross endpoint cursor")
	}
	req.Cursor = page.NextCursor
	roles := managedRoles(t, s, admin)
	account := managedAccount(t, s, admin, roles["admin"])
	other, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	if _, err = GetTenantSourceInventory(other, s, req); err == nil {
		t.Fatal("cross viewer cursor")
	}
	for _, limit := range []int64{-1, 51} {
		if _, err = GetTenantSourceInventory(admin, s, &types.TenantSourceInventoryReq{Limit: limit}); err == nil {
			t.Fatal("invalid limit")
		}
	}
}
