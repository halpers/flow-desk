package logic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func adoptionFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *inspectingSources, *types.SourceAdoptionReq) {
	t.Helper()
	s, admin, operator, row, store := inventoryFixture(t)
	s.Config.FileStoreAdoptionEnabled = true
	if _, err := RestoreOriginalSource(admin, s, &types.ArchiveSourceReq{DocumentId: row.DocumentId, VersionId: row.Id, IdempotencyKey: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	page, err := GetSourceInventory(admin, s, inventoryRequest(row))
	if err != nil || !page.Items[0].AdoptionEligible || page.Items[0].AdoptionToken == "" {
		t.Fatal("adoption selection", page, err)
	}
	return s, admin, operator, row, store, &types.SourceAdoptionReq{DocumentId: row.DocumentId, VersionId: row.Id, ObjectRef: page.Items[0].ObjectRef, AdoptionToken: page.Items[0].AdoptionToken, ExpectedSourceEpoch: page.SourceEpoch, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func adoptionCounts(t *testing.T, s *svc.ServiceContext, objects, audits int64) {
	t.Helper()
	var count int64
	if err := s.Conn.QueryRow(&count, "select count(*) from business.source_objects"); err != nil || count != objects {
		t.Fatal("object count", count, err)
	}
	if err := s.Conn.QueryRow(&count, "select count(*) from business.audit_events where operation='knowledge.source_adopt'"); err != nil || count != audits {
		t.Fatal("audit count", count, err)
	}
}

func TestSourceAdoptionVerifiedAtomicReceiptAndExpiredReplay(t *testing.T) {
	s, admin, operator, row, store, req := adoptionFixture(t)
	legacy := fmt.Sprintf("knowledge/%s/%s/%s.pdf", row.TenantId, row.Id, row.ContentHash)
	expires := time.Now().Unix() + 3
	var err error
	req.AdoptionToken, err = sealInventoryCursor(s.Config.JwtSecret, sourceAdoptionScope(admin, row, store.ID()), inventoryPosition{After: legacy, Epoch: 1, Expires: expires})
	if err != nil {
		t.Fatal(err)
	}
	reads, puts := store.reads, store.puts
	receipt, err := AdoptSourceObject(admin, s, req)
	if err != nil || receipt.VersionId != row.Id || receipt.SourceEpoch != 0 || receipt.ObjectRef != req.ObjectRef || receipt.VerifiedAt == "" {
		t.Fatal("adopt", receipt, err)
	}
	if store.reads != reads+1 || store.puts != puts {
		t.Fatal("verification was not one read without writes")
	}
	adoptionCounts(t, s, 2, 1)
	time.Sleep(time.Until(time.Unix(expires+1, 0)))
	fresh := *req
	fresh.IdempotencyKey = uuid.NewString()
	_, err = AdoptSourceObject(admin, s, &fresh)
	assertAccountCode(t, err, "ADOPTION_SELECTION_EXPIRED")
	delete(store.objects, legacy)
	if _, err = s.Conn.Exec("update business.source_objects set status='retired',retired_at=now() where id=$1", receipt.ObjectId); err != nil {
		t.Fatal(err)
	}
	s.Config.FileStoreAdoptionEnabled = false
	s.SourceStore = nil
	replay, err := AdoptSourceObject(admin, s, req)
	if err != nil || marshal(replay) != marshal(receipt) || store.reads != reads+1 {
		t.Fatal("expired/disabled lost-response replay", replay, err)
	}
	changed := *req
	changed.ObjectRef = strings.Repeat("a", 64)
	_, err = AdoptSourceObject(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	if _, err = AdoptSourceObject(operator, s, req); err == nil {
		t.Fatal("ordinary operator replay")
	}
	if _, err = s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = AdoptSourceObject(admin, s, req); err == nil {
		t.Fatal("revoked replay")
	}
	adoptionCounts(t, s, 2, 1)
}

type failingAdoptionReceipt struct{ model.ApiIdempotencyModel }

func (m failingAdoptionReceipt) InsertTx(context.Context, sqlx.Session, *model.ApiIdempotency) error {
	return fmt.Errorf("synthetic receipt failure")
}

func TestSourceAdoptionFailuresNeverRegister(t *testing.T) {
	for _, scenario := range []string{"audit", "receipt", "corrupt", "hash_mismatch", "missing", "unavailable", "disabled", "unconfirmed", "tampered", "inventory_cursor", "original_corrupt", "epoch_stale", "future", "nonstandard", "epoch_change", "revocation", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, row, store, req := adoptionFixture(t)
			legacy := fmt.Sprintf("knowledge/%s/%s/%s.pdf", row.TenantId, row.Id, row.ContentHash)
			reads, puts := store.reads, store.puts
			expectedObjects := int64(1)
			switch scenario {
			case "audit":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "receipt":
				s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
			case "corrupt":
				store.objects[legacy] = []byte("corrupt synthetic PDF")
			case "hash_mismatch":
				store.objects[legacy] = []byte(strings.Repeat("x", int(row.SourceSize)))
			case "unavailable":
				s.SourceStore = unavailableAdoptionSources{store}
			case "inventory_cursor":
				page, err := GetSourceInventory(admin, s, inventoryRequest(row))
				if err != nil {
					t.Fatal(err)
				}
				req.AdoptionToken = page.NextCursor
			case "original_corrupt":
				if _, err := s.Conn.Exec("update business.document_versions set source_bytes=$2 where id=$1", row.Id, []byte(strings.Repeat("x", int(row.SourceSize)))); err != nil {
					t.Fatal(err)
				}
			case "epoch_stale":
				req.ExpectedSourceEpoch++
			case "missing":
				delete(store.objects, legacy)
			case "disabled":
				s.Config.FileStoreAdoptionEnabled = false
			case "unconfirmed":
				req.Confirm = false
			case "tampered":
				req.AdoptionToken = req.AdoptionToken[:len(req.AdoptionToken)/2]
			case "future", "nonstandard":
				key := strings.TrimSuffix(legacy, ".pdf") + "/2.pdf"
				if scenario == "nonstandard" {
					key = "knowledge/" + row.TenantId + "/" + row.Id + "/other.pdf"
				}
				req.ObjectRef = HashToken(store.ID() + "\n" + key)
				req.AdoptionToken, _ = sealInventoryCursor(s.Config.JwtSecret, sourceAdoptionScope(admin, row, store.ID()), inventoryPosition{After: key, Epoch: 1, Expires: time.Now().Add(time.Minute).Unix()})
			case "epoch_change":
				store.afterRead = func() {
					if _, err := s.Conn.Exec("update business.document_versions set source_epoch=source_epoch+1 where id=$1", row.Id); err != nil {
						t.Fatal(err)
					}
				}
			case "revocation":
				store.afterRead = func() {
					if _, err := s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
						t.Fatal(err)
					}
				}
			case "capacity":
				if _, err := s.Conn.Exec(`insert into business.source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size)
      select gen_random_uuid(),$1,$2,0,'synthetic-limit','synthetic-'||n,$3,$4 from generate_series(1,99)n`, row.TenantId, row.Id, row.ContentHash, row.SourceSize); err != nil {
					t.Fatal(err)
				}
				expectedObjects = 100
			}
			if _, err := AdoptSourceObject(admin, s, req); err == nil {
				t.Fatal("unsafe adoption accepted")
			}
			adoptionCounts(t, s, expectedObjects, 0)
			if store.puts != puts {
				t.Fatal("adoption wrote S3")
			}
			if (scenario == "disabled" || scenario == "unconfirmed" || scenario == "tampered" || scenario == "inventory_cursor" || scenario == "original_corrupt" || scenario == "epoch_stale" || scenario == "future" || scenario == "nonstandard" || scenario == "capacity") && store.reads != reads {
				t.Fatal("invalid request reached storage")
			}
		})
	}
}

type unavailableAdoptionSources struct{ *inspectingSources }

func (s unavailableAdoptionSources) Read(context.Context, string, int64) ([]byte, error) {
	return nil, fmt.Errorf("synthetic storage unavailable")
}

type serializedAdoptionSources struct {
	*inspectingSources
	mu sync.Mutex
}

func (s *serializedAdoptionSources) Read(ctx context.Context, key string, limit int64) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.memorySources.Read(ctx, key, limit)
}
func TestSourceAdoptionConcurrentRequestsHaveOneEffect(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(fmt.Sprint(sameKey), func(t *testing.T) {
			s, admin, _, _, store, req := adoptionFixture(t)
			s.SourceStore = &serializedAdoptionSources{inspectingSources: store}
			next := *req
			if !sameKey {
				next.IdempotencyKey = uuid.NewString()
			}
			type outcome struct {
				receipt types.SourceAdoptionData
				err     error
			}
			outcomes := make(chan outcome, 2)
			for _, request := range []*types.SourceAdoptionReq{req, &next} {
				go func(request *types.SourceAdoptionReq) {
					r, e := AdoptSourceObject(admin, s, request)
					outcomes <- outcome{r, e}
				}(request)
			}
			first, second := <-outcomes, <-outcomes
			if sameKey {
				if first.err != nil || second.err != nil || marshal(first.receipt) != marshal(second.receipt) {
					t.Fatal("same-key race", first.err, second.err)
				}
			} else {
				if (first.err == nil) == (second.err == nil) {
					t.Fatal("different-key race", first.err, second.err)
				}
			}
			adoptionCounts(t, s, 2, 1)
		})
	}
}

func TestSourceAdoptionTeamAclAndSelectionScopeRechecked(t *testing.T) {
	s, admin, _, row, store, req := adoptionFixture(t)
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Copy registrar", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	account := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	reads := store.reads
	if _, err = AdoptSourceObject(manager, s, req); err == nil || store.reads != reads {
		t.Fatal("cross-actor selection")
	}
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
	if _, err = GetSourceInventory(manager, s, inventoryRequest(row)); err == nil {
		t.Fatal("team boundary")
	}
	memberChange(t, s, admin, team, account.Id, true)
	page, err := GetSourceInventory(manager, s, inventoryRequest(row))
	if err != nil {
		t.Fatal(err)
	}
	req.AdoptionToken = page.Items[0].AdoptionToken
	store.afterRead = func() {
		if _, e := UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: updated.AclRevision}); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = AdoptSourceObject(manager, s, req); err == nil {
		t.Fatal("ACL revoked during read")
	}
	adoptionCounts(t, s, 1, 0)
	foreign := *Current(admin).User
	foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.adoption"
	if _, err = s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-adoption", Name: "Synthetic foreign"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignCtx, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	reads = store.reads
	if _, err = AdoptSourceObject(foreignCtx, s, req); err == nil || store.reads != reads {
		t.Fatal("cross tenant")
	}
}
