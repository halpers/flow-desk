package logic

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const defaultInvestigationHistoryPage int64 = 20
const maximumInvestigationHistoryPage int64 = 50

func investigationHistoryScope(actor *model.Users, ref string) string {
	return strings.Join([]string{"source-investigation-history-v1", actor.TenantId, actor.Id, ref}, ":")
}
func ListInvestigationHistory(ctx context.Context, s *svc.ServiceContext, req *types.InvestigationHistoryReq) (types.InvestigationHistoryPage, error) {
	limit := req.Limit
	if limit == 0 {
		limit = defaultInvestigationHistoryPage
	}
	if limit < 1 || limit > maximumInvestigationHistoryPage || !inventoryHashPattern.MatchString(req.ObjectRef) || len(req.Cursor) > maximumSourceInventoryCursor {
		return types.InvestigationHistoryPage{}, errArgument
	}
	result := types.InvestigationHistoryPage{ObjectRef: req.ObjectRef, Items: []types.InvestigationRevisionItem{}}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		initial, err := s.SourceInvestigationsModel.FindOneByTenantIdObjectRefTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if errors.Is(err, model.ErrNotFound) {
			return errMissing
		}
		if err != nil {
			return err
		}
		if err = investigationResourceTx(ctx, s, tx, actor, initial.ObjectKey); err != nil {
			return err
		}
		// Resource locks precede the investigation row lock, matching the writer's order.
		current, err := s.SourceInvestigationsModel.FindOneForShareTx(ctx, tx, actor.TenantId, req.ObjectRef)
		if err != nil {
			return err
		}
		if current.ObjectKey != initial.ObjectKey || current.StoreId != initial.StoreId {
			return errForbidden
		}
		scope := investigationHistoryScope(actor, req.ObjectRef)
		position, err := openInventoryCursor(s.Config.JwtSecret, scope, req.Cursor, 0)
		if err != nil {
			var business *BusinessError
			if errors.As(err, &business) && business.Code == "INVENTORY_CURSOR_EXPIRED" {
				return failure(410, "INVESTIGATION_HISTORY_CURSOR_EXPIRED", "调查历史游标已过期，请刷新第一页")
			}
			return err
		}
		upper, before := current.Revision, int64(0)
		if position.After != "" {
			fields := strings.Split(position.After, "|")
			if len(fields) != 2 {
				return errArgument
			}
			upper, err = strconv.ParseInt(fields[0], 10, 64)
			if err != nil || upper < 1 || strconv.FormatInt(upper, 10) != fields[0] {
				return errArgument
			}
			before, err = strconv.ParseInt(fields[1], 10, 64)
			if err != nil || before < 1 || before > upper || upper > current.Revision || strconv.FormatInt(before, 10) != fields[1] {
				return errArgument
			}
		}
		result.CurrentRevision, result.SnapshotRevision = current.Revision, upper
		result.FirstRecordedRevision, err = s.SourceInvestigationRevisionsModel.FindFirstRevisionTx(ctx, tx, actor.TenantId, current.Id)
		if err != nil {
			return err
		}
		rows, err := s.SourceInvestigationRevisionsModel.ListPageTx(ctx, tx, actor.TenantId, current.Id, upper, before, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		revisions := make([]int64, 0, len(rows))
		for _, row := range rows {
			revisions = append(revisions, row.Revision)
		}
		archives, err := s.SourceInvestigationRevisionArchivesModel.ListRevisionsTx(ctx, tx, actor.TenantId, current.Id, revisions)
		if err != nil {
			return err
		}
		archiveTimes := make(map[int64]string, len(archives))
		for _, archived := range archives {
			archiveTimes[archived.Revision] = archived.ArchivedAt.UTC().Format(time.RFC3339Nano)
		}
		observed := time.Now()
		for _, row := range rows {
			archivedAt := archiveTimes[row.Revision]
			reason := investigationArchiveBlock(current, &row, archivedAt != "", observed)
			result.Items = append(result.Items, types.InvestigationRevisionItem{Revision: row.Revision, Status: row.Status, Note: row.Note, ChangedBy: row.ChangedBy, ChangedAt: row.ChangedAt.UTC().Format(time.RFC3339Nano), IsBaseline: row.IsBaseline, MetadataArchived: archivedAt != "", ArchivedAt: archivedAt, CanArchive: reason == "", ArchiveBlockReason: reason})
		}
		if result.HasMore {
			position.After = fmt.Sprintf("%d|%d", upper, rows[len(rows)-1].Revision)
			result.NextCursor, err = sealInventoryCursor(s.Config.JwtSecret, scope, position)
			if err != nil {
				return err
			}
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_investigation_history", ObjectType: "source_investigation", ObjectId: current.Id, Source: "manual", Summary: fmt.Sprintf("Read %d revision snapshots through revision %d", len(rows), upper)})
	})
	if err != nil {
		return types.InvestigationHistoryPage{}, err
	}
	return result, nil
}
