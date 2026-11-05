package logic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

const maxKnowledgeBasesPerTenant int64 = 50
const maxDocumentsPerBase int64 = 200
const maxVersionsPerDocument int64 = 100

func knowledgeAdmin(ctx context.Context) (*model.Users, error) {
	current, err := identityRequired(ctx)
	if err != nil {
		return nil, err
	}
	if !permitted(current.User, "knowledge:manage") {
		return nil, errForbidden
	}
	return current.User, nil
}
func validRoles(roles []string) bool {
	if len(roles) < 1 || len(roles) > 3 || !slices.Contains(roles, "admin") {
		return false
	}
	seen := map[string]bool{}
	for _, role := range roles {
		if !slices.Contains([]string{"admin", "supervisor", "operator"}, role) || seen[role] {
			return false
		}
		seen[role] = true
	}
	return true
}
func baseData(row *model.KnowledgeBases) types.KnowledgeBaseData {
	roles := []string{}
	_ = json.Unmarshal([]byte(row.ReadRoles), &roles)
	return types.KnowledgeBaseData{TeamId: row.TeamId.String, Id: row.Id, Name: row.Name, ReadRoles: roles, AclRevision: row.AclRevision}
}
func documentData(row *model.Documents) types.DocumentData {
	return types.DocumentData{Id: row.Id, KnowledgeBaseId: row.KnowledgeBaseId, Title: row.Title, ActiveVersionId: row.ActiveVersionId.String, Version: row.Version, Status: row.Status}
}
func versionData(row *model.DocumentVersions) types.VersionData {
	pages := pageTexts(row.PageTexts)
	textPages := int64(0)
	for _, page := range pages {
		if strings.TrimSpace(page.Text) != "" {
			textPages++
		}
	}
	size := int(row.SourceSize)
	if row.SourceFormat != "pdf" && row.CleanupState != versionPurged {
		size = len(row.Content)
	}
	return types.VersionData{CleanupState: row.CleanupState, PdfLayout: row.PdfLayout, SourceStorage: row.SourceStorage, OcrMode: row.OcrMode, OcrPageCount: ocrPageCount(pages), SourceFormat: row.SourceFormat, SourceSize: int64(size), PageCount: int64(len(pages)), TextPageCount: textPages, Id: row.Id, DocumentId: row.DocumentId, Filename: row.Filename, ContentHash: row.ContentHash, IndexStatus: row.IndexStatus, ChunkCount: row.ChunkCount, ErrorCode: row.ErrorCode}
}
func evidenceData(row *model.RunEvidence) types.EvidenceData {
	return types.EvidenceData{ExtractionMethod: row.ExtractionMethod, PageNumber: row.PageNumber, Id: row.Id, VersionId: row.VersionId, ChunkId: row.ChunkId, Title: row.Title, Excerpt: row.Excerpt}
}
func readableBase(row *model.KnowledgeBases, role string) bool {
	var roles []string
	_ = json.Unmarshal([]byte(row.ReadRoles), &roles)
	return slices.Contains(roles, role)
}

// Resource locks precede the tenant authorization lock. Custom managers retain
// the same base-role ACL and team boundary as other readers.
func knowledgeManagementBaseTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, id string) error {
	base, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, actor.TenantId, id)
	if err != nil {
		return err
	}
	if err = teamAccessTx(ctx, s, tx, actor, base.TeamId); err != nil {
		return err
	}
	fresh, err := operationsViewerTx(ctx, s, tx)
	if err != nil {
		return err
	}
	if fresh.AuthVersion != actor.AuthVersion {
		return ErrUnauthorized
	}
	if !permitted(fresh, "knowledge:manage") {
		return errForbidden
	}
	if !readableBase(base, fresh.Role) {
		return errMissing
	}
	return nil
}
func managedVersionAccessTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, result *types.VersionData) error {
	_, err := sourceAdminVersionTx(ctx, s, tx, result.DocumentId, result.Id)
	return err
}

func ListKnowledgeBases(ctx context.Context, s *svc.ServiceContext) (types.KnowledgeBasePage, error) {
	result := types.KnowledgeBasePage{Items: []types.KnowledgeBaseData{}}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if err := s.TeamsModel.LockAuthorizationForShareTx(ctx, tx, current.User.TenantId); err != nil {
			return err
		}
		rows, err := s.KnowledgeBasesModel.ListAccessibleTx(ctx, tx, current.User.TenantId, current.User.Id, current.User.Role)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result.Items = append(result.Items, baseData(&row))
		}
		return nil
	})
	return result, err
}

func CreateKnowledgeBase(ctx context.Context, s *svc.ServiceContext, req *types.KnowledgeBaseWriteReq) (types.KnowledgeBaseData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.KnowledgeBaseData{}, err
	}
	req.Name = strings.TrimSpace(req.Name)
	if len(req.Name) == 0 || utf8.RuneCountInString(req.Name) > 120 || !validRoles(req.ReadRoles) || !slices.Contains(req.ReadRoles, actor.Role) {
		return types.KnowledgeBaseData{}, errArgument
	}
	return idempotentAuthorized(ctx, s, "knowledge.create", req.IdempotencyKey, req, 201, func(ctx context.Context, tx sqlx.Session) (types.KnowledgeBaseData, error) {
		if err := s.KnowledgeBasesModel.LockTenantQuotaTx(ctx, tx, actor.TenantId); err != nil {
			return types.KnowledgeBaseData{}, err
		}
		count, err := s.KnowledgeBasesModel.CountByTenantTx(ctx, tx, actor.TenantId)
		if err != nil {
			return types.KnowledgeBaseData{}, err
		}
		if count >= maxKnowledgeBasesPerTenant {
			return types.KnowledgeBaseData{}, failure(429, "KNOWLEDGE_LIMIT", "知识库数量已达到本地演示上限")
		}
		row := &model.KnowledgeBases{Id: uuid.NewString(), TenantId: actor.TenantId, Name: req.Name, ReadRoles: marshal(req.ReadRoles), AclRevision: 1}
		if err := s.KnowledgeBasesModel.InsertTx(ctx, tx, row); err != nil {
			return types.KnowledgeBaseData{}, err
		}
		return baseData(row), nil
	}, func(ctx context.Context, tx sqlx.Session, result *types.KnowledgeBaseData) error {
		return knowledgeManagementBaseTx(ctx, s, tx, actor, result.Id)
	})
}
func UpdateKnowledgeAcl(ctx context.Context, s *svc.ServiceContext, req *types.KnowledgeAclReq) (types.KnowledgeBaseData, error) {
	var result types.KnowledgeBaseData
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return result, err
	}
	if !validID(req.KnowledgeBaseId) || !validRoles(req.ReadRoles) {
		return result, errArgument
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		row, err := s.KnowledgeBasesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.KnowledgeBaseId)
		if err != nil {
			return err
		}
		if row.AclRevision != req.ExpectedRevision {
			return failure(409, "VERSION_CONFLICT", "知识库权限版本已变化")
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, row.Id); err != nil {
			return err
		}
		if err = s.KnowledgeBasesModel.UpdateAclTx(ctx, tx, actor.TenantId, row.Id, req.ExpectedRevision, marshal(req.ReadRoles)); err != nil {
			return err
		}
		row.ReadRoles = marshal(req.ReadRoles)
		row.AclRevision++
		result = baseData(row)
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	return result, err
}
func ListDocuments(ctx context.Context, s *svc.ServiceContext, baseID string) (types.DocumentPage, error) {
	result := types.DocumentPage{Items: []types.DocumentData{}}
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	if !validID(baseID) {
		return result, errArgument
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		base, err := s.KnowledgeBasesModel.FindOneForShareTx(ctx, tx, current.User.TenantId, baseID)
		if err != nil {
			return err
		}
		if !readableBase(base, current.User.Role) {
			return errMissing
		}
		if err = teamAccessTx(ctx, s, tx, current.User, base.TeamId); err != nil {
			return err
		}
		rows, err := s.DocumentsModel.ListByBaseTx(ctx, tx, current.User.TenantId, baseID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result.Items = append(result.Items, documentData(&row))
		}
		return nil
	})
	return result, err
}

func validateUpload(filename, content string) bool {
	extension := strings.ToLower(filepath.Ext(filename))
	return (extension == ".txt" || extension == ".md") && len(filename) <= 180 && filepath.Base(filename) == filename && !strings.ContainsAny(filename, "/\\\x00") && utf8.ValidString(content) && len(content) <= 256*1024 && utf8.RuneCountInString(content) <= 20000 && strings.TrimSpace(content) != "" && !strings.ContainsRune(content, 0)
}
func insertDocumentVersionTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, actor *model.Users, doc *model.Documents, filename string, file knowledgeFile) (types.VersionData, error) {
	count, err := s.DocumentVersionsModel.CountByDocumentTx(ctx, tx, actor.TenantId, doc.Id)
	if err != nil {
		return types.VersionData{}, err
	}
	if count >= maxVersionsPerDocument {
		return types.VersionData{}, failure(429, "KNOWLEDGE_LIMIT", "文档版本数已达到上限")
	}
	hash := HashToken(file.content)
	if file.format == "pdf" {
		hash = HashToken(string(file.raw))
	}
	row := &model.DocumentVersions{PdfLayout: file.pdfLayout, OcrMode: file.ocrMode, SourceFormat: file.format, SourceBytes: file.raw, PageTexts: "[]", Id: uuid.NewString(), TenantId: actor.TenantId, DocumentId: doc.Id, JobId: uuid.NewString(), Filename: filename, Content: file.content, ContentHash: hash, IndexStatus: "pending"}
	if err := s.DocumentVersionsModel.InsertTx(ctx, tx, row); err != nil {
		return types.VersionData{}, err
	}
	payload := marshal(map[string]any{"job_id": row.JobId, "version_id": row.Id})
	if err := s.OutboxEventsModel.InsertTx(ctx, tx, &model.OutboxEvents{Id: uuid.NewString(), TenantId: actor.TenantId, AggregateId: doc.Id, DispatchId: row.JobId, Topic: "knowledge.index", Payload: payload, Status: "pending"}); err != nil {
		return types.VersionData{}, err
	}
	if file.format == "pdf" && s.Config.FileStoreProvider == "s3" {
		if err := enqueueSourceArchiveTx(ctx, s, tx, row); err != nil {
			return types.VersionData{}, err
		}
	}
	return versionData(row), nil
}
func UploadDocument(ctx context.Context, s *svc.ServiceContext, req *types.UploadDocumentReq) (types.VersionData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.VersionData{}, err
	}
	req.Title = strings.TrimSpace(req.Title)
	file, err := knowledgeUploadWithLayout(req.Filename, req.Content, req.ContentBase64, req.OcrMode, req.PdfLayout)
	if err != nil {
		return types.VersionData{}, err
	}
	req.OcrMode = file.ocrMode
	req.PdfLayout = file.pdfLayout
	if file.format == "pdf" {
		req.ContentBase64 = base64.StdEncoding.EncodeToString(file.raw)
	}
	if !validID(req.KnowledgeBaseId) || req.Title == "" || utf8.RuneCountInString(req.Title) > 120 {
		return types.VersionData{}, errArgument
	}
	// Keep the historical fingerprint for default uploads; include explicit layout selections.
	fingerprint := struct {
		*types.UploadDocumentReq
		PdfLayout string `json:"pdf_layout,omitempty"`
	}{UploadDocumentReq: req}
	if file.pdfLayout != "plain" {
		fingerprint.PdfLayout = file.pdfLayout
	}
	return idempotentAuthorized(ctx, s, "document.upload", req.IdempotencyKey, fingerprint, 202, func(ctx context.Context, tx sqlx.Session) (types.VersionData, error) {
		if _, err := s.KnowledgeBasesModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.KnowledgeBaseId); err != nil {
			return types.VersionData{}, err
		}
		if err := knowledgeManagementBaseTx(ctx, s, tx, actor, req.KnowledgeBaseId); err != nil {
			return types.VersionData{}, err
		}
		count, err := s.DocumentsModel.CountByBaseTx(ctx, tx, actor.TenantId, req.KnowledgeBaseId)
		if err != nil {
			return types.VersionData{}, err
		}
		if count >= maxDocumentsPerBase {
			return types.VersionData{}, failure(429, "KNOWLEDGE_LIMIT", "知识库文档数已达到上限")
		}
		doc := &model.Documents{Id: uuid.NewString(), TenantId: actor.TenantId, KnowledgeBaseId: req.KnowledgeBaseId, Title: req.Title, Version: 1, Status: "draft"}
		if err := s.DocumentsModel.InsertTx(ctx, tx, doc); err != nil {
			return types.VersionData{}, err
		}
		return insertDocumentVersionTx(ctx, s, tx, actor, doc, req.Filename, file)
	}, func(ctx context.Context, tx sqlx.Session, result *types.VersionData) error {
		return managedVersionAccessTx(ctx, s, tx, result)
	})
}
func UploadDocumentVersion(ctx context.Context, s *svc.ServiceContext, req *types.UploadVersionReq) (types.VersionData, error) {
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return types.VersionData{}, err
	}
	file, err := knowledgeUploadWithLayout(req.Filename, req.Content, req.ContentBase64, req.OcrMode, req.PdfLayout)
	if err != nil {
		return types.VersionData{}, err
	}
	req.OcrMode = file.ocrMode
	req.PdfLayout = file.pdfLayout
	if file.format == "pdf" {
		req.ContentBase64 = base64.StdEncoding.EncodeToString(file.raw)
	}
	if !validID(req.DocumentId) {
		return types.VersionData{}, errArgument
	}
	fingerprint := struct {
		*types.UploadVersionReq
		PdfLayout string `json:"pdf_layout,omitempty"`
	}{UploadVersionReq: req}
	if file.pdfLayout != "plain" {
		fingerprint.PdfLayout = file.pdfLayout
	}
	return idempotentAuthorized(ctx, s, "document.version", req.IdempotencyKey, fingerprint, 202, func(ctx context.Context, tx sqlx.Session) (types.VersionData, error) {
		doc, err := s.DocumentsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, req.DocumentId)
		if err != nil {
			return types.VersionData{}, err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return types.VersionData{}, err
		}
		return insertDocumentVersionTx(ctx, s, tx, actor, doc, req.Filename, file)
	}, func(ctx context.Context, tx sqlx.Session, result *types.VersionData) error {
		return managedVersionAccessTx(ctx, s, tx, result)
	})
}
func ListDocumentVersions(ctx context.Context, s *svc.ServiceContext, documentID string) (types.VersionPage, error) {
	result := types.VersionPage{Items: []types.VersionData{}}
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return result, err
	}
	if !validID(documentID) {
		return result, errArgument
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		doc, err := s.DocumentsModel.FindOneForShareTx(ctx, tx, actor.TenantId, documentID)
		if err != nil {
			return err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return err
		}
		rows, err := s.DocumentVersionsModel.ListByDocumentTx(ctx, tx, actor.TenantId, documentID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result.Items = append(result.Items, types.VersionData{CleanupState: row.CleanupState, Id: row.Id, DocumentId: row.DocumentId, Filename: row.Filename,
				ContentHash: row.ContentHash, IndexStatus: row.IndexStatus, ChunkCount: row.ChunkCount, ErrorCode: row.ErrorCode,
				PdfLayout: row.PdfLayout, SourceStorage: row.SourceStorage, OcrMode: row.OcrMode, OcrPageCount: row.OcrPageCount, SourceFormat: row.SourceFormat, SourceSize: row.SourceSize, PageCount: row.PageCount, TextPageCount: row.TextPageCount})
		}
		return nil
	})
	return result, err
}
func changeDocument(ctx context.Context, s *svc.ServiceContext, id, versionID string, expected int64, confirmOCR, confirmLayout bool) (types.DocumentData, error) {
	var result types.DocumentData
	actor, err := knowledgeAdmin(ctx)
	if err != nil {
		return result, err
	}
	if !validID(id) || (versionID != "" && !validID(versionID)) {
		return result, errArgument
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		doc, err := s.DocumentsModel.FindOneForUpdateTx(ctx, tx, actor.TenantId, id)
		if err != nil {
			return err
		}
		if err = knowledgeManagementBaseTx(ctx, s, tx, actor, doc.KnowledgeBaseId); err != nil {
			return err
		}
		if doc.Version != expected {
			return failure(409, "VERSION_CONFLICT", "文档状态已变化，请刷新")
		}
		status := "withdrawn"
		if versionID != "" {
			v, err := s.DocumentVersionsModel.FindOneForShareTx(ctx, tx, actor.TenantId, versionID)
			if err != nil {
				return err
			}
			if v.DocumentId != id {
				return errMissing
			}
			if err = requireRetained(v); err != nil {
				return err
			}
			if v.IndexStatus != "ready" {
				return failure(409, "INDEX_NOT_READY", "索引尚未就绪")
			}
			if ocrPageCount(pageTexts(v.PageTexts)) > 0 {
				if !confirmOCR {
					return failure(409, "OCR_REVIEW_REQUIRED", "请核对 OCR 提取文本并确认后再激活")
				}
				if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.ocr_review", ObjectType: "document_version", ObjectId: v.Id, Source: "manual", Summary: "OCR text reviewed before activation"}); err != nil {
					return err
				}
			}
			if v.PdfLayout != "" && v.PdfLayout != "plain" {
				if !confirmLayout {
					return failure(409, "PDF_LAYOUT_REVIEW_REQUIRED", "请核对双栏顺序或表格行列后再确认激活")
				}
				if err = s.AuditEventsModel.InsertTx(ctx, tx, &model.AuditEvents{Id: orderedID(), TenantId: actor.TenantId, ActorUserId: actor.Id, Operation: "knowledge.layout_review", ObjectType: "document_version", ObjectId: v.Id, Source: "manual", Summary: "PDF reading order and row alignment reviewed before activation"}); err != nil {
					return err
				}
			}
			status = "active"
		}
		if err = s.DocumentsModel.UpdateActiveVersionTx(ctx, tx, actor.TenantId, id, expected, versionID, status); err != nil {
			return err
		}
		doc.Version++
		doc.ActiveVersionId.String = versionID
		doc.ActiveVersionId.Valid = versionID != ""
		doc.Status = status
		result = documentData(doc)
		return validateCurrentIdentityTx(ctx, s, tx)
	})
	return result, err
}
func ActivateDocument(ctx context.Context, s *svc.ServiceContext, req *types.ActivateDocumentReq) (types.DocumentData, error) {
	if !validID(req.VersionId) {
		return types.DocumentData{}, errArgument
	}
	return changeDocument(ctx, s, req.DocumentId, req.VersionId, req.ExpectedVersion, req.ConfirmOcr, req.ConfirmLayout)
}
func WithdrawDocument(ctx context.Context, s *svc.ServiceContext, req *types.WithdrawDocumentReq) (types.DocumentData, error) {
	return changeDocument(ctx, s, req.DocumentId, "", req.ExpectedVersion, false, false)
}
func GetIndexSource(ctx context.Context, s *svc.ServiceContext, req *types.IndexSourceReq) (types.IndexSourceData, error) {
	var result types.IndexSourceData
	if !validID(req.VersionId) || !validID(req.JobId) {
		return result, errArgument
	}
	v, err := s.DocumentVersionsModel.FindOne(ctx, req.VersionId)
	if err != nil {
		return result, err
	}
	if v.JobId != req.JobId {
		return result, errMissing
	}
	d, err := s.DocumentsModel.FindOneByTenantIdId(ctx, v.TenantId, v.DocumentId)
	if err != nil {
		return result, err
	}
	raw, err := readOriginal(ctx, s, v)
	if err != nil {
		return result, err
	}
	return types.IndexSourceData{PdfLayout: v.PdfLayout, OcrMode: v.OcrMode, SourceFormat: v.SourceFormat, SourceBase64: base64.StdEncoding.EncodeToString(raw), TenantId: v.TenantId, KnowledgeBaseId: d.KnowledgeBaseId, DocumentId: d.Id, VersionId: v.Id, Content: v.Content, ContentHash: v.ContentHash}, nil
}
func CompleteIndex(ctx context.Context, s *svc.ServiceContext, req *types.IndexCompleteReq) (types.VersionData, error) {
	var result types.VersionData
	if !validID(req.VersionId) || !validID(req.JobId) || req.ChunkCount < 0 || req.ChunkCount > 1000 || (req.Status != "ready" && req.Status != "failed") || (req.Status == "ready" && req.ChunkCount == 0) {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		if req.Status == "failed" && req.ErrorCode == "" {
			req.ErrorCode = "INDEX_FAILED"
		}
		v, err := s.DocumentVersionsModel.FindOneByJobIdForUpdateTx(ctx, tx, req.JobId)
		if err != nil {
			return err
		}
		if err = requireRetained(v); err != nil {
			return err
		}
		pending := v.IndexStatus == "pending"
		if err = applyIndexResult(v, req); err != nil {
			return err
		}
		if pending {
			if err = s.DocumentVersionsModel.UpdateIndexResultTx(ctx, tx, v); err != nil {
				return err
			}
		}
		result = versionData(v)
		return nil
	})
	return result, err
}

func knowledgeRunTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, runID, raw string) (*model.Runs, *model.Users, error) {
	claims, err := parseCapability(s, raw)
	if err != nil {
		return nil, nil, err
	}
	if claims.RunId != runID {
		return nil, nil, errForbidden
	}
	run, err := s.RunsModel.FindOneForUpdateTx(ctx, tx, claims.TenantId, runID)
	if err != nil {
		return nil, nil, err
	}
	if err = authorizeCapabilityTx(ctx, s, tx, run, claims, "knowledge:read"); err != nil {
		return nil, nil, err
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
	return run, actor, err
}
func GetKnowledgeScope(ctx context.Context, s *svc.ServiceContext, runID, capability string) (types.KnowledgeScope, error) {
	result := types.KnowledgeScope{Versions: []types.ScopeVersion{}}
	if !validID(runID) {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, actor, err := knowledgeRunTx(ctx, s, tx, runID, capability)
		if err != nil {
			return err
		}
		result.TenantId = run.TenantId
		versions, err := s.DocumentVersionsModel.ListActiveForShareTx(ctx, tx, run.TenantId, actor.Role, actor.Id)
		if err != nil {
			return err
		}
		for _, v := range versions {
			result.Versions = append(result.Versions, types.ScopeVersion{VersionId: v.VersionId, DocumentId: v.DocumentId, KnowledgeBaseId: v.KnowledgeBaseId, Title: v.Title, AclRevision: v.AclRevision})
		}
		return nil
	})
	return result, err
}
func ValidateEvidence(ctx context.Context, s *svc.ServiceContext, req *types.EvidenceReq, capability string) (types.EvidencePage, error) {
	result := types.EvidencePage{Items: []types.EvidenceData{}}
	if !validID(req.RunId) || len(req.Candidates) > 5 {
		return result, errArgument
	}
	err := s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, actor, err := knowledgeRunTx(ctx, s, tx, req.RunId, capability)
		if err != nil {
			return err
		}
		versions, err := s.DocumentVersionsModel.ListActiveForShareTx(ctx, tx, run.TenantId, actor.Role, actor.Id)
		if err != nil {
			return err
		}
		for _, candidate := range req.Candidates {
			if !validID(candidate.VersionId) || !validID(candidate.ChunkId) || candidate.Excerpt == "" || utf8.RuneCountInString(candidate.Excerpt) > 1800 || HashToken(candidate.Excerpt) != candidate.ContentHash {
				return errArgument
			}
			match := slices.IndexFunc(versions, func(v model.ActiveKnowledgeVersion) bool { return v.VersionId == candidate.VersionId })
			if match < 0 {
				continue
			}
			v := versions[match]
			if !excerptOnPage(v, candidate) {
				return errForbidden
			}
			id := uuid.NewSHA1(uuid.MustParse(run.Id), []byte(candidate.ChunkId)).String()
			row, err := s.RunEvidenceModel.FindOneForRunTx(ctx, tx, run.TenantId, run.Id, id)
			if errors.Is(err, model.ErrNotFound) {
				row = &model.RunEvidence{ExtractionMethod: evidenceExtractionMethod(v, candidate.PageNumber), PageNumber: candidate.PageNumber, Id: id, TenantId: run.TenantId, RunId: run.Id, VersionId: v.VersionId, ChunkId: candidate.ChunkId, Title: v.Title, Excerpt: candidate.Excerpt, ContentHash: candidate.ContentHash}
				err = s.RunEvidenceModel.InsertTx(ctx, tx, row)
			} else if err == nil && (row.VersionId != candidate.VersionId || row.ContentHash != candidate.ContentHash || row.PageNumber != candidate.PageNumber) {
				return errForbidden
			}
			if err != nil {
				return err
			}
			result.Items = append(result.Items, evidenceData(row))
		}
		return nil
	})
	return result, err
}

func canonicalCitationsTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, citations any) ([]map[string]any, error) {
	result := []map[string]any{}
	values, ok := citations.([]interface{})
	if !ok || len(values) > 5 {
		return nil, errArgument
	}
	if len(values) == 0 {
		return result, nil
	}
	actor, err := s.UsersModel.FindOneTx(ctx, tx, run.ActorUserId)
	if err != nil {
		return nil, err
	}
	versions, err := s.DocumentVersionsModel.ListActiveForShareTx(ctx, tx, run.TenantId, actor.Role, actor.Id)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, value := range values {
		data, ok := value.(map[string]any)
		if !ok {
			return nil, errArgument
		}
		id, ok := data["id"].(string)
		if !ok || !validID(id) || seen[id] {
			return nil, errArgument
		}
		seen[id] = true
		row, err := s.RunEvidenceModel.FindOneForRunTx(ctx, tx, run.TenantId, run.Id, id)
		if err != nil {
			return nil, errForbidden
		}
		if !slices.ContainsFunc(versions, func(v model.ActiveKnowledgeVersion) bool { return v.VersionId == row.VersionId }) {
			return nil, failure(409, "EVIDENCE_REVOKED", "引用的政策已撤回或权限已变化")
		}
		result = append(result, map[string]any{"id": row.Id, "version_id": row.VersionId, "chunk_id": row.ChunkId, "title": row.Title, "page_number": row.PageNumber})
	}
	return result, nil
}
func GetCitation(ctx context.Context, s *svc.ServiceContext, id string) (types.EvidenceData, error) {
	var result types.EvidenceData
	current, err := identityRequired(ctx)
	if err != nil {
		return result, err
	}
	if !validID(id) {
		return result, errArgument
	}
	// Identifier lookup is only a locator; no content leaves before owner and ACL checks.
	row, err := s.RunEvidenceModel.FindOne(ctx, id)
	if err != nil {
		return result, err
	}
	if row.TenantId != current.User.TenantId {
		return result, errMissing
	}
	err = s.Conn.TransactCtx(ctx, func(ctx context.Context, tx sqlx.Session) error {
		run, err := s.RunsModel.FindOneOwnedForShareTx(ctx, tx, current.User.TenantId, current.User.Id, row.RunId)
		if err != nil {
			return err
		}
		_, err = canonicalCitationsTx(ctx, s, tx, run, []interface{}{map[string]any{"id": id}})
		if err != nil {
			return err
		}
		result = evidenceData(row)
		return nil
	})
	return result, err
}

func redactMessageTx(ctx context.Context, s *svc.ServiceContext, tx sqlx.Session, run *model.Runs, message *types.MessageData) error {
	visible, err := runTicketsVisibleTx(ctx, s, tx, run)
	if err != nil {
		return err
	}
	if !visible {
		message.Content = resourceHiddenMessage
		message.Citations = []map[string]any{}
		message.ActionReceipts = []map[string]any{}
		return nil
	}
	if len(message.Citations) == 0 {
		return nil
	}
	var citations []interface{}
	if err := json.Unmarshal([]byte(marshal(message.Citations)), &citations); err != nil {
		return err
	}
	_, err = canonicalCitationsTx(ctx, s, tx, run, citations)
	if err == nil {
		return nil
	}
	var business *BusinessError
	if errors.As(err, &business) && (business.Code == "EVIDENCE_REVOKED" || business.Code == "PERMISSION_DENIED") {
		message.Content = "此答复引用的政策已撤回或访问权限已变化，请重新查询。"
		message.Citations = []map[string]any{}
		return nil
	}
	return err
}
