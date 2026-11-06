package logic

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func investigationRequest(t *testing.T, s *svc.ServiceContext, ctx context.Context, ref string) *types.SaveSourceInvestigationReq {
	t.Helper()
	page, err := GetTenantSourceInventory(ctx, s, &types.TenantSourceInventoryReq{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ObjectRef == ref {
			return &types.SaveSourceInvestigationReq{ObjectRef: ref, SelectionToken: item.InvestigationToken, Status: investigationActive, Note: "Synthetic investigation note", Confirm: true, IdempotencyKey: uuid.NewString()}
		}
	}
	t.Fatal("fixture object missing")
	return nil
}
func investigationCounts(t *testing.T, s *svc.ServiceContext, records, audits int64) {
	t.Helper()
	for _, check := range []struct {
		query string
		want  int64
	}{
		{"select count(*) from business.source_investigations", records},
		{"select count(*) from business.source_investigation_revisions", audits},
		{"select count(*) from business.audit_events where operation='knowledge.source_investigate'", audits},
		{"select count(*) from business.api_idempotency where operation='knowledge.source_investigate'", audits},
	} {
		var count int64
		if err := s.Conn.QueryRow(&count, check.query); err != nil || count != check.want {
			t.Fatal(check.query, count, check.want, err)
		}
	}
}
func TestSourceInvestigationReceiptRevisionsOfflineAndMetadataOnly(t *testing.T) {
	s, admin, _, row, store := tenantInventoryFixture(t)
	key := "knowledge/" + row.TenantId + "/misc/investigate.txt"
	store.objects[key] = []byte("synthetic unknown object")
	ref := HashToken(store.ID() + "\n" + key)
	req := investigationRequest(t, s, admin, ref)
	draft, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil || draft.Revision != 0 || draft.Note != "" || len(draft.AllowedStatuses) != 3 {
		t.Fatal(draft, err)
	}
	puts, reads, calls := store.puts, store.reads, store.calls
	first, err := SaveSourceInvestigation(admin, s, req)
	if err != nil || first.Revision != 1 || first.Note != req.Note || first.UpdatedBy != Current(admin).User.Id || first.UpdatedAt == "" {
		t.Fatal(first, err)
	}
	// No S3 access, even for subsequent reads/revisions/replay after inventory pauses.
	delete(store.objects, key)
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	update := *req
	update.SelectionToken = ""
	update.ExpectedRevision = 1
	update.Status = investigationClosed
	update.Note = "Follow-up ended; ownership remains unproven"
	update.IdempotencyKey = uuid.NewString()
	second, err := SaveSourceInvestigation(admin, s, &update)
	if err != nil || second.Revision != 2 || second.Status != investigationClosed {
		t.Fatal(second, err)
	}
	replay, err := SaveSourceInvestigation(admin, s, req)
	if err != nil || marshal(replay) != marshal(first) {
		t.Fatal("old receipt changed", replay, err)
	}
	current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil || marshal(current) != marshal(second) {
		t.Fatal(current, err)
	}
	stale := update
	stale.IdempotencyKey = uuid.NewString()
	_, err = SaveSourceInvestigation(admin, s, &stale)
	assertAccountCode(t, err, "INVESTIGATION_CHANGED")
	changed := *req
	changed.Note = "changed body"
	_, err = SaveSourceInvestigation(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	if store.puts != puts || store.reads != reads || store.calls != calls {
		t.Fatal("note operation used storage")
	}
	stored, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRef(admin, row.TenantId, ref)
	if err != nil || stored.ObjectKey != key || stored.StoreId != store.ID() || stored.CreatedBy != Current(admin).User.Id {
		t.Fatal(stored, err)
	}
	var registrations int64
	if err = s.Conn.QueryRow(&registrations, "select count(*) from business.source_objects"); err != nil || registrations != 1 {
		t.Fatal("registration changed", err)
	}
	var summaries []string
	if err = s.Conn.QueryRows(&summaries, "select summary from business.audit_events where operation='knowledge.source_investigate'"); err != nil {
		t.Fatal(err)
	}
	raw := marshal(summaries) + marshal(current)
	for _, secret := range []string{key, store.ID(), req.SelectionToken} {
		if strings.Contains(raw, secret) {
			t.Fatal("private locator leaked")
		}
	}
	if strings.Contains(marshal(summaries), req.Note) || strings.Contains(marshal(summaries), update.Note) {
		t.Fatal("notes copied into audit")
	}
	investigationCounts(t, s, 1, 2)
}

func TestSourceInvestigationSelectionScopesAndInputLimits(t *testing.T) {
	scenarios := []string{"empty_token", "tamper", "expiry", "inventory_scope", "adoption_scope", "other_user", "other_store", "other_ref", "foreign_prefix", "disabled", "no_store", "not_confirmed", "empty_note", "long_note", "control_note", "bad_status", "negative_revision", "bad_ref", "oversized_token", "expected_existing"}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, row, store := tenantInventoryFixture(t)
			req := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+sourceKey(row)))
			switch scenario {
			case "empty_token":
				req.SelectionToken = ""
			case "tamper":
				req.SelectionToken = req.SelectionToken[:len(req.SelectionToken)/2]
			case "expiry":
				req.SelectionToken, _ = sealInventoryCursor(s.Config.JwtSecret, investigationScope(Current(admin).User, store.ID()), inventoryPosition{After: sourceKey(row), Expires: time.Now().Add(-time.Minute).Unix()})
			case "inventory_scope":
				req.SelectionToken, _ = sealInventoryCursor(s.Config.JwtSecret, "tenant-source-inventory-v1:"+row.TenantId+":"+Current(admin).User.Id+":"+store.ID(), inventoryPosition{After: sourceKey(row), Expires: time.Now().Add(time.Minute).Unix()})
			case "adoption_scope":
				req.SelectionToken, _ = sealInventoryCursor(s.Config.JwtSecret, sourceAdoptionScope(admin, row, store.ID()), inventoryPosition{After: sourceKey(row), Expires: time.Now().Add(time.Minute).Unix()})
			case "other_user":
				roles := managedRoles(t, s, admin)
				account := managedAccount(t, s, admin, roles["admin"])
				admin, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			case "other_store":
				req.SelectionToken, _ = sealInventoryCursor(s.Config.JwtSecret, investigationScope(Current(admin).User, "another-store"), inventoryPosition{After: sourceKey(row), Expires: time.Now().Add(time.Minute).Unix()})
			case "other_ref":
				req.ObjectRef = strings.Repeat("a", 64)
			case "foreign_prefix":
				key := "knowledge/" + uuid.NewString() + "/misc.txt"
				req.ObjectRef = HashToken(store.ID() + "\n" + key)
				req.SelectionToken, _ = sealInventoryCursor(s.Config.JwtSecret, investigationScope(Current(admin).User, store.ID()), inventoryPosition{After: key, Expires: time.Now().Add(time.Minute).Unix()})
			case "disabled":
				s.Config.FileStoreTenantInventoryEnabled = false
			case "no_store":
				s.SourceStore = nil
			case "not_confirmed":
				req.Confirm = false
			case "empty_note":
				req.Note = " \n\t "
			case "long_note":
				req.Note = strings.Repeat("中", 2001)
			case "control_note":
				req.Note = "hidden\x00control"
			case "bad_status":
				req.Status = "owned"
			case "negative_revision":
				req.ExpectedRevision = -1
			case "bad_ref":
				req.ObjectRef = "not-a-fingerprint"
			case "oversized_token":
				req.SelectionToken = strings.Repeat("x", 4097)
			case "expected_existing":
				req.ExpectedRevision = 1
			}
			reads, puts, calls := store.reads, store.puts, store.calls
			if _, err := SaveSourceInvestigation(admin, s, req); err == nil {
				t.Fatal("invalid investigation accepted")
			}
			if store.reads != reads || store.puts != puts || store.calls != calls {
				t.Fatal("invalid note touched storage")
			}
			investigationCounts(t, s, 0, 0)
		})
	}
	// Unicode limit counts runes, not bytes, and retains harmless text literally.
	s, admin, _, row, store := tenantInventoryFixture(t)
	req := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+sourceKey(row)))
	req.Note = strings.Repeat("中", 1990) + "\n\t<script>"
	if saved, err := SaveSourceInvestigation(admin, s, req); err != nil || saved.Note != req.Note {
		t.Fatal(saved, err)
	}
}

func TestSourceInvestigationAtomicInsertAndUpdateRollback(t *testing.T) {
	for _, operation := range []string{"insert", "update"} {
		for _, fault := range []string{"snapshot", "audit", "receipt"} {
			t.Run(operation+"_"+fault, func(t *testing.T) {
				s, admin, _, row, store := tenantInventoryFixture(t)
				req := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+sourceKey(row)))
				count := int64(0)
				if operation == "update" {
					if _, err := SaveSourceInvestigation(admin, s, req); err != nil {
						t.Fatal(err)
					}
					count = 1
					req.ExpectedRevision = 1
					req.Note = "new note must roll back"
					req.IdempotencyKey = uuid.NewString()
				}
				if fault == "snapshot" {
					if _, err := s.Conn.Exec(`alter table business.source_investigation_revisions add constraint synthetic_history_fault check(note<>'new note must roll back')`); err != nil {
						t.Fatal(err)
					}
					req.Note = "new note must roll back"
				} else if fault == "audit" {
					s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
				} else {
					s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
				}
				if _, err := SaveSourceInvestigation(admin, s, req); err == nil {
					t.Fatal("later fault accepted")
				}
				investigationCounts(t, s, count, count)
				actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: req.ObjectRef})
				if err != nil || actual.Revision != count || (count == 1 && actual.Note == req.Note) {
					t.Fatal("partial record committed", actual, err)
				}
			})
		}
	}
}

func TestSourceInvestigationConcurrentCreateUpdateAndSameKey(t *testing.T) {
	for _, scenario := range []string{"create", "update", "same_key"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, row, store := tenantInventoryFixture(t)
			initial := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+sourceKey(row)))
			revision := int64(1)
			if scenario == "update" {
				if _, err := SaveSourceInvestigation(admin, s, initial); err != nil {
					t.Fatal(err)
				}
				initial.ExpectedRevision = 1
				revision = 2
			}
			results := make([]types.SourceInvestigationData, 2)
			failures := make([]error, 2)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for index := range 2 {
				workers.Go(func() {
					req := *initial
					if scenario != "same_key" {
						req.IdempotencyKey = uuid.NewString()
					}
					<-start
					results[index], failures[index] = SaveSourceInvestigation(admin, s, &req)
				})
			}
			close(start)
			workers.Wait()
			successes := 0
			for _, err := range failures {
				if err == nil {
					successes++
				} else {
					assertAccountCode(t, err, "INVESTIGATION_CHANGED")
				}
			}
			if scenario == "same_key" {
				if successes != 2 || marshal(results[0]) != marshal(results[1]) {
					t.Fatal("same key not immutable", failures)
				}
			} else if successes != 1 {
				t.Fatal("lost update", failures)
			}
			actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: initial.ObjectRef})
			if err != nil || actual.Revision != revision {
				t.Fatal(actual, err)
			}
			investigationCounts(t, s, 1, revision)
		})
	}
}

func TestSourceInvestigationKnownAndRestoredVersionAcl(t *testing.T) {
	for _, kind := range []string{"standard", "nonstandard", "restored_unknown"} {
		t.Run(kind, func(t *testing.T) {
			s, admin, _, row, store := tenantInventoryFixture(t)
			key := sourceKey(row)
			if kind == "nonstandard" {
				key = "knowledge/" + row.TenantId + "/" + row.Id + "/private-suffix.txt"
				store.objects[key] = []byte("synthetic")
			}
			restored := *row
			if kind == "restored_unknown" {
				restored.Id = uuid.NewString()
				restored.JobId = uuid.NewString()
				key = "knowledge/" + row.TenantId + "/" + restored.Id + "/untrusted.txt"
				store.objects[key] = []byte("synthetic")
			}
			req := investigationRequest(t, s, admin, HashToken(store.ID()+"\n"+key))
			if _, err := SaveSourceInvestigation(admin, s, req); err != nil {
				t.Fatal(err)
			}
			if kind == "restored_unknown" {
				if _, err := s.DocumentVersionsModel.Insert(admin, &restored); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, row.TenantId); err != nil {
				t.Fatal(err)
			}
			actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: req.ObjectRef})
			if err == nil || actual.Note != "" {
				t.Fatal("restricted note disclosed", actual, err)
			}
			if rejected, replayErr := SaveSourceInvestigation(admin, s, req); replayErr == nil || rejected.Note != "" {
				t.Fatal("restricted receipt replayed or returned partial data", replayErr)
			}
			update := *req
			update.ExpectedRevision = 1
			update.IdempotencyKey = uuid.NewString()
			update.Note = "must not be saved"
			if _, err = SaveSourceInvestigation(admin, s, &update); err == nil {
				t.Fatal("restricted update accepted")
			}
			investigationCounts(t, s, 1, 1)
		})
	}
}

func TestSourceInvestigationCurrentIdentityAndTenantBoundaries(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "disabled_user", "admin_team_policy", "foreign_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, store := tenantInventoryFixture(t)
			actor := admin
			roles := managedRoles(t, s, admin)
			if scenario == "admin_team_policy" {
				account := managedAccount(t, s, admin, roles["admin"])
				actor, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			}
			req := investigationRequest(t, s, actor, HashToken(store.ID()+"\n"+sourceKey(row)))
			if _, err := SaveSourceInvestigation(actor, s, req); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Investigation delegation", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
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
			case "disabled_user":
				if _, err := s.Conn.Exec("update business.users set enabled=false where id=$1", Current(actor).User.Id); err != nil {
					t.Fatal(err)
				}
			case "admin_team_policy":
				team := newTeam(t, s, admin)
				team = memberChange(t, s, admin, team, Current(actor).User.Id, true)
				doc, err := s.DocumentsModel.FindOne(admin, row.DocumentId)
				if err != nil {
					t.Fatal(err)
				}
				base, err := s.KnowledgeBasesModel.FindOne(admin, doc.KnowledgeBaseId)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
				memberChange(t, s, admin, team, Current(actor).User.Id, false)
			case "foreign_tenant":
				foreign := *Current(admin).User
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.investigation"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-investigation", Name: "Synthetic foreign"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
					t.Fatal(err)
				}
				actor, _, _ = managedLogin(t, s, foreign.Account, "integration-password")
			}
			result, err := GetSourceInvestigation(actor, s, &types.SourceInvestigationReq{ObjectRef: req.ObjectRef})
			if scenario == "admin_team_policy" {
				// Existing tenant-admin policy permits all teams, while base ACL still applies.
				if err != nil || result.Revision != 1 {
					t.Fatal("admin team policy changed", result, err)
				}
				replay, replayErr := SaveSourceInvestigation(actor, s, req)
				if replayErr != nil || replay.Revision != 1 {
					t.Fatal("admin receipt", replayErr)
				}
				investigationCounts(t, s, 1, 1)
				return
			}
			if scenario == "foreign_tenant" {
				if err != nil || result.Revision != 0 || result.Note != "" {
					t.Fatal("cross-tenant disclosure", result, err)
				}
			} else if err == nil || result.Note != "" {
				t.Fatal("unauthorized read", result, err)
			}
			if _, err = SaveSourceInvestigation(actor, s, req); err == nil {
				t.Fatal("unauthorized replay")
			}
			update := *req
			update.ExpectedRevision = 1
			update.Note = "unauthorized update"
			update.IdempotencyKey = uuid.NewString()
			if _, err = SaveSourceInvestigation(actor, s, &update); err == nil {
				t.Fatal("unauthorized write")
			}
			investigationCounts(t, s, 1, 1)
		})
	}
}
