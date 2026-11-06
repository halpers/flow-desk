package logic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const defaultInvestigationListPage int64 = 20
const maximumInvestigationListPage int64 = 50

func investigationListScope(actor *model.Users, status string) string {
	return strings.Join([]string{"source-investigation-list-v1", actor.TenantId, actor.Id, status}, ":")
}
func ListSourceInvestigations(ctx context.Context, s *svc.ServiceContext, req *types.SourceInvestigationListReq) (types.SourceInvestigationListPage, error) {
	limit := req.Limit
	if limit == 0 {
		limit = defaultInvestigationListPage
	}
	if limit < 1 || limit > maximumInvestigationListPage || (req.Status != "" && !slices.Contains(investigationStatuses(), req.Status)) || len(req.Cursor) > maximumSourceInventoryCursor {
		return types.SourceInvestigationListPage{}, errArgument
	}
	result := types.SourceInvestigationListPage{Items: []types.SourceInvestigationListItem{}, AllowedStatuses: investigationStatuses()}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		actor, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		scope := investigationListScope(actor, req.Status)
		position, err := openInventoryCursor(s.Config.JwtSecret, scope, req.Cursor, 0)
		if err != nil {
			var business *BusinessError
			if errors.As(err, &business) && business.Code == "INVENTORY_CURSOR_EXPIRED" {
				return failure(410, "INVESTIGATION_CURSOR_EXPIRED", "调查列表游标已过期，请刷新第一页")
			}
			return err
		}
		before, beforeID := "", ""
		if position.After != "" {
			fields := strings.Split(position.After, "|")
			if len(fields) != 2 || !validID(fields[1]) {
				return errArgument
			}
			timestamp, parseErr := time.Parse(time.RFC3339Nano, fields[0])
			if parseErr != nil {
				return errArgument
			}
			before, beforeID = timestamp.UTC().Format(time.RFC3339Nano), fields[1]
		}
		rows, err := s.SourceInvestigationsModel.ListPageTx(ctx, tx, actor.TenantId, req.Status, before, beforeID, limit+1)
		if err != nil {
			return err
		}
		result.HasMore = int64(len(rows)) > limit
		if result.HasMore {
			rows = rows[:limit]
		}
		for _, row := range rows {
			err = investigationResourceTx(ctx, s, tx, actor, row.ObjectKey)
			if errors.Is(err, errForbidden) || errors.Is(err, errMissing) || errors.Is(err, model.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			result.Items = append(result.Items, types.SourceInvestigationListItem{ObjectRef: row.ObjectRef, Revision: row.Revision, Status: row.Status, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano)})
		}
		if result.HasMore {
			last := rows[len(rows)-1]
			position.After = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.Id
			result.NextCursor, err = sealInventoryCursor(s.Config.JwtSecret, scope, position)
			if err != nil {
				return err
			}
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_investigation_list", ObjectType: "tenant", ObjectId: actor.TenantId, Source: "manual", Summary: fmt.Sprintf("Read investigation metadata: %d scanned, %d visible; status=%s", len(rows), len(result.Items), req.Status)})
	})
	if err != nil {
		return types.SourceInvestigationListPage{}, err
	}
	return result, nil
}
