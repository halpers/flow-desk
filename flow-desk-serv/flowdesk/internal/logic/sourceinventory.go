package logic

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"flowdesk/db/model"
	"flowdesk/internal/client"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const defaultSourceInventoryPage int64 = 20
const maximumSourceInventoryPage int64 = 50
const sourceInventoryCursorLifetime = 15 * time.Minute
const maximumSourceInventoryCursor = 4096
const maximumSourceInventoryJSONInteger int64 = 9007199254740991

type inventoryPosition struct {
	After   string
	Epoch   int64
	Expires int64
}

func inventoryCipher(secret string) (cipher.AEAD, error) {
	if len(secret) < 32 {
		return nil, sourceUnavailable()
	}
	key := sha256.Sum256([]byte("flowdesk-source-inventory-v1\n" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealInventoryCursor(secret, scope string, position inventoryPosition) (string, error) {
	aead, err := inventoryCipher(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	raw, err := json.Marshal(position)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, raw, []byte(scope))), nil
}
func openInventoryCursor(secret, scope, raw string, epoch int64) (inventoryPosition, error) {
	position := inventoryPosition{Epoch: epoch, Expires: time.Now().Add(sourceInventoryCursorLifetime).Unix()}
	if raw == "" {
		return position, nil
	}
	if len(raw) > maximumSourceInventoryCursor {
		return position, errArgument
	}
	aead, err := inventoryCipher(secret)
	if err != nil {
		return position, err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) < aead.NonceSize()+aead.Overhead() {
		return position, errArgument
	}
	decoded, err = aead.Open(nil, decoded[:aead.NonceSize()], decoded[aead.NonceSize():], []byte(scope))
	if err != nil || json.Unmarshal(decoded, &position) != nil || position.After == "" || len(position.After) > 1024 {
		return position, errArgument
	}
	if position.Expires <= time.Now().Unix() {
		return position, failure(410, "INVENTORY_CURSOR_EXPIRED", "巡检游标已过期，请刷新第一页")
	}
	if position.Epoch != epoch {
		return position, failure(409, "SOURCE_CHANGED", "原件代次已变化，请刷新巡检")
	}
	return position, nil
}

func inventoryKeyKind(row *model.DocumentVersions, key string) (string, int64) {
	stem := fmt.Sprintf("knowledge/%s/%s/%s", row.TenantId, row.Id, row.ContentHash)
	if key == stem+".pdf" {
		return "legacy", 0
	}
	if strings.HasPrefix(key, stem+"/") && strings.HasSuffix(key, ".pdf") {
		value := strings.TrimSuffix(strings.TrimPrefix(key, stem+"/"), ".pdf")
		epoch, err := strconv.ParseInt(value, 10, 64)
		if err == nil && epoch >= 0 && epoch <= maximumSourceInventoryJSONInteger && strconv.FormatInt(epoch, 10) == value {
			return "generation", epoch
		}
	}
	return "unrecognized", 0
}
func inventoryItem(row *model.DocumentVersions, store string, observed client.SourceObjectMetadata, registered *model.SourceObjects) types.SourceInventoryItem {
	kind, epoch := inventoryKeyKind(row, observed.Key)
	item := types.SourceInventoryItem{ObjectRef: HashToken(store + "\n" + observed.Key), KeyKind: kind, SourceEpoch: epoch,
		Size: observed.Size, ModifiedAt: observed.ModifiedAt.UTC().Format(time.RFC3339Nano), Reconciliation: "unregistered"}
	if kind == "unrecognized" {
		item.Reconciliation = "unrecognized"
	}
	if registered == nil {
		return item
	}
	item.RegisteredId, item.RegisteredStatus = registered.Id, registered.Status
	switch {
	case kind == "unrecognized" || registered.ContentHash != row.ContentHash || registered.SourceEpoch != epoch:
		item.Reconciliation = "registration_mismatch"
	case registered.Status == sourceObjectRetired:
		if observed.Size == 0 {
			item.Reconciliation = "retired_zero_bytes"
		} else {
			item.Reconciliation = "size_mismatch"
		}
	case registered.Status == sourceObjectCleanupPending:
		item.Reconciliation = "cleanup_pending"
	case observed.Size != registered.SourceSize || registered.SourceSize != row.SourceSize:
		item.Reconciliation = "size_mismatch"
	case row.SourceStorage == "s3" && row.SourceStoreId == store && row.SourceKey == observed.Key:
		item.Reconciliation = "current"
	default:
		item.Reconciliation = "registered"
	}
	return item
}
func GetSourceInventory(ctx context.Context, s *svc.ServiceContext, req *types.SourceInventoryReq) (types.SourceInventoryPage, error) {
	result := types.SourceInventoryPage{VersionId: req.VersionId, Items: []types.SourceInventoryItem{}}
	limit := req.Limit
	if limit == 0 {
		limit = defaultSourceInventoryPage
	}
	if !validID(req.DocumentId) || !validID(req.VersionId) || limit < 1 || limit > maximumSourceInventoryPage {
		return result, errArgument
	}
	row, err := sourceAdminVersion(ctx, s, req.DocumentId, req.VersionId)
	if err != nil {
		return result, err
	}
	if row.SourceFormat != "pdf" {
		return result, errArgument
	}
	if !s.Config.FileStoreInventoryEnabled {
		return result, failure(409, "SOURCE_INVENTORY_DISABLED", "对象巡检尚未启用，请联系运维配置只读列举权限")
	}
	inspector, ok := s.SourceStore.(client.SourceInventory)
	if !ok || s.SourceStore == nil {
		return result, sourceUnavailable()
	}
	store := s.SourceStore.ID()
	if row.SourceEpoch < 0 || row.SourceEpoch > maximumSourceInventoryJSONInteger || (row.SourceStorage == "s3" && row.SourceStoreId != store) {
		return result, sourceUnavailable()
	}
	actor := Current(ctx).User
	scope := strings.Join([]string{"source-inventory-v1", actor.TenantId, actor.Id, req.DocumentId, row.Id, row.ContentHash, store}, ":")
	position, err := openInventoryCursor(s.Config.JwtSecret, scope, req.Cursor, row.SourceEpoch)
	if err != nil {
		return result, err
	}
	observed, err := inspector.ListVersionObjects(ctx, row.TenantId, row.Id, position.After, int32(limit))
	if err != nil {
		return result, failure(503, "SOURCE_INVENTORY_UNAVAILABLE", "对象巡检暂不可用，请核对存储列举权限、网络与兼容能力")
	}
	// Storage requests occur without holding database locks. Do not release a stale observation.
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		current, err := sourceAdminVersionTx(ctx, s, tx, req.DocumentId, req.VersionId)
		if err != nil {
			return err
		}
		if current.SourceEpoch != row.SourceEpoch || current.SourceStorage != row.SourceStorage || current.SourceKey != row.SourceKey || s.SourceStore.ID() != store {
			return failure(409, "SOURCE_CHANGED", "原件状态已变化，请刷新巡检")
		}
		keys := make([]string, 0, len(observed.Items))
		for _, object := range observed.Items {
			keys = append(keys, object.Key)
		}
		registered, err := s.SourceObjectsModel.ListInventoryRegistrationsTx(ctx, tx, current.TenantId, current.Id, store, keys)
		if err != nil {
			return err
		}
		byKey := map[string]*model.SourceObjects{}
		for i := range registered {
			byKey[registered[i].ObjectKey] = &registered[i]
		}
		unknown := 0
		result.AdoptionEnabled = s.Config.FileStoreAdoptionEnabled
		originalVerified := validDatabaseOriginal(current)
		for _, object := range observed.Items {
			item := inventoryItem(current, store, object, byKey[object.Key])
			item.AdoptionReason = sourceAdoptionReason(current, item, result.AdoptionEnabled, originalVerified)
			item.AdoptionEligible = item.AdoptionReason == ""
			if item.AdoptionEligible {
				item.AdoptionToken, err = sealInventoryCursor(s.Config.JwtSecret, sourceAdoptionScope(ctx, current, store), inventoryPosition{After: object.Key, Epoch: current.SourceEpoch, Expires: time.Now().Add(sourceInventoryCursorLifetime).Unix()})
				if err != nil {
					return err
				}
			}
			result.Items = append(result.Items, item)
			if item.RegisteredId == "" {
				unknown++
			}
		}
		result.SourceEpoch = current.SourceEpoch
		result.SourceSize = current.SourceSize
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
		return s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: current.TenantId, ActorUserId: actor.Id, Operation: "knowledge.source_inventory", ObjectType: "document_version", ObjectId: current.Id, Source: "manual",
			Summary: fmt.Sprintf("Read-only object metadata inspection: %d returned, %d unregistered; content and retirement markers not verified", len(result.Items), unknown)})
	})
	return result, err
}
