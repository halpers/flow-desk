package logic

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var inventoryHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Parsed names are hints, never proof of ownership or content integrity.
func tenantInventoryKey(tenant, key string) (version, hash, kind string, epoch int64) {
	prefix := "knowledge/" + tenant + "/"
	if !strings.HasPrefix(key, prefix) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(key, prefix), "/")
	if len(parts) != 2 && len(parts) != 3 {
		return
	}
	parsed, err := uuid.Parse(parts[0])
	if err != nil || parsed.String() != parts[0] {
		return
	}
	if len(parts) == 2 {
		if !strings.HasSuffix(parts[1], ".pdf") {
			return
		}
		hash = strings.TrimSuffix(parts[1], ".pdf")
		kind = "legacy"
	} else {
		hash = parts[1]
		value := strings.TrimSuffix(parts[2], ".pdf")
		if !strings.HasSuffix(parts[2], ".pdf") {
			return "", "", "", 0
		}
		epoch, err = strconv.ParseInt(value, 10, 64)
		if err != nil || epoch < 0 || epoch > maximumSourceInventoryJSONInteger || strconv.FormatInt(epoch, 10) != value {
			return "", "", "", 0
		}
		kind = "generation"
	}
	if !inventoryHashPattern.MatchString(hash) {
		return "", "", "", 0
	}
	return parts[0], hash, kind, epoch
}

// Even a malformed suffix may sit below an existing version. Use its UUID only
// to enforce that resource's ACL; it never proves object ownership.
func tenantInventoryVersionHint(tenant, key string) string {
	rest, ok := strings.CutPrefix(key, "knowledge/"+tenant+"/")
	if !ok {
		return ""
	}
	candidate, _, found := strings.Cut(rest, "/")
	if !found {
		return ""
	}
	parsed, err := uuid.Parse(candidate)
	if err != nil {
		return ""
	}
	return parsed.String()
}

func tenantInventoryActorTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session) (*model.Users, error) {
	actor, err := operationsViewerTx(ctx, s, tx)
	if err != nil {
		return nil, err
	}
	// A delegated knowledge manager must never gain tenant-wide storage discovery.
	if actor.Role != "admin" || !permitted(actor, "knowledge:manage") {
		return nil, errForbidden
	}
	return actor, nil
}

func GetTenantSourceInventory(ctx context.Context, s *svc.ServiceContext, req *types.TenantSourceInventoryReq) (types.TenantSourceInventoryPage, error) {
	result := types.TenantSourceInventoryPage{Items: []types.TenantSourceInventoryItem{}}
	limit := req.Limit
	if limit == 0 {
		limit = defaultSourceInventoryPage
	}
	if limit < 1 || limit > maximumSourceInventoryPage {
		return result, errArgument
	}
	var actor *model.Users
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		var err error
		actor, err = tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	if err != nil {
		return result, err
	}
	if !s.Config.FileStoreInventoryEnabled || !s.Config.FileStoreTenantInventoryEnabled {
		return result, failure(409, "TENANT_SOURCE_INVENTORY_DISABLED", "租户对象巡检尚未启用，请联系运维")
	}
	inspector, ok := s.SourceStore.(client.SourceTenantInventory)
	if !ok || s.SourceStore == nil {
		return result, sourceUnavailable()
	}
	store := s.SourceStore.ID()
	scope := strings.Join([]string{"tenant-source-inventory-v1", actor.TenantId, actor.Id, store}, ":")
	position, err := openInventoryCursor(s.Config.JwtSecret, scope, req.Cursor, 0)
	if err != nil {
		return result, err
	}
	// No database transaction remains open during object-store I/O.
	observed, err := inspector.ListTenantObjects(ctx, actor.TenantId, position.After, int32(limit))
	if err != nil {
		return result, failure(503, "SOURCE_INVENTORY_UNAVAILABLE", "对象巡检暂不可用，请核对存储列举权限、网络与兼容能力")
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		fresh, err := tenantInventoryActorTx(ctx, s, tx)
		if err != nil {
			return err
		}
		if !s.Config.FileStoreInventoryEnabled || !s.Config.FileStoreTenantInventoryEnabled {
			return failure(409, "TENANT_SOURCE_INVENTORY_DISABLED", "租户对象巡检已暂停，请刷新")
		}
		if s.SourceStore == nil || s.SourceStore.ID() != store {
			return sourceUnavailable()
		}
		keys := make([]string, 0, len(observed.Items))
		for _, object := range observed.Items {
			keys = append(keys, object.Key)
		}
		registered, err := s.SourceObjectsModel.ListTenantInventoryRegistrationsTx(ctx, tx, fresh.TenantId, store, keys)
		if err != nil {
			return err
		}
		byKey := map[string]*model.SourceObjects{}
		for i := range registered {
			byKey[registered[i].ObjectKey] = &registered[i]
		}
		unknown := 0
		for _, object := range observed.Items {
			item := types.TenantSourceInventoryItem{ObjectRef: HashToken(store + "\n" + object.Key), Size: object.Size, ModifiedAt: object.ModifiedAt.UTC().Format(time.RFC3339Nano), KeyKind: "unrecognized", Association: "unrecognized_key", Reconciliation: "unrecognized"}
			version, hash, kind, epoch := tenantInventoryKey(fresh.TenantId, object.Key)
			if version != "" {
				item.KeyKind, item.SourceEpoch = kind, epoch
			}
			hint := tenantInventoryVersionHint(fresh.TenantId, object.Key)
			if hint != "" {
				row, lookupErr := s.DocumentVersionsModel.FindInventoryIdentityTx(ctx, tx, fresh.TenantId, hint)
				if errors.Is(lookupErr, model.ErrNotFound) {
					if version != "" {
						item.Association, item.Reconciliation = "unknown_version", "unresolved"
						unknown++
					}
				} else if lookupErr != nil {
					return lookupErr
				} else {
					// Preserve each known resource's current ACL/team boundary.
					if _, err = sourceAdminDocumentTx(ctx, s, tx, row.DocumentId); errors.Is(err, errForbidden) || errors.Is(err, errMissing) || errors.Is(err, model.ErrNotFound) {
						continue
					} else if err != nil {
						return err
					}
					item.Association, item.DocumentId, item.VersionId = "known_version", row.DocumentId, row.Id
					if version == "" {
						item.Reconciliation = "unrecognized"
					} else if hash != row.ContentHash || row.SourceFormat != "pdf" {
						item.Reconciliation = "version_mismatch"
					} else {
						known := inventoryItem(&model.DocumentVersions{Id: row.Id, TenantId: row.TenantId, ContentHash: row.ContentHash, SourceSize: row.SourceSize, SourceStorage: row.SourceStorage, SourceStoreId: row.SourceStoreId, SourceKey: row.SourceKey}, store, object, byKey[object.Key])
						item.Reconciliation, item.RegisteredId, item.RegisteredStatus = known.Reconciliation, known.RegisteredId, known.RegisteredStatus
						if registration := byKey[object.Key]; registration != nil && registration.VersionId != row.Id {
							item.Reconciliation = "registration_mismatch"
						}
					}
				}
			}
			item.InvestigationToken, err = sealInventoryCursor(s.Config.JwtSecret, investigationScope(fresh, store), inventoryPosition{After: object.Key, Expires: time.Now().Add(sourceInventoryCursorLifetime).Unix()})
			if err != nil {
				return err
			}
			result.Items = append(result.Items, item)
		}
		result.ScannedCount = int64(len(observed.Items))
		result.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
		result.HasMore = observed.HasMore
		if result.HasMore && len(observed.Items) > 0 {
			position.After = observed.Items[len(observed.Items)-1].Key
			result.NextCursor, err = sealInventoryCursor(s.Config.JwtSecret, scope, position)
			if err != nil {
				return err
			}
		}
		if err = validateCurrentIdentityTx(ctx, s, tx); err != nil {
			return err
		}
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: fresh.TenantId, ActorUserId: fresh.Id, Operation: "knowledge.tenant_source_inventory", ObjectType: "tenant", ObjectId: fresh.TenantId, Source: "manual", Summary: fmt.Sprintf("Read-only tenant object metadata inspection: %d scanned, %d visible, %d unknown versions; no ownership/content verification", result.ScannedCount, len(result.Items), unknown)})
	})
	if err != nil {
		return types.TenantSourceInventoryPage{}, err
	}
	return result, nil
}
