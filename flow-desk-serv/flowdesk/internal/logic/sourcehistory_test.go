package logic

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

func retiredHistoryFixture(t *testing.T) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *retiringSources, *types.CleanupArchiveCopyReq, *model.OutboxEvents) {
	t.Helper()
	s, admin, operator, row, store, req := sourceCleanupFixture(t)
	if _, err := CleanupArchiveCopy(admin, s, req); err != nil {
		t.Fatal(err)
	}
	event := claimSourceCleanup(t, s, req.ObjectId)
	if err := CleanupSourceObject(context.Background(), s, event); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Conn.Exec("update business.outbox_events set status='delivered' where id=$1", event.Id); err != nil {
		t.Fatal(err)
	}
	return s, admin, operator, row, store, req, event
}
func sourceMetadataRequest(req *types.CleanupArchiveCopyReq) *types.ArchiveSourceMetadataReq {
	return &types.ArchiveSourceMetadataReq{DocumentId: req.DocumentId, VersionId: req.VersionId, ObjectId: req.ObjectId, ExpectedSourceEpoch: req.ExpectedSourceEpoch, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func historyCounts(t *testing.T, s *svc.ServiceContext, id string, hot, cold, audits int64) {
	t.Helper()
	for _, test := range []struct {
		query string
		want  int64
	}{
		{"select count(*) from business.source_objects where id=$1", hot},
		{"select count(*) from business.source_object_archives where id=$1", cold},
		{"select count(*) from business.audit_events where operation='knowledge.source_metadata_archive' and object_id=$1", audits},
	} {
		var count int64
		if err := s.Conn.QueryRow(&count, test.query, id); err != nil || count != test.want {
			t.Fatal("archive counts", count, test.want, err)
		}
	}
}
func TestSourceHistoryArchiveAtomicIdentityAndOldCleanupReplay(t *testing.T) {
	s, admin, operator, row, store, cleanup, event := retiredHistoryFixture(t)
	req := sourceMetadataRequest(cleanup)
	original, err := s.SourceObjectsModel.FindOneByTenantIdId(admin, row.TenantId, req.ObjectId)
	if err != nil {
		t.Fatal(err)
	}
	reads, puts, retires := store.reads, store.puts, store.retirements
	receipt, err := ArchiveSourceMetadata(admin, s, req)
	if err != nil || receipt.ObjectId != req.ObjectId || receipt.ArchivedAt == "" {
		t.Fatal("archive", receipt, err)
	}
	historyCounts(t, s, req.ObjectId, 0, 1, 1)
	archived, err := s.SourceObjectArchivesModel.FindOneByTenantIdId(admin, row.TenantId, req.ObjectId)
	if err != nil || archived.ObjectKey != original.ObjectKey || archived.ContentHash != original.ContentHash || archived.SourceSize != original.SourceSize || archived.StoreId != original.StoreId || archived.SourceEpoch != original.SourceEpoch || !archived.CreatedAt.Equal(original.CreatedAt) || !archived.RetiredAt.Equal(original.RetiredAt.Time) || archived.ArchivedBy != Current(admin).User.Id {
		t.Fatal("incomplete identity", err)
	}
	compatible, err := s.SourceObjectsModel.FindOneByStoreIdObjectKey(admin, original.StoreId, original.ObjectKey)
	if err != nil || marshal(compatible) != marshal(original) {
		t.Fatal("locator identity", err)
	}
	if err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		identities, readErr := s.SourceObjectsModel.ListInventoryRegistrationsTx(ctx, tx, row.TenantId, row.Id, original.StoreId, []string{original.ObjectKey})
		if readErr != nil || len(identities) != 1 || identities[0].Status != sourceObjectRetired {
			return fmt.Errorf("inventory identity %v", readErr)
		}
		tenantIdentities, readErr := s.SourceObjectsModel.ListTenantInventoryRegistrationsTx(ctx, tx, row.TenantId, original.StoreId, []string{original.ObjectKey})
		if readErr != nil || len(tenantIdentities) != 1 {
			return fmt.Errorf("tenant inventory identity %v", readErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	preview, err := ListArchiveCopies(admin, s, sourceRequest(row))
	if err != nil || len(preview.Items) != 0 {
		t.Fatal("hot preview", err)
	}
	page, err := ListSourceCopyHistory(admin, s, &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id})
	if err != nil || len(page.Items) != 1 || !page.Items[0].MetadataArchived || page.Items[0].CanArchive || page.Items[0].ArchivedAt != receipt.ArchivedAt {
		t.Fatal("history", page, err)
	}
	old, err := CleanupArchiveCopy(admin, s, cleanup)
	if err != nil || old.Status != sourceObjectRetired {
		t.Fatal("old cleanup receipt", err)
	}
	if err = CleanupSourceObject(context.Background(), s, event); err != nil {
		t.Fatal("late worker replay", err)
	}
	if store.reads != reads || store.puts != puts || store.retirements != retires {
		t.Fatal("metadata archival touched storage")
	}
	// A later generation does not change an already committed receipt.
	if _, err = s.Conn.Exec("update business.document_versions set source_epoch=source_epoch+1 where id=$1", row.Id); err != nil {
		t.Fatal(err)
	}
	replay, err := ArchiveSourceMetadata(admin, s, req)
	if err != nil || replay != receipt {
		t.Fatal("immutable archive receipt", err)
	}
	changed := *req
	changed.ExpectedSourceEpoch++
	_, err = ArchiveSourceMetadata(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
	changed.IdempotencyKey = uuid.NewString()
	_, err = ArchiveSourceMetadata(admin, s, &changed)
	assertAccountCode(t, err, "SOURCE_METADATA_ARCHIVE_PROTECTED")
	if _, err = ArchiveSourceMetadata(operator, s, req); err == nil {
		t.Fatal("operator receipt replay")
	}
	if _, err = ListSourceCopyHistory(operator, s, &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id}); err == nil {
		t.Fatal("operator history")
	}
	if _, err = s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(admin).Session.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveSourceMetadata(admin, s, req); err == nil {
		t.Fatal("revoked receipt replay")
	}
	if _, err = ListSourceCopyHistory(admin, s, &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id}); err == nil {
		t.Fatal("revoked history")
	}
	historyCounts(t, s, req.ObjectId, 0, 1, 1)
}
func TestSourceHistoryRollbackAndProtection(t *testing.T) {
	for _, scenario := range []string{"audit", "receipt", "registered", "cleanup_pending", "missing_retirement", "current_generation", "pending_delivery", "failed_delivery", "wrong_document", "wrong_object", "wrong_version", "stale", "unconfirmed"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, row, store, cleanup, _ := retiredHistoryFixture(t)
			req := sourceMetadataRequest(cleanup)
			var mutation string
			switch scenario {
			case "audit":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "receipt":
				s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
			case "registered", "cleanup_pending":
				mutation = "update business.source_objects set status='" + scenario + "' where id=$1"
			case "missing_retirement":
				mutation = "update business.source_objects set retired_at=null where id=$1"
			case "current_generation":
				mutation = "update business.source_objects set source_epoch=1 where id=$1"
			case "pending_delivery":
				mutation = "update business.outbox_events set status='pending' where payload->>'object_id'=$1"
			case "failed_delivery":
				mutation = "update business.outbox_events set status='dead_letter' where payload->>'object_id'=$1"
			case "wrong_document":
				req.DocumentId = uuid.NewString()
			case "wrong_object":
				req.ObjectId = uuid.NewString()
			case "wrong_version":
				req.VersionId = uuid.NewString()
			case "stale":
				req.ExpectedSourceEpoch++
			case "unconfirmed":
				req.Confirm = false
			}
			if mutation != "" {
				if _, err := s.Conn.Exec(mutation, cleanup.ObjectId); err != nil {
					t.Fatal(err)
				}
			}
			reads, puts, retires := store.reads, store.puts, store.retirements
			if _, err := ArchiveSourceMetadata(admin, s, req); err == nil {
				t.Fatal("unsafe archive", scenario)
			}
			historyCounts(t, s, cleanup.ObjectId, 1, 0, 0)
			if store.reads != reads || store.puts != puts || store.retirements != retires {
				t.Fatal("storage touched")
			}
			if scenario == "audit" || scenario == "receipt" {
				var count int64
				if err := s.Conn.QueryRow(&count, "select count(*) from business.api_idempotency where operation='knowledge.source_metadata_archive'"); err != nil || count != 0 {
					t.Fatal("receipt rollback", err)
				}
			}
			_ = row
		})
	}
}
func TestSourceHistoryKeysetStableAcrossArchiveAndNewRegistration(t *testing.T) {
	s, admin, _, row, _, cleanup, _ := retiredHistoryFixture(t)
	if _, err := s.Conn.Exec(`insert into business.source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size,status,created_at,retired_at)
 select gen_random_uuid(),$1,$2,0,'history-test','history-'||n,$3,$4,'retired','2020-01-01'::timestamptz,'2020-01-02'::timestamptz from generate_series(1,107)n`, row.TenantId, row.Id, row.ContentHash, row.SourceSize); err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		Id string `db:"id"`
	}{}
	if err := s.Conn.QueryRows(&expected, "select id from business.source_objects where version_id=$1 order by created_at desc,id desc", row.Id); err != nil {
		t.Fatal(err)
	}
	request := &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id, Limit: 11}
	first, err := ListSourceCopyHistory(admin, s, request)
	if err != nil || len(first.Items) != 11 || !first.HasMore {
		t.Fatal("first page", err)
	}
	selected := first.Items[len(first.Items)-1]
	req := sourceMetadataRequest(cleanup)
	req.ObjectId = selected.Id
	if _, err = ArchiveSourceMetadata(admin, s, req); err != nil {
		t.Fatal("archive during pagination", err)
	}
	if _, err = s.Conn.Exec(`insert into business.source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size) values($1,$2,$3,1,'history-test','new-after-page',$4,$5)`, uuid.NewString(), row.TenantId, row.Id, row.ContentHash, row.SourceSize); err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	for _, item := range first.Items {
		actual = append(actual, item.Id)
	}
	request.Cursor = first.NextCursor
	for i := 0; request.Cursor != "" && i < 30; i++ {
		page, readErr := ListSourceCopyHistory(admin, s, request)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, item := range page.Items {
			actual = append(actual, item.Id)
		}
		request.Cursor = page.NextCursor
	}
	expectedIDs := []string{}
	for _, item := range expected {
		expectedIDs = append(expectedIDs, item.Id)
	}
	if !slices.Equal(actual, expectedIDs) || len(actual) != 108 {
		t.Fatal("unstable pagination", len(actual))
	}
	for _, invalid := range []types.SourceCopyHistoryReq{
		{DocumentId: row.DocumentId, VersionId: row.Id, Limit: 51}, {DocumentId: row.DocumentId, VersionId: row.Id, Limit: -1},
		{DocumentId: row.DocumentId, VersionId: row.Id, Cursor: "malformed"},
		{DocumentId: row.DocumentId, VersionId: row.Id, Cursor: encodeCursor("foreign-scope", "2020-01-01T00:00:00Z|"+selected.Id)},
	} {
		if _, err = ListSourceCopyHistory(admin, s, &invalid); err == nil {
			t.Fatal("invalid cursor/page accepted")
		}
	}
}
func TestSourceHistoryArchivedAdoptionReplayAndReregistrationGuard(t *testing.T) {
	s, admin, _, row, store, adoption := adoptionFixture(t)
	receipt, err := AdoptSourceObject(admin, s, adoption)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Conn.Exec("update business.source_objects set status='retired',retired_at=now() where id=$1", receipt.ObjectId); err != nil {
		t.Fatal(err)
	}
	req := &types.ArchiveSourceMetadataReq{DocumentId: row.DocumentId, VersionId: row.Id, ObjectId: receipt.ObjectId, Confirm: true, ExpectedSourceEpoch: 1, IdempotencyKey: uuid.NewString()}
	if _, err = ArchiveSourceMetadata(admin, s, req); err != nil {
		t.Fatal(err)
	}
	reads := store.reads
	s.SourceStore = nil
	s.Config.FileStoreAdoptionEnabled = false
	replay, err := AdoptSourceObject(admin, s, adoption)
	if err != nil || replay != receipt || store.reads != reads {
		t.Fatal("archived adoption replay", err)
	}
	original, err := s.SourceObjectsModel.FindOneByTenantIdId(admin, row.TenantId, receipt.ObjectId)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		if _, lockErr := s.DocumentVersionsModel.FindOneForUpdateTx(ctx, tx, row.TenantId, row.Id); lockErr != nil {
			return lockErr
		}
		return registerSourceObjectTx(ctx, s, tx, row, original.StoreId, original.ObjectKey)
	}); err == nil {
		t.Fatal("archived retired locator registered again")
	}
	historyCounts(t, s, receipt.ObjectId, 0, 1, 1)
}

func TestSourceHistoryCurrentAuthorizationAndRevokedReplay(t *testing.T) {
	s, admin, operator, row, _, cleanup, _ := retiredHistoryFixture(t)
	if _, err := s.Conn.Exec(`insert into business.source_objects(id,tenant_id,version_id,source_epoch,store_id,object_key,content_hash,source_size)
 values($1,$2,$3,1,'scope-test','scope-test',$4,$5)`, uuid.NewString(), row.TenantId, row.Id, row.ContentHash, row.SourceSize); err != nil {
		t.Fatal(err)
	}
	historyReq := &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id, Limit: 1}
	if _, err := ListSourceCopyHistory(operator, s, historyReq); err == nil {
		t.Fatal("operator history")
	}
	if _, err := ArchiveSourceMetadata(operator, s, sourceMetadataRequest(cleanup)); err == nil {
		t.Fatal("operator archive")
	}
	roles := managedRoles(t, s, admin)
	role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "History manager", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	account := managedAccount(t, s, admin, roles["operator"], role.Id)
	manager, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	req := sourceMetadataRequest(cleanup)
	if _, err = ArchiveSourceMetadata(manager, s, req); err != nil {
		t.Fatal(err)
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
	baseData, err := SetKnowledgeTeam(admin, s, &types.KnowledgeTeamReq{KnowledgeBaseId: base.Id, TeamId: team.Id, ExpectedRevision: base.AclRevision, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveSourceMetadata(manager, s, req); err == nil {
		t.Fatal("team revoked replay")
	}
	if _, err = ListSourceCopyHistory(manager, s, historyReq); err == nil {
		t.Fatal("team revoked history")
	}
	memberChange(t, s, admin, team, account.Id, true)
	if _, err = ArchiveSourceMetadata(manager, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin"}, ExpectedRevision: baseData.AclRevision}); err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveSourceMetadata(manager, s, req); err == nil {
		t.Fatal("ACL revoked replay")
	}
	if _, err = ListSourceCopyHistory(manager, s, historyReq); err == nil {
		t.Fatal("ACL revoked history")
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ReadRoles: []string{"admin", "operator"}, ExpectedRevision: baseData.AclRevision + 1}); err != nil {
		t.Fatal(err)
	}
	page, err := ListSourceCopyHistory(manager, s, historyReq)
	if err != nil || page.NextCursor == "" {
		t.Fatal(err)
	}
	foreign := *Current(admin).User
	foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "foreign.history"
	if _, err = s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "foreign-history", Name: "Synthetic foreign history"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UsersModel.Insert(admin, &foreign); err != nil {
		t.Fatal(err)
	}
	foreignCtx, _, _ := managedLogin(t, s, foreign.Account, "integration-password")
	if _, err = ListSourceCopyHistory(foreignCtx, s, historyReq); err == nil {
		t.Fatal("cross tenant history")
	}
	if _, err = ArchiveSourceMetadata(foreignCtx, s, req); err == nil {
		t.Fatal("cross tenant archive")
	}
	if _, err = ListSourceCopyHistory(admin, s, &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id, Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor reused by another viewer")
	}
	if _, err = s.Conn.Exec("update business.sessions set revoked_at=now() where id=$1", Current(manager).Session.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = ArchiveSourceMetadata(manager, s, req); err == nil {
		t.Fatal("session revoked replay")
	}
	if _, err = ListSourceCopyHistory(manager, s, historyReq); err == nil {
		t.Fatal("session revoked history")
	}
}

type pausedSourceMetadataAudit struct {
	model.AuditEventsModel
	entered, release chan struct{}
}

func (m pausedSourceMetadataAudit) InsertTx(ctx context.Context, tx sqlx.Session, row *model.AuditEvents) error {
	if row.Operation == "knowledge.source_metadata_archive" {
		close(m.entered)
		<-m.release
	}
	return m.AuditEventsModel.InsertTx(ctx, tx, row)
}
func TestSourceHistorySerializesMoveAndHistory(t *testing.T) {
	s, admin, _, row, _, cleanup, _ := retiredHistoryFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	shadow := *s
	shadow.AuditEventsModel = pausedSourceMetadataAudit{s.AuditEventsModel, entered, release}
	updated := make(chan error, 1)
	go func() { _, e := ArchiveSourceMetadata(admin, &shadow, sourceMetadataRequest(cleanup)); updated <- e }()
	select {
	case <-entered:
	case e := <-updated:
		t.Fatal(e)
	case <-time.After(5 * time.Second):
		t.Fatal("archive lock timeout")
	}
	read := make(chan error, 1)
	go func() {
		page, e := ListSourceCopyHistory(admin, s, &types.SourceCopyHistoryReq{DocumentId: row.DocumentId, VersionId: row.Id})
		if e == nil && !page.Items[len(page.Items)-1].MetadataArchived {
			e = fmt.Errorf("partial archive visible")
		}
		read <- e
	}()
	select {
	case e := <-read:
		close(release)
		<-updated
		t.Fatal("history crossed transaction", e)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("history failed to resume")
	}
}
