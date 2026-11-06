package logic

import (
	"context"
	"errors"
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

func batchRequest(ref string, expected int64, revisions ...int64) *types.ArchiveInvestigationBatchReq {
	return &types.ArchiveInvestigationBatchReq{ObjectRef: ref, ExpectedRevision: expected, Revisions: revisions, Confirm: true, IdempotencyKey: uuid.NewString()}
}
func batchPreview(req *types.ArchiveInvestigationBatchReq) *types.PreviewInvestigationArchiveReq {
	return &types.PreviewInvestigationArchiveReq{ObjectRef: req.ObjectRef, ExpectedRevision: req.ExpectedRevision, Revisions: req.Revisions}
}
func batchCounts(t *testing.T, s *svc.ServiceContext, hot, cold, effects int64) {
	t.Helper()
	for query, want := range map[string]int64{
		"select count(*) from business.source_investigation_revisions":                                                 hot,
		"select count(*) from business.source_investigation_revision_archives":                                         cold,
		"select count(*) from business.audit_events where operation='knowledge.source_investigation_archive_batch'":    effects,
		"select count(*) from business.api_idempotency where operation='knowledge.source_investigation_archive_batch'": effects,
	} {
		var actual int64
		if err := s.Conn.QueryRow(&actual, query); err != nil || actual != want {
			t.Fatal(query, actual, want, err)
		}
	}
}
func TestInvestigationBatchArchivePreviewExactSnapshotsReplayAndColdRestore(t *testing.T) {
	s, admin, _, row, store, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	var originals []model.SourceInvestigationRevisions
	if err = s.Conn.QueryRows(&originals, `select * from business.source_investigation_revisions where revision<=3 order by revision`); err != nil {
		t.Fatal(err)
	}
	request := batchRequest(ref, 10, 1, 2, 3)
	puts, reads, calls := store.puts, store.reads, store.calls
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	preview, err := PreviewInvestigationArchive(admin, s, batchPreview(request))
	if err != nil || preview.CurrentRevision != 10 || preview.MaximumItems != 50 || len(preview.Items) != 3 {
		t.Fatal(preview, err)
	}
	for index, item := range preview.Items {
		source := originals[index]
		if item.Revision != source.Revision || item.Note != source.Note || item.Status != source.Status || item.ChangedBy != source.ChangedBy || item.ChangedAt != source.ChangedAt.UTC().Format(time.RFC3339Nano) || !item.CanArchive || item.MetadataArchived {
			t.Fatal("preview changed source", item)
		}
	}
	batchCounts(t, s, 10, 0, 0)
	receipt, err := ArchiveInvestigationBatch(admin, s, request)
	if err != nil || receipt.ObjectRef != ref || len(receipt.Items) != 3 {
		t.Fatal(receipt, err)
	}
	var archived []model.SourceInvestigationRevisionArchives
	if err = s.Conn.QueryRows(&archived, `select * from business.source_investigation_revision_archives order by revision`); err != nil {
		t.Fatal(err)
	}
	for index, archive := range archived {
		original := originals[index]
		if archive.Id != original.Id || archive.TenantId != original.TenantId || archive.InvestigationId != original.InvestigationId || archive.Revision != original.Revision || archive.Status != original.Status || archive.Note != original.Note || archive.ChangedBy != original.ChangedBy || !archive.ChangedAt.Equal(original.ChangedAt) || archive.IsBaseline != original.IsBaseline || receipt.Items[index].Revision != original.Revision || receipt.Items[index].ArchivedAt != archive.ArchivedAt.UTC().Format(time.RFC3339Nano) {
			t.Fatal("archive changed source", archive)
		}
	}
	actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil || marshal(actual) != marshal(current) {
		t.Fatal("batch changed current", actual, err)
	}
	restored, err := RestoreInvestigationRevision(admin, s, restoreRequest(ref, 2, 10))
	if err != nil || restored.Investigation.Note != originals[1].Note || restored.Investigation.Revision != 11 {
		t.Fatal(restored, err)
	}
	latest := historyUpdate(t, s, admin, ref, 11)
	replay, err := ArchiveInvestigationBatch(admin, s, request)
	if err != nil || marshal(replay) != marshal(receipt) {
		t.Fatal("batch receipt rewritten", replay, err)
	}
	actual, err = GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil || marshal(actual) != marshal(latest) {
		t.Fatal("replay overwrote edit", err)
	}
	var summaries []string
	if err = s.Conn.QueryRows(&summaries, `select summary from business.audit_events where operation in ($1,$2)`, investigationArchiveBatchOperation, investigationArchivePreviewOperation); err != nil || len(summaries) != 2 {
		t.Fatal(summaries, err)
	}
	for _, private := range []string{originals[0].Note, sourceKey(row), store.ID(), ref} {
		if strings.Contains(marshal(summaries), private) {
			t.Fatal("audit leaked private content")
		}
	}
	if store.puts != puts || store.reads != reads || store.calls != calls {
		t.Fatal("batch used S3")
	}
	batchCounts(t, s, 9, 3, 1)
	changed := *request
	changed.Revisions = []int64{1, 2}
	_, err = ArchiveInvestigationBatch(admin, s, &changed)
	assertAccountCode(t, err, "IDEMPOTENCY_CONFLICT")
}
func TestInvestigationBatchArchiveValidationAndAllOrNothingProtection(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	for _, scenario := range []string{"empty", "duplicate", "descending", "zero", "negative", "over_limit", "bad_ref", "zero_expected", "stale", "missing", "unknown", "protected"} {
		t.Run(scenario, func(t *testing.T) {
			req := batchRequest(ref, 10, 1, 2)
			code := "INVALID_ARGUMENT"
			switch scenario {
			case "empty":
				req.Revisions = nil
			case "duplicate":
				req.Revisions = []int64{1, 1}
			case "descending":
				req.Revisions = []int64{2, 1}
			case "zero":
				req.Revisions = []int64{0, 1}
			case "negative":
				req.Revisions = []int64{-1, 1}
			case "over_limit":
				for value := int64(3); value <= 51; value++ {
					req.Revisions = append(req.Revisions, value)
				}
			case "bad_ref":
				req.ObjectRef = "bad"
			case "zero_expected":
				req.ExpectedRevision = 0
			case "stale":
				req.ExpectedRevision = 9
				code = "INVESTIGATION_CHANGED"
			case "missing":
				req.Revisions = []int64{1, 99}
				code = "RESOURCE_NOT_FOUND"
			case "unknown":
				req.ObjectRef = strings.Repeat("f", 64)
				code = "RESOURCE_NOT_FOUND"
			case "protected":
				req.Revisions = []int64{1, 8}
				code = "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
			}
			preview, err := PreviewInvestigationArchive(admin, s, batchPreview(req))
			assertAccountCode(t, err, code)
			if preview.ObjectRef != "" || len(preview.Items) > 0 {
				t.Fatal("partial rejected preview")
			}
			receipt, err := ArchiveInvestigationBatch(admin, s, req)
			assertAccountCode(t, err, code)
			if receipt.ObjectRef != "" || len(receipt.Items) > 0 {
				t.Fatal("partial rejected batch")
			}
			batchCounts(t, s, 10, 0, 0)
		})
	}
	for _, fault := range []string{"unconfirmed", "bad_key"} {
		req := batchRequest(ref, 10, 1, 2)
		if fault == "unconfirmed" {
			req.Confirm = false
		} else {
			req.IdempotencyKey = "bad"
		}
		_, err := ArchiveInvestigationBatch(admin, s, req)
		assertAccountCode(t, err, "INVALID_ARGUMENT")
	}
}

type batchInsertFault struct {
	model.SourceInvestigationRevisionArchivesModel
	identitySession string
}

func (m batchInsertFault) InsertSnapshotTx(ctx context.Context, tx sqlx.Session, row *model.SourceInvestigationRevisions, actor string) error {
	if row.Revision == 2 && m.identitySession == "" {
		return errors.New("synthetic second archive insert failure")
	}
	if err := m.SourceInvestigationRevisionArchivesModel.InsertSnapshotTx(ctx, tx, row, actor); err != nil {
		return err
	}
	if row.Revision == 2 && m.identitySession != "" {
		_, err := tx.ExecCtx(ctx, `update business.sessions set revoked_at=now() where id=$1`, m.identitySession)
		return err
	}
	return nil
}

type batchDeleteFault struct {
	model.SourceInvestigationRevisionsModel
	noop bool
}

func (m batchDeleteFault) DeleteArchivedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64) error {
	if revision == 2 {
		if m.noop {
			return nil
		}
		return errors.New("synthetic second archive delete failure")
	}
	return m.SourceInvestigationRevisionsModel.DeleteArchivedTx(ctx, tx, tenant, id, revision)
}
func TestInvestigationBatchArchiveRollbackAfterEarlierRowsAndAtCommit(t *testing.T) {
	for _, fault := range []string{"second_insert", "second_delete", "commit", "audit", "receipt", "identity_after_second"} {
		t.Run(fault, func(t *testing.T) {
			s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			switch fault {
			case "second_insert":
				s.SourceInvestigationRevisionArchivesModel = batchInsertFault{s.SourceInvestigationRevisionArchivesModel, ""}
			case "identity_after_second":
				s.SourceInvestigationRevisionArchivesModel = batchInsertFault{s.SourceInvestigationRevisionArchivesModel, Current(admin).Session.Id}
			case "second_delete", "commit":
				s.SourceInvestigationRevisionsModel = batchDeleteFault{s.SourceInvestigationRevisionsModel, fault == "commit"}
			case "audit":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "receipt":
				s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
			}
			receipt, err := ArchiveInvestigationBatch(admin, s, batchRequest(ref, 10, 1, 2, 3))
			if err == nil || receipt.ObjectRef != "" || len(receipt.Items) > 0 {
				t.Fatal("partial batch", receipt, err)
			}
			batchCounts(t, s, 10, 0, 0)
			current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
			if err != nil || current.Revision != 10 || current.Note != "PRIVATE_ARCHIVE_10" {
				t.Fatal("rollback escaped", current, err)
			}
		})
	}
}

type revokedBatchSelection struct {
	model.SourceInvestigationRevisionsModel
	session string
}

func (m revokedBatchSelection) ListSelectedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revisions []int64) ([]model.SourceInvestigationRevisions, error) {
	rows, err := m.SourceInvestigationRevisionsModel.ListSelectedTx(ctx, tx, tenant, id, revisions)
	if err == nil {
		_, err = tx.ExecCtx(ctx, `update business.sessions set revoked_at=now() where id=$1`, m.session)
	}
	return rows, err
}
func TestInvestigationBatchArchivePreviewFailureReturnsNoContent(t *testing.T) {
	for _, fault := range []string{"identity", "audit"} {
		t.Run(fault, func(t *testing.T) {
			s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			if fault == "identity" {
				s.SourceInvestigationRevisionsModel = revokedBatchSelection{s.SourceInvestigationRevisionsModel, Current(admin).Session.Id}
			} else {
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			}
			preview, err := PreviewInvestigationArchive(admin, s, batchPreview(batchRequest(ref, 10, 1, 2)))
			if err == nil || preview.ObjectRef != "" || len(preview.Items) > 0 {
				t.Fatal("partial preview", preview, err)
			}
			batchCounts(t, s, 10, 0, 0)
			var count int64
			if err = s.Conn.QueryRow(&count, `select count(*) from business.audit_events where operation=$1`, investigationArchivePreviewOperation); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		})
	}
}
func TestInvestigationBatchArchiveStalePreviewAndOperationKeyScope(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	request := batchRequest(ref, 10, 1, 2)
	if _, err := PreviewInvestigationArchive(admin, s, batchPreview(request)); err != nil {
		t.Fatal(err)
	}
	single := archiveRequest(ref, 2, 10)
	if _, err := ArchiveInvestigationRevision(admin, s, single); err != nil {
		t.Fatal(err)
	}
	receipt, err := ArchiveInvestigationBatch(admin, s, request)
	assertAccountCode(t, err, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
	if len(receipt.Items) > 0 {
		t.Fatal("partial stale batch")
	}
	batchCounts(t, s, 9, 1, 0)
	// A literal key remains scoped to its operation; a changed selection is still a conflict within a batch.
	request.Revisions = []int64{1, 3}
	request.IdempotencyKey = single.IdempotencyKey
	if _, err = ArchiveInvestigationBatch(admin, s, request); err != nil {
		t.Fatal(err)
	}
	batchCounts(t, s, 7, 3, 1)
}
func TestInvestigationBatchArchiveConcurrentBatchesSinglesAndEditors(t *testing.T) {
	for _, scenario := range []string{"same_key", "overlap", "disjoint", "single", "edit", "restore"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			request := batchRequest(ref, 10, 1, 2)
			other := *request
			if scenario != "same_key" {
				other.IdempotencyKey = uuid.NewString()
			}
			if scenario == "overlap" {
				other.Revisions = []int64{2, 3}
			}
			if scenario == "disjoint" {
				other.Revisions = []int64{3, 4}
			}
			start := make(chan struct{})
			var group sync.WaitGroup
			group.Add(2)
			failures := make([]error, 2)
			receipts := make([]types.InvestigationArchiveBatchData, 2)
			go func() {
				defer group.Done()
				<-start
				receipts[0], failures[0] = ArchiveInvestigationBatch(admin, s, request)
			}()
			go func() {
				defer group.Done()
				<-start
				switch scenario {
				case "single":
					_, failures[1] = ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 2, 10))
				case "edit":
					_, failures[1] = SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: 10, Status: investigationClosed, Note: "Concurrent batch edit", Confirm: true, IdempotencyKey: uuid.NewString()})
				case "restore":
					_, failures[1] = RestoreInvestigationRevision(admin, s, restoreRequest(ref, 1, 10))
				default:
					receipts[1], failures[1] = ArchiveInvestigationBatch(admin, s, &other)
				}
			}()
			close(start)
			group.Wait()
			hot, cold, effects := int64(8), int64(2), int64(1)
			switch scenario {
			case "same_key":
				if failures[0] != nil || failures[1] != nil || marshal(receipts[0]) != marshal(receipts[1]) {
					t.Fatal(receipts, failures)
				}
			case "overlap":
				if (failures[0] == nil) == (failures[1] == nil) {
					t.Fatal(failures)
				}
				for _, err := range failures {
					if err != nil {
						assertAccountCode(t, err, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
					}
				}
			case "disjoint":
				hot, cold, effects = 6, 4, 2
				if failures[0] != nil || failures[1] != nil {
					t.Fatal(failures)
				}
			case "single":
				if (failures[0] == nil) == (failures[1] == nil) {
					t.Fatal(failures)
				}
				if failures[0] != nil {
					assertAccountCode(t, failures[0], "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
					hot, cold, effects = 9, 1, 0
				} else {
					assertAccountCode(t, failures[1], "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
				}
			default:
				if failures[1] != nil {
					t.Fatal(failures)
				}
				hot = 9
				if failures[0] != nil {
					assertAccountCode(t, failures[0], "INVESTIGATION_CHANGED")
					hot, cold, effects = 11, 0, 0
				}
			}
			batchCounts(t, s, hot, cold, effects)
		})
	}
}
func TestInvestigationBatchArchiveMaximumSizeAndHistoryPagination(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 60, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	selection := []int64{}
	for revision := int64(1); revision <= 50; revision++ {
		selection = append(selection, revision)
	}
	request := batchRequest(ref, 60, selection...)
	preview, err := PreviewInvestigationArchive(admin, s, batchPreview(request))
	if err != nil || len(preview.Items) != 50 {
		t.Fatal(err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref, Limit: 7})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ArchiveInvestigationBatch(admin, s, request)
	if err != nil || len(receipt.Items) != 50 {
		t.Fatal(err)
	}
	var actual []int64
	for index := 0; index < 10; index++ {
		for _, item := range page.Items {
			actual = append(actual, item.Revision)
			if item.Revision <= 50 && !item.MetadataArchived {
				t.Fatal("missing archive marker")
			}
		}
		if !page.HasMore {
			break
		}
		page, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref, Limit: 7, Cursor: page.NextCursor})
		if err != nil || page.SnapshotRevision != 60 || page.FirstRecordedRevision != 1 {
			t.Fatal(page, err)
		}
	}
	if len(actual) != 60 {
		t.Fatal(len(actual))
	}
	for index, revision := range actual {
		if revision != int64(60-index) {
			t.Fatal("missing/duplicate", actual)
		}
	}
	batchCounts(t, s, 10, 50, 1)
}
func TestInvestigationBatchArchiveCurrentAuthorizationProtectsPreviewAndOldReceipt(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "disabled", "foreign_tenant", "acl"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			request := batchRequest(ref, 10, 1, 2)
			if _, err := ArchiveInvestigationBatch(admin, s, request); err != nil {
				t.Fatal(err)
			}
			actor := admin
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Batch archive delegate", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
				if err != nil {
					t.Fatal(err)
				}
				account := managedAccount(t, s, admin, roles["operator"], role.Id)
				actor, _, _ = managedLogin(t, s, account.Account, managedTestPassword)
			case "revoked":
				if _, err := s.Conn.Exec(`update business.sessions set revoked_at=now() where id=$1`, Current(admin).Session.Id); err != nil {
					t.Fatal(err)
				}
			case "role_changed":
				if _, err := s.Conn.Exec(`update business.users set role='operator' where id=$1`, Current(admin).User.Id); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if _, err := s.Conn.Exec(`update business.users set enabled=false where id=$1`, Current(admin).User.Id); err != nil {
					t.Fatal(err)
				}
			case "foreign_tenant":
				foreign := *Current(admin).User
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "batch.foreign"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "batch-foreign", Name: "Synthetic"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UsersModel.Insert(admin, &foreign); err != nil {
					t.Fatal(err)
				}
				actor, _, _ = managedLogin(t, s, foreign.Account, "integration-password")
			case "acl":
				if _, err := s.Conn.Exec(`update business.knowledge_bases set read_roles='["operator"]',acl_revision=acl_revision+1 where tenant_id=$1`, row.TenantId); err != nil {
					t.Fatal(err)
				}
			}
			selection := batchRequest(ref, 10, 3, 4)
			preview, err := PreviewInvestigationArchive(actor, s, batchPreview(selection))
			if err == nil || len(preview.Items) > 0 || preview.ObjectRef != "" {
				t.Fatal("unauthorized preview", err)
			}
			for _, req := range []*types.ArchiveInvestigationBatchReq{request, selection} {
				receipt, err := ArchiveInvestigationBatch(actor, s, req)
				if err == nil || receipt.ObjectRef != "" || len(receipt.Items) > 0 {
					t.Fatal("unauthorized receipt", receipt, err)
				}
			}
			batchCounts(t, s, 8, 2, 1)
		})
	}
}

// Observe the real parent lock while an earlier row has moved but the batch has not committed.
type pausedBatchDelete struct {
	model.SourceInvestigationRevisionsModel
	entered, release chan struct{}
	once             *sync.Once
}

func (m pausedBatchDelete) DeleteArchivedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64) error {
	if err := m.SourceInvestigationRevisionsModel.DeleteArchivedTx(ctx, tx, tenant, id, revision); err != nil {
		return err
	}
	m.once.Do(func() {
		close(m.entered)
		select {
		case <-m.release:
		case <-ctx.Done():
		}
	})
	return ctx.Err()
}
func TestInvestigationBatchArchiveReaderCannotObservePartialBatch(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 10, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithTimeout(admin, 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.SourceInvestigationRevisionsModel = pausedBatchDelete{s.SourceInvestigationRevisionsModel, entered, release, &sync.Once{}}
	written := make(chan error, 1)
	go func() { _, err := ArchiveInvestigationBatch(ctx, s, batchRequest(ref, 10, 1, 2)); written <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	read := make(chan error, 1)
	go func() {
		page, err := ListInvestigationHistory(ctx, s, &types.InvestigationHistoryReq{ObjectRef: ref})
		if err == nil && (len(page.Items) != 10 || !page.Items[8].MetadataArchived || !page.Items[9].MetadataArchived) {
			err = fmt.Errorf("partial batch was exposed")
		}
		read <- err
	}()
	waiting := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int64
		if err := s.Conn.QueryRow(&count, `select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock' and query ilike '%for share%'`); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			waiting = true
			break
		}
		select {
		case err := <-read:
			t.Fatal("reader did not wait", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !waiting {
		t.Fatal("no reader lock wait observed")
	}
	once.Do(func() { close(release) })
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}
