package logic

import (
	"context"
	"encoding/base64"
	"flowdesk/db/model"
	"flowdesk/internal/types"
	"github.com/google/uuid"
	"testing"
)

func TestOCRModeReviewAndTrustedEvidence(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "OCR synthetic", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	upload := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "OCR policy", Filename: "scan.pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-synthetic")), OcrMode: "auto", IdempotencyKey: uuid.NewString()}
	version, err := UploadDocument(admin, s, upload)
	if err != nil || version.OcrMode != "auto" {
		t.Fatal("mode", err)
	}
	upload.OcrMode = "force"
	if _, err = UploadDocument(admin, s, upload); err == nil {
		t.Fatal("idempotency accepted changed OCR mode")
	}
	row, _ := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	source, err := GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: row.JobId})
	if err != nil || source.OcrMode != "auto" {
		t.Fatal("source mode", err)
	}
	pages := []types.KnowledgePageText{{PageNumber: 1, Text: "Native policy", ExtractionMethod: "text"}, {PageNumber: 2, Text: "保修三十六个月", ExtractionMethod: "ocr"}}
	done := &types.IndexCompleteReq{VersionId: row.Id, JobId: row.JobId, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 2, PageTexts: pages, ParserVersion: ocrParserVersion}
	ready, err := CompleteIndex(context.Background(), s, done)
	if err != nil || ready.OcrPageCount != 1 {
		t.Fatal("OCR result", err)
	}
	if _, err = CompleteIndex(context.Background(), s, done); err != nil {
		t.Fatal("replay", err)
	}
	activation := &types.ActivateDocumentReq{DocumentId: version.DocumentId, VersionId: version.Id, ExpectedVersion: 1}
	if _, err = ActivateDocument(admin, s, activation); err == nil {
		t.Fatal("OCR activated without review")
	}
	activation.ConfirmOcr = true
	if _, err = ActivateDocument(ctx, s, activation); err == nil {
		t.Fatal("operator reviewed OCR")
	}
	active, err := ActivateDocument(admin, s, activation)
	if err != nil {
		t.Fatal(err)
	}
	var audits int64
	if err = s.Conn.QueryRowCtx(context.Background(), &audits, "select count(*) from business.audit_events where object_id=$1 and operation='knowledge.ocr_review'", version.Id); err != nil || audits != 1 {
		t.Fatal("review audit", err)
	}
	listed, err := ListDocumentVersions(admin, s, version.DocumentId)
	if err != nil || listed.Items[0].OcrMode != "auto" || listed.Items[0].OcrPageCount != 1 {
		t.Fatal("listing", err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.started", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	candidate := types.EvidenceCandidate{VersionId: version.Id, ChunkId: uuid.NewString(), PageNumber: 2, Excerpt: pages[1].Text, ContentHash: HashToken(pages[1].Text)}
	checked, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{candidate}}, lease.Capability)
	if err != nil || len(checked.Items) != 1 || checked.Items[0].ExtractionMethod != "ocr" {
		t.Fatal("trusted OCR provenance", err)
	}
	citation, err := GetCitation(ctx, s, checked.Items[0].Id)
	if err != nil || citation.ExtractionMethod != "ocr" {
		t.Fatal("citation provenance", err)
	}
	if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: active.Id, ExpectedVersion: active.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = GetCitation(ctx, s, checked.Items[0].Id); err == nil {
		t.Fatal("withdrawn OCR readable")
	}
}

func TestOCRCallbackAndUploadBoundaries(t *testing.T) {
	for _, mode := range []string{"auto", "force", "untrusted"} {
		if _, err := knowledgeUploadWithOCR("policy.txt", "synthetic", "", mode); err == nil {
			t.Fatal("text accepted OCR mode")
		}
	}
	for _, test := range []struct{ mode, parser, method string }{
		{"off", ocrParserVersion, "ocr"}, {"force", pdfParserVersion, ""}, {"auto", ocrParserVersion, "text"}, {"auto", ocrParserVersion, "empty"}, {"auto", pdfParserVersion, "ocr"},
	} {
		row := &model.DocumentVersions{Id: "version", ContentHash: "hash", SourceFormat: "pdf", OcrMode: test.mode, IndexStatus: "pending"}
		req := &types.IndexCompleteReq{VersionId: row.Id, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 1, ParserVersion: test.parser, PageTexts: []types.KnowledgePageText{{PageNumber: 1, Text: "synthetic", ExtractionMethod: test.method}}}
		if applyIndexResult(row, req) == nil {
			t.Fatal("accepted invalid provenance", test)
		}
	}
}
