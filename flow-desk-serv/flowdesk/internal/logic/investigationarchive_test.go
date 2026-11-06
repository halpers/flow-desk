package logic

import (
	"context"
	"database/sql"
	"errors"
	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"fmt"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
	"golang.org/x/crypto/bcrypt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func archiveRequest(ref string, revision, expected int64) *types.ArchiveInvestigationRevisionReq {
	return &types.ArchiveInvestigationRevisionReq{ObjectRef: ref, Revision: revision, ExpectedRevision: expected, Confirm: true, IdempotencyKey: uuid.NewString()}
}

// Synthetic old edits exercise the real capture trigger; immutable history rows are never patched.
func archiveFixture(t *testing.T, count int64, changed time.Time) (*svc.ServiceContext, context.Context, context.Context, *model.DocumentVersions, *tenantInspectingSources, string) {
	t.Helper()
	s, admin, operator, row, store := tenantInventoryFixture(t)
	ref := HashToken(store.ID() + "\n" + sourceKey(row))
	actor := Current(admin).User
	if _, err := s.Conn.Exec(`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,created_at,updated_at) values($1,$2,$3,$4,$5,'closed','PRIVATE_ARCHIVE_1',1,$6,$6,$7,$7)`, uuid.NewString(), actor.TenantId, ref, store.ID(), sourceKey(row), actor.Id, changed); err != nil {
		t.Fatal(err)
	}
	for revision := int64(2); revision <= count; revision++ {
		if _, err := s.Conn.Exec(`update business.source_investigations set revision=$1,note=$2,updated_at=$3 where tenant_id=$4 and object_ref=$5`, revision, fmt.Sprintf("PRIVATE_ARCHIVE_%d", revision), changed.Add(time.Duration(revision)*time.Minute), actor.TenantId, ref); err != nil {
			t.Fatal(err)
		}
	}
	return s, admin, operator, row, store, ref
}
func archiveCounts(t *testing.T, s *svc.ServiceContext, hot, cold, effects int64) {
	t.Helper()
	for query, want := range map[string]int64{
		"select count(*) from business.source_investigation_revisions":                                           hot,
		"select count(*) from business.source_investigation_revision_archives":                                   cold,
		"select count(*) from business.audit_events where operation='knowledge.source_investigation_archive'":    effects,
		"select count(*) from business.api_idempotency where operation='knowledge.source_investigation_archive'": effects,
	} {
		var count int64
		if err := s.Conn.QueryRow(&count, query); err != nil || count != want {
			t.Fatal(query, count, want, err)
		}
	}
}
func TestInvestigationArchivePreservesSnapshotCurrentStateAndColdRestore(t *testing.T) {
	s, admin, _, row, store, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	var original model.SourceInvestigationRevisions
	if err = s.Conn.QueryRow(&original, `select * from business.source_investigation_revisions where revision=1`); err != nil {
		t.Fatal(err)
	}
	roles := managedRoles(t, s, admin)
	account := managedAccount(t, s, admin, roles["admin"])
	archiver, _, _ := managedLogin(t, s, account.Account, managedTestPassword)
	puts, reads, calls := store.puts, store.reads, store.calls
	s.SourceStore = nil
	s.Config.FileStoreInventoryEnabled = false
	s.Config.FileStoreTenantInventoryEnabled = false
	request := archiveRequest(ref, 1, 6)
	receipt, err := ArchiveInvestigationRevision(archiver, s, request)
	if err != nil || receipt.Revision != 1 || receipt.ObjectRef != ref {
		t.Fatal(receipt, err)
	}
	var archived model.SourceInvestigationRevisionArchives
	if err = s.Conn.QueryRow(&archived, `select * from business.source_investigation_revision_archives`); err != nil {
		t.Fatal(err)
	}
	if archived.Id != original.Id || archived.TenantId != original.TenantId || archived.InvestigationId != original.InvestigationId || archived.Revision != original.Revision || archived.Note != original.Note || archived.Status != original.Status || archived.ChangedBy != original.ChangedBy || !archived.ChangedAt.Equal(original.ChangedAt) || archived.IsBaseline != original.IsBaseline || archived.ArchivedBy != Current(archiver).User.Id || archived.ArchivedAt.Before(time.Now().Add(-time.Minute)) {
		t.Fatal("snapshot was rewritten", archived)
	}
	actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
	if err != nil || marshal(actual) != marshal(current) {
		t.Fatal("current changed", actual, err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || len(page.Items) != 6 || page.FirstRecordedRevision != 1 || !page.Items[5].MetadataArchived || page.Items[5].CanArchive || page.Items[5].ArchiveBlockReason != "already_archived" || page.Items[5].Note != original.Note || page.Items[5].ArchivedAt != receipt.ArchivedAt {
		t.Fatal(page, err)
	}
	restored, err := RestoreInvestigationRevision(archiver, s, restoreRequest(ref, 1, 6))
	if err != nil || restored.Investigation.Revision != 7 || restored.Investigation.Note != original.Note || restored.Investigation.UpdatedBy != Current(archiver).User.Id {
		t.Fatal(restored, err)
	}
	replay, err := ArchiveInvestigationRevision(archiver, s, request)
	if err != nil || marshal(replay) != marshal(receipt) {
		t.Fatal("receipt changed", replay, err)
	}
	_, err = ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 1, 7))
	assertAccountCode(t, err, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
	var summaries []string
	if err = s.Conn.QueryRows(&summaries, `select summary from business.audit_events where operation=$1`, investigationArchiveOperation); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{original.Note, sourceKey(row), store.ID(), ref} {
		if strings.Contains(marshal(summaries), private) {
			t.Fatal("archive audit leaked content")
		}
	}
	var registered int64
	if err = s.Conn.QueryRow(&registered, "select count(*) from business.source_objects"); err != nil || registered != 1 || store.puts != puts || store.reads != reads || store.calls != calls {
		t.Fatal("storage changed", err)
	}
	archiveCounts(t, s, 6, 1, 1)
}
func TestInvestigationArchiveDatabaseGuardsAndDeferredCompletion(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	for _, query := range []string{
		`update business.source_investigation_revisions set note='changed' where revision=1`,
		`delete from business.source_investigation_revisions where revision=1`,
		`truncate business.source_investigation_revisions`,
		`insert into business.source_investigation_revision_archives select id,tenant_id,investigation_id,revision,status,'fabricated',changed_by,changed_at,is_baseline,changed_by,now() from business.source_investigation_revisions where revision=1`,
	} {
		if _, err := s.Conn.Exec(query); err == nil {
			t.Fatal("guard accepted mutation", query)
		}
	}
	err := s.Conn.TransactCtx(admin, func(ctx context.Context, tx sqlx.Session) error {
		_, err := tx.ExecCtx(ctx, `insert into business.source_investigation_revision_archives select *,changed_by,now() from business.source_investigation_revisions where revision=1`)
		return err
	})
	if err == nil {
		t.Fatal("standalone copy committed")
	}
	archiveCounts(t, s, 6, 0, 0)
	if _, err = ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 1, 6)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`update business.source_investigation_revision_archives set note='changed'`, `delete from business.source_investigation_revision_archives`, `truncate business.source_investigation_revision_archives`} {
		if _, err = s.Conn.Exec(query); err == nil {
			t.Fatal("cold mutation accepted", query)
		}
	}
	archiveCounts(t, s, 5, 1, 1)
}
func TestInvestigationArchiveProtectionValidationAndPreview(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	for _, scenario := range []string{"bad_ref", "zero_revision", "zero_expected", "unconfirmed", "bad_key", "latest", "second_latest", "third_latest", "stale", "missing", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			req := archiveRequest(ref, 1, 6)
			code := "INVALID_ARGUMENT"
			switch scenario {
			case "bad_ref":
				req.ObjectRef = "bad"
			case "zero_revision":
				req.Revision = 0
			case "zero_expected":
				req.ExpectedRevision = 0
			case "unconfirmed":
				req.Confirm = false
			case "bad_key":
				req.IdempotencyKey = "bad"
			case "latest":
				req.Revision = 6
				code = "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
			case "second_latest":
				req.Revision = 5
				code = "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
			case "third_latest":
				req.Revision = 4
				code = "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED"
			case "stale":
				req.ExpectedRevision = 5
				code = "INVESTIGATION_CHANGED"
			case "missing":
				req.Revision = 8
				code = "RESOURCE_NOT_FOUND"
			case "unknown":
				req.ObjectRef = strings.Repeat("f", 64)
				code = "RESOURCE_NOT_FOUND"
			}
			result, err := ArchiveInvestigationRevision(admin, s, req)
			assertAccountCode(t, err, code)
			if result.ObjectRef != "" {
				t.Fatal("partial receipt")
			}
		})
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || !page.Items[3].CanArchive || page.Items[0].ArchiveBlockReason != "keep_latest" {
		t.Fatal(page, err)
	}
	for _, status := range []string{investigationOpen, investigationActive} {
		current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
		if err != nil {
			t.Fatal(err)
		}
		saved, err := SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: current.Revision, Status: status, Note: "Still active", Confirm: true, IdempotencyKey: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		_, err = ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 1, saved.Revision))
		assertAccountCode(t, err, "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED")
	}
	archiveCounts(t, s, 8, 0, 0)
	for _, age := range []time.Duration{29 * 24 * time.Hour, 30*24*time.Hour + time.Hour} {
		t.Run(fmt.Sprint(age), func(t *testing.T) {
			fixture, actor, _, _, _, object := archiveFixture(t, 6, time.Now().Add(-age))
			page, err := ListInvestigationHistory(actor, fixture, &types.InvestigationHistoryReq{ObjectRef: object})
			if err != nil {
				t.Fatal(err)
			}
			result, err := ArchiveInvestigationRevision(actor, fixture, archiveRequest(object, 1, 6))
			if age < 30*24*time.Hour {
				assertAccountCode(t, err, "INVESTIGATION_HISTORY_ARCHIVE_PROTECTED")
				if result.ObjectRef != "" || page.Items[5].ArchiveBlockReason != "minimum_age" {
					t.Fatal(page)
				}
			} else if err != nil || !page.Items[5].CanArchive {
				t.Fatal(result, err)
			}
		})
	}
}

type archiveFault struct {
	model.SourceInvestigationRevisionArchivesModel
	mode, session string
}

func (m archiveFault) InsertSnapshotTx(ctx context.Context, tx sqlx.Session, row *model.SourceInvestigationRevisions, actor string) error {
	if m.mode == "insert" {
		return errors.New("synthetic insert failure")
	}
	if err := m.SourceInvestigationRevisionArchivesModel.InsertSnapshotTx(ctx, tx, row, actor); err != nil {
		return err
	}
	if m.mode == "identity" {
		_, err := tx.ExecCtx(ctx, `update business.sessions set revoked_at=now() where id=$1`, m.session)
		return err
	}
	return nil
}

type archiveDeleteFault struct {
	model.SourceInvestigationRevisionsModel
	noop bool
}

func (m archiveDeleteFault) DeleteArchivedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64) error {
	if m.noop {
		return nil
	}
	return errors.New("synthetic delete failure")
}
func TestInvestigationArchiveAtomicRollbackIncludingCommitFailure(t *testing.T) {
	for _, fault := range []string{"insert", "delete", "deferred_commit", "audit", "receipt", "identity"} {
		t.Run(fault, func(t *testing.T) {
			s, admin, _, _, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			current, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
			if err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "insert", "identity":
				s.SourceInvestigationRevisionArchivesModel = archiveFault{s.SourceInvestigationRevisionArchivesModel, fault, Current(admin).Session.Id}
			case "delete", "deferred_commit":
				s.SourceInvestigationRevisionsModel = archiveDeleteFault{s.SourceInvestigationRevisionsModel, fault == "deferred_commit"}
			case "audit":
				s.AuditEventsModel = failingAccountAudit{s.AuditEventsModel}
			case "receipt":
				s.ApiIdempotencyModel = failingAdoptionReceipt{s.ApiIdempotencyModel}
			}
			receipt, err := ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 1, 6))
			if err == nil || receipt.ObjectRef != "" || receipt.Revision != 0 || receipt.ArchivedAt != "" {
				t.Fatal("partial failed receipt", receipt, err)
			}
			actual, err := GetSourceInvestigation(admin, s, &types.SourceInvestigationReq{ObjectRef: ref})
			if err != nil || marshal(actual) != marshal(current) {
				t.Fatal("current escaped rollback", err)
			}
			archiveCounts(t, s, 6, 0, 0)
		})
	}
}
func TestInvestigationArchiveConcurrentEditorsAndRetries(t *testing.T) {
	for _, scenario := range []string{"same_key", "same_target", "different_targets", "edit", "restore"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, _, _, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			req := archiveRequest(ref, 1, 6)
			other := *req
			if scenario != "same_key" {
				other.IdempotencyKey = uuid.NewString()
			}
			if scenario == "different_targets" {
				other.Revision = 2
			}
			start := make(chan struct{})
			var group sync.WaitGroup
			group.Add(2)
			failures := make([]error, 2)
			receipts := make([]types.InvestigationArchiveData, 2)
			go func() {
				defer group.Done()
				<-start
				receipts[0], failures[0] = ArchiveInvestigationRevision(admin, s, req)
			}()
			go func() {
				defer group.Done()
				<-start
				switch scenario {
				case "edit":
					_, failures[1] = SaveSourceInvestigation(admin, s, &types.SaveSourceInvestigationReq{ObjectRef: ref, ExpectedRevision: 6, Status: investigationClosed, Note: "Concurrent edit", Confirm: true, IdempotencyKey: uuid.NewString()})
				case "restore":
					_, failures[1] = RestoreInvestigationRevision(admin, s, restoreRequest(ref, 1, 6))
				default:
					receipts[1], failures[1] = ArchiveInvestigationRevision(admin, s, &other)
				}
			}()
			close(start)
			group.Wait()
			expectedHot, expectedCold := int64(5), int64(1)
			switch scenario {
			case "same_key":
				if failures[0] != nil || failures[1] != nil || marshal(receipts[0]) != marshal(receipts[1]) {
					t.Fatal(receipts, failures)
				}
			case "same_target":
				if (failures[0] == nil) == (failures[1] == nil) {
					t.Fatal(failures)
				}
				for _, err := range failures {
					if err != nil {
						assertAccountCode(t, err, "INVESTIGATION_HISTORY_ALREADY_ARCHIVED")
					}
				}
			case "different_targets":
				expectedHot, expectedCold = 4, 2
				if failures[0] != nil || failures[1] != nil {
					t.Fatal(failures)
				}
			default:
				if failures[1] != nil {
					t.Fatal(failures)
				}
				expectedHot = 6
				if failures[0] != nil {
					assertAccountCode(t, failures[0], "INVESTIGATION_CHANGED")
					expectedHot, expectedCold = 7, 0
				}
			}
			archiveCounts(t, s, expectedHot, expectedCold, expectedCold)
		})
	}
}
func TestInvestigationArchiveStablePagingAcrossTierMoves(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 113, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref, Limit: 9})
	if err != nil {
		t.Fatal(err)
	}
	position, err := openInventoryCursor(s.Config.JwtSecret, investigationHistoryScope(Current(admin).User, ref), page.NextCursor, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, revision := range []int64{1, 20, 105} {
		if _, err = ArchiveInvestigationRevision(admin, s, archiveRequest(ref, revision, 113)); err != nil {
			t.Fatal(err)
		}
	}
	historyUpdate(t, s, admin, ref, 113)
	var revisions []int64
	for n := 0; n < 20; n++ {
		if page.SnapshotRevision != 113 || page.FirstRecordedRevision != 1 {
			t.Fatal("moving page bound")
		}
		for _, item := range page.Items {
			revisions = append(revisions, item.Revision)
		}
		if !page.HasMore {
			break
		}
		next, err := openInventoryCursor(s.Config.JwtSecret, investigationHistoryScope(Current(admin).User, ref), page.NextCursor, 0)
		if err != nil || next.Expires != position.Expires {
			t.Fatal("extended cursor", err)
		}
		page, err = ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref, Limit: 9, Cursor: page.NextCursor})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(revisions) != 113 {
		t.Fatal("missing rows", len(revisions))
	}
	for i, revision := range revisions {
		if revision != int64(113-i) {
			t.Fatal("duplicate/missing revision", revisions)
		}
	}
	archiveCounts(t, s, 111, 3, 3)
}

type pausedArchiveDelete struct {
	model.SourceInvestigationRevisionsModel
	entered, release chan struct{}
}

func (m pausedArchiveDelete) DeleteArchivedTx(ctx context.Context, tx sqlx.Session, tenant, id string, revision int64) error {
	if err := m.SourceInvestigationRevisionsModel.DeleteArchivedTx(ctx, tx, tenant, id, revision); err != nil {
		return err
	}
	close(m.entered)
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestInvestigationArchiveHistoryReadWaitsForCompleteMove(t *testing.T) {
	s, admin, _, _, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx, cancel := context.WithTimeout(admin, 10*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	s.SourceInvestigationRevisionsModel = pausedArchiveDelete{s.SourceInvestigationRevisionsModel, entered, release}
	archiveDone := make(chan error, 1)
	go func() { _, err := ArchiveInvestigationRevision(ctx, s, archiveRequest(ref, 1, 6)); archiveDone <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	readDone := make(chan error, 1)
	go func() {
		page, err := ListInvestigationHistory(ctx, s, &types.InvestigationHistoryReq{ObjectRef: ref})
		if err == nil && (len(page.Items) != 6 || !page.Items[5].MetadataArchived) {
			err = errors.New("partial tier move exposed")
		}
		readDone <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
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
		case err := <-readDone:
			t.Fatal("reader did not wait", err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	if !waiting {
		t.Fatal("no blocked shared-lock reader observed")
	}
	once.Do(func() { close(release) })
	if err := <-archiveDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
}
func TestInvestigationArchiveCurrentAuthorizationAndColdReceiptRecheck(t *testing.T) {
	for _, scenario := range []string{"operator", "delegated", "revoked", "role_changed", "disabled", "foreign_tenant", "acl"} {
		t.Run(scenario, func(t *testing.T) {
			s, admin, operator, row, _, ref := archiveFixture(t, 6, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			original := archiveRequest(ref, 1, 6)
			if _, err := ArchiveInvestigationRevision(admin, s, original); err != nil {
				t.Fatal(err)
			}
			actor := admin
			switch scenario {
			case "operator":
				actor = operator
			case "delegated":
				roles := managedRoles(t, s, admin)
				role, err := CreateManagedRole(admin, s, &types.CreateManagedRoleReq{Name: "Archive delegate", Permissions: []string{"knowledge:manage"}, IdempotencyKey: uuid.NewString()})
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
				foreign.Id, foreign.TenantId, foreign.Account = uuid.NewString(), uuid.NewString(), "archive.foreign"
				if _, err := s.TenantsModel.Insert(admin, &model.Tenants{Id: foreign.TenantId, Code: "archive-foreign", Name: "Synthetic"}); err != nil {
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
			for _, req := range []*types.ArchiveInvestigationRevisionReq{original, archiveRequest(ref, 2, 6)} {
				result, err := ArchiveInvestigationRevision(actor, s, req)
				if err == nil || result.ObjectRef != "" || result.Revision != 0 {
					t.Fatal("unauthorized new/archive receipt", result, err)
				}
			}
			history, err := ListInvestigationHistory(actor, s, &types.InvestigationHistoryReq{ObjectRef: ref})
			if err == nil || len(history.Items) > 0 {
				t.Fatal("unauthorized cold history", err)
			}
			restored, err := RestoreInvestigationRevision(actor, s, restoreRequest(ref, 1, 6))
			if err == nil || restored.Investigation.Note != "" {
				t.Fatal("unauthorized cold restore", err)
			}
			archiveCounts(t, s, 5, 1, 1)
		})
	}
}
func TestInvestigationArchiveLegacyBaselinePreservesFirstRecordedRevision(t *testing.T) {
	tenant, actor, investigation := uuid.NewString(), uuid.NewString(), uuid.NewString()
	ref := HashToken("synthetic archive baseline")
	hash, _ := bcrypt.GenerateFromPassword([]byte("integration-password"), bcrypt.MinCost)
	s := testService(t, func(database *sql.DB) {
		if err := goose.UpTo(database, filepath.Join("..", "..", "db", "migrations"), 2026110603); err != nil {
			t.Fatal(err)
		}
		for _, statement := range []struct {
			sql  string
			args []any
		}{
			{`insert into business.tenants(id,code,name) values($1,'archive-baseline','Synthetic')`, []any{tenant}},
			{`insert into business.users(id,tenant_id,account,password_hash,display_name,role,enabled) values($1,$2,'archive.baseline',$3,'Synthetic','admin',true)`, []any{actor, tenant, string(hash)}},
			{`insert into business.source_investigations(id,tenant_id,object_ref,store_id,object_key,status,note,revision,created_by,updated_by,updated_at) values($1,$2,$3,'private-store',$4,'closed',' Exact legacy whitespace ',7,$5,$5,'2020-01-01T00:00:00Z')`, []any{investigation, tenant, ref, "knowledge/" + tenant + "/misc/baseline", actor}},
		} {
			if _, err := database.Exec(statement.sql, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
	})
	admin, _, _ := managedLogin(t, s, "archive.baseline", "integration-password")
	for revision := int64(7); revision < 11; revision++ {
		historyUpdate(t, s, admin, ref, revision)
	}
	if _, err := ArchiveInvestigationRevision(admin, s, archiveRequest(ref, 7, 11)); err != nil {
		t.Fatal(err)
	}
	page, err := ListInvestigationHistory(admin, s, &types.InvestigationHistoryReq{ObjectRef: ref})
	if err != nil || page.FirstRecordedRevision != 7 || len(page.Items) != 5 || !page.Items[4].IsBaseline || !page.Items[4].MetadataArchived || page.Items[4].Note != " Exact legacy whitespace " {
		t.Fatal(page, err)
	}
	receipt, err := RestoreInvestigationRevision(admin, s, restoreRequest(ref, 7, 11))
	if err != nil || receipt.Investigation.Revision != 12 || receipt.Investigation.Note != " Exact legacy whitespace " {
		t.Fatal(receipt, err)
	}
	archiveCounts(t, s, 5, 1, 1)
}
