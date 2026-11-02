package seed

import (
	"context"
	"fmt"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"github.com/google/uuid"
)

// Orders inserts repeatable synthetic fixtures while preserving existing rows.
func Orders(ctx context.Context, s *svc.ServiceContext, tenant string) error {
	fixed := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	for index := 1; index <= 50; index++ {
		number := fmt.Sprintf("FD20261102%04d", index)
		id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(tenant+":"+number)).String()
		statuses := []string{"processing", "in_transit", "delivered", "in_transit", "cancelled"}
		status := statuses[(index-1)%len(statuses)]
		if _, err := s.OrdersModel.FindOne(ctx, id); err == model.ErrNotFound {
			_, err = s.OrdersModel.Insert(ctx, &model.Orders{Id: id, TenantId: tenant, OrderNo: number, MaskedCustomerName: fmt.Sprintf("合成客户%02d*", index), Status: status, TotalAmount: fmt.Sprintf("%d.90", 100+index), Currency: "CNY", Version: 1, PlacedAt: fixed.Add(-24 * time.Hour)})
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if status == "processing" || status == "cancelled" {
			continue
		}
		descriptions := []string{"包裹已揽收", "包裹到达合成配送站"}
		if status == "delivered" {
			descriptions = append(descriptions, "包裹已送达合成收件人")
		}
		for position, description := range descriptions {
			providerID := fmt.Sprintf("synthetic:%s:%d", id, position)
			if _, err := s.LogisticsEventsModel.FindOneByTenantIdProviderEventId(ctx, tenant, providerID); err == model.ErrNotFound {
				occurred := fixed.Add(time.Duration(position) * time.Hour)
				_, err = s.LogisticsEventsModel.Insert(ctx, &model.LogisticsEvents{Id: uuid.NewSHA1(uuid.NameSpaceURL, []byte(providerID)).String(), TenantId: tenant, OrderId: id, ProviderEventId: providerID, Status: status, Description: description, OccurredAt: occurred, SourceUpdatedAt: occurred.Add(time.Minute)})
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
	}
	return nil
}
