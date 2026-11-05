package logic

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

type failedLayoutActivationModel struct{ model.DocumentsModel }

func (m failedLayoutActivationModel) UpdateActiveVersionTx(ctx context.Context, tx sqlx.Session, tenant, id string, expected int64, active, status string) error {
	if err := m.DocumentsModel.UpdateActiveVersionTx(ctx, tx, tenant, id, expected, active, status); err != nil {
		return err
	}
	return errors.New("synthetic failure after layout activation update")
}

func TestLayoutUploadReviewAndImmutableParser(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic layouts", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{"columns", "table"} {
		req := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: layout, Filename: "layout.pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-synthetic")), PdfLayout: layout, IdempotencyKey: uuid.NewString()}
		version, err := UploadDocument(admin, s, req)
		if err != nil || version.PdfLayout != layout {
			t.Fatal("layout upload", err)
		}
		same, err := UploadDocument(admin, s, req)
		if err != nil || same.Id != version.Id {
			t.Fatal("layout replay", err)
		}
		req.PdfLayout = "plain"
		if _, err = UploadDocument(admin, s, req); err == nil {
			t.Fatal("changed layout reused request key")
		}
		row, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
		if err != nil {
			t.Fatal(err)
		}
		source, err := GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: row.JobId})
		if err != nil || source.PdfLayout != layout {
			t.Fatal("source layout", err)
		}
		done := &types.IndexCompleteReq{VersionId: row.Id, JobId: row.JobId, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 1, PageTexts: []types.KnowledgePageText{{PageNumber: 1, Text: "专业设备    六十个月"}}, ParserVersion: pdfParserVersion}
		if _, err = CompleteIndex(context.Background(), s, done); err == nil {
			t.Fatal("plain parser accepted for layout")
		}
		done.ParserVersion = map[string]string{"columns": columnsParserVersion, "table": tableParserVersion}[layout]
		if _, err = CompleteIndex(context.Background(), s, done); err != nil {
			t.Fatal(err)
		}
		if _, err = CompleteIndex(context.Background(), s, done); err != nil {
			t.Fatal("callback replay", err)
		}
		changed := *done
		changed.PageTexts = []types.KnowledgePageText{{PageNumber: 1, Text: "mutated"}}
		if _, err = CompleteIndex(context.Background(), s, &changed); err == nil {
			t.Fatal("changed extraction accepted")
		}
		activate := &types.ActivateDocumentReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedVersion: 1, ConfirmOcr: true}
		if _, err = ActivateDocument(admin, s, activate); err == nil {
			t.Fatal("OCR confirmation bypassed layout review")
		}
		activate.ConfirmLayout = true
		if _, err = ActivateDocument(operator, s, activate); err == nil {
			t.Fatal("operator confirmed layout")
		}
		foreignUser := *Current(admin).User
		foreignUser.TenantId = uuid.NewString()
		foreign := WithIdentity(context.Background(), &Identity{User: &foreignUser})
		if _, err = ActivateDocument(foreign, s, activate); err == nil {
			t.Fatal("foreign tenant confirmed layout")
		}
		originalModel := s.DocumentsModel
		s.DocumentsModel = failedLayoutActivationModel{originalModel}
		if _, err = ActivateDocument(admin, s, activate); err == nil {
			t.Fatal("expected activation rollback")
		}
		s.DocumentsModel = originalModel
		var rolledBackAudits int64
		if err = s.Conn.QueryRowCtx(context.Background(), &rolledBackAudits, "select count(*) from business.audit_events where object_id=$1 and operation='knowledge.layout_review'", row.Id); err != nil || rolledBackAudits != 0 {
			t.Fatal("review audit survived failed activation", err)
		}
		draft, err := s.DocumentsModel.FindOne(context.Background(), row.DocumentId)
		if err != nil || draft.Version != 1 || draft.ActiveVersionId.Valid || draft.Status != "draft" {
			t.Fatal("activation survived rollback", err)
		}
		active, err := ActivateDocument(admin, s, activate)
		if err != nil {
			t.Fatal(err)
		}
		var audits int64
		if err = s.Conn.QueryRowCtx(context.Background(), &audits, "select count(*) from business.audit_events where object_id=$1 and operation='knowledge.layout_review'", row.Id); err != nil || audits != 1 {
			t.Fatal("review audit", err)
		}
		if _, err = ActivateDocument(admin, s, activate); err == nil {
			t.Fatal("stale activation accepted")
		}
		if err = s.Conn.QueryRowCtx(context.Background(), &audits, "select count(*) from business.audit_events where object_id=$1 and operation='knowledge.layout_review'", row.Id); err != nil || audits != 1 {
			t.Fatal("stale request created audit", err)
		}
		listed, err := ListDocumentVersions(admin, s, row.DocumentId)
		if err != nil || listed.Items[0].PdfLayout != layout {
			t.Fatal("listed layout", err)
		}
		if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: row.DocumentId, ExpectedVersion: active.Version}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLayoutValidationAndPlainUploadFingerprint(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("%PDF-synthetic"))
	for _, input := range []struct{ filename, content, encoded, ocr, layout string }{
		{"policy.txt", "text", "", "off", "table"}, {"a.pdf", "", raw, "auto", "columns"}, {"a.pdf", "", raw, "force", "table"}, {"a.pdf", "", raw, "off", "guess"},
	} {
		if _, err := knowledgeUploadWithLayout(input.filename, input.content, input.encoded, input.ocr, input.layout); err == nil {
			t.Fatal("invalid layout accepted", input)
		}
	}
	for _, input := range []struct{ layout, parser, ocr, method string }{
		{"plain", columnsParserVersion, "off", ""}, {"columns", tableParserVersion, "off", ""}, {"table", ocrParserVersion, "off", "ocr"}, {"columns", columnsParserVersion, "off", "text"},
	} {
		row := &model.DocumentVersions{Id: "version", ContentHash: "hash", SourceFormat: "pdf", PdfLayout: input.layout, OcrMode: input.ocr, IndexStatus: "pending"}
		req := &types.IndexCompleteReq{VersionId: row.Id, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 1, ParserVersion: input.parser, PageTexts: []types.KnowledgePageText{{PageNumber: 1, Text: "synthetic", ExtractionMethod: input.method}}}
		if applyIndexResult(row, req) == nil {
			t.Fatal("layout provenance mismatch accepted", input)
		}
	}
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Legacy default", ReadRoles: []string{"admin"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	req := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "Legacy plain", Filename: "plain.pdf", ContentBase64: raw, IdempotencyKey: uuid.NewString()}
	result, err := UploadDocument(admin, s, req)
	if err != nil || result.PdfLayout != "plain" {
		t.Fatal("default", err)
	}
	// Historical generated request shape, before pdf_layout was introduced.
	legacy := struct {
		OcrMode         string `json:"ocr_mode,optional"`
		KnowledgeBaseId string `path:"knowledge_base_id"`
		Title           string `json:"title"`
		Filename        string `json:"filename"`
		Content         string `json:"content,optional"`
		ContentBase64   string `json:"content_base64,optional"`
		IdempotencyKey  string `header:"Idempotency-Key"`
	}{"off", base.Id, req.Title, req.Filename, "", raw, req.IdempotencyKey}
	var storedHash string
	if err = s.Conn.QueryRowCtx(context.Background(), &storedHash, "select request_hash from business.api_idempotency where key_hash=$1", HashToken(req.IdempotencyKey)); err != nil || storedHash != HashToken(marshal(legacy)) {
		t.Fatal("legacy fingerprint changed", err)
	}
	req.PdfLayout = "plain"
	replay, err := UploadDocument(admin, s, req)
	if err != nil || replay.Id != result.Id {
		t.Fatal("explicit plain differed from default", err)
	}
}
