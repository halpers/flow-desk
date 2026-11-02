package logic

import (
	"context"
	"time"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
)

func orderData(row *model.Orders) types.OrderData {
	return types.OrderData{Id: row.Id, OrderNo: row.OrderNo, MaskedCustomerName: row.MaskedCustomerName, Status: row.Status, TotalAmount: row.TotalAmount, Currency: row.Currency, Version: row.Version, PlacedAt: row.PlacedAt.UTC().Format(time.RFC3339Nano)}
}
func logisticsData(rows []model.LogisticsEvents) []types.LogisticsData {
	items := []types.LogisticsData{}
	for _, row := range rows {
		items = append(items, types.LogisticsData{Id: row.Id, Status: row.Status, Description: row.Description, OccurredAt: row.OccurredAt.UTC().Format(time.RFC3339Nano), SourceUpdatedAt: row.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return items
}

func ListOrders(ctx context.Context, s *svc.ServiceContext, req *types.OrdersReq) (types.OrderPage, error) {
	result := types.OrderPage{Items: []types.OrderData{}}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	actor := current.User
	if !permitted(actor, "order:read") {
		return result, errForbidden
	}
	if utf8.RuneCountInString(req.OrderNo) > 50 {
		return result, errArgument
	}
	limit, err := pageLimit(req.Limit)
	if err != nil {
		return result, err
	}
	scope := "orders:" + actor.TenantId + ":" + req.OrderNo
	after, err := decodeCursor(req.Cursor, scope)
	if err != nil {
		return result, err
	}
	rows, err := s.OrdersModel.ListByTenant(ctx, actor.TenantId, req.OrderNo, after, limit+1)
	if err != nil {
		return result, err
	}
	result.HasMore = int64(len(rows)) > limit
	if result.HasMore {
		rows = rows[:limit]
	}
	for index := range rows {
		result.Items = append(result.Items, orderData(&rows[index]))
	}
	if result.HasMore {
		result.NextCursor = encodeCursor(scope, rows[len(rows)-1].OrderNo)
	}
	return result, nil
}
func GetOrder(ctx context.Context, s *svc.ServiceContext, id string) (types.OrderData, error) {
	if !validID(id) {
		return types.OrderData{}, errArgument
	}
	current, err := identityRequired(ctx)
	if err != nil {
		return types.OrderData{}, err
	}
	if !permitted(current.User, "order:read") {
		return types.OrderData{}, errForbidden
	}
	row, err := s.OrdersModel.FindOneByTenant(ctx, current.User.TenantId, id)
	if err != nil {
		return types.OrderData{}, err
	}
	return orderData(row), nil
}
func GetLogistics(ctx context.Context, s *svc.ServiceContext, id string) (types.LogisticsPage, error) {
	result := types.LogisticsPage{Items: []types.LogisticsData{}}
	if _, err := GetOrder(ctx, s, id); err != nil {
		return result, err
	}
	rows, err := s.LogisticsEventsModel.ListByOrder(ctx, Current(ctx).User.TenantId, id)
	if err != nil {
		return result, err
	}
	result.Items = logisticsData(rows)
	return result, nil
}
