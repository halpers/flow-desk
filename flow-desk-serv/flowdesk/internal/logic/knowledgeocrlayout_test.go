package logic

import (
	"context"
	"encoding/base64"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/domain"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func TestOCRLayoutUploadDualReviewAndCitations(t *testing.T) {
	s := testService(t)
	operator, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, operator)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic joint extraction", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range []string{"columns", "table"} {
		for _, mode := range []string{"auto", "force"} {
			t.Run(layout+"/"+mode, func(t *testing.T) {
				request := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: layout + mode, Filename: "scan.pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF-synthetic")), PdfLayout: layout, OcrMode: mode, IdempotencyKey: uuid.NewString()}
				version, err := UploadDocument(admin, s, request)
				if err != nil || version.PdfLayout != layout || version.OcrMode != mode {
					t.Fatal("combined upload", err)
				}
				replay, err := UploadDocument(admin, s, request)
				if err != nil || replay.Id != version.Id {
					t.Fatal("combined replay", err)
				}
				changed := *request
				changed.PdfLayout = "plain"
				if _, err = UploadDocument(admin, s, &changed); err == nil {
					t.Fatal("changed layout reused key")
				}
				row, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
				if err != nil {
					t.Fatal(err)
				}
				source, err := GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: row.JobId})
				if err != nil || source.OcrMode != mode || source.PdfLayout != layout {
					t.Fatal("trusted source", err)
				}
				pages := []types.KnowledgePageText{{PageNumber: 1, Text: "Native policy", ExtractionMethod: "ocr"}, {PageNumber: 2, Text: "专业设备    六十个月    设备编号与发票", ExtractionMethod: "ocr"}}
				if mode == "auto" {
					pages[0].ExtractionMethod = "text"
				}
				done := &types.IndexCompleteReq{VersionId: row.Id, JobId: row.JobId, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 2, PageTexts: pages, ParserVersion: map[string]string{"columns": ocrColumnsParserVersion, "table": ocrTableParserVersion}[layout]}
				ready, err := CompleteIndex(context.Background(), s, done)
				if err != nil || ready.OcrPageCount == 0 {
					t.Fatal("combined callback", err)
				}
				if _, err = CompleteIndex(context.Background(), s, done); err != nil {
					t.Fatal("callback replay", err)
				}
				altered := *done
				altered.PageTexts = []types.KnowledgePageText{{PageNumber: 1, Text: "mutated", ExtractionMethod: "ocr"}}
				if _, err = CompleteIndex(context.Background(), s, &altered); err == nil {
					t.Fatal("mutated replay accepted")
				}
				for _, flags := range [][2]bool{{false, false}, {true, false}, {false, true}} {
					req := &types.ActivateDocumentReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedVersion: 1, ConfirmOcr: flags[0], ConfirmLayout: flags[1]}
					if _, err = ActivateDocument(admin, s, req); err == nil {
						t.Fatal("missing one review accepted", flags)
					}
				}
				activation := &types.ActivateDocumentReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedVersion: 1, ConfirmOcr: true, ConfirmLayout: true}
				if _, err = ActivateDocument(operator, s, activation); err == nil {
					t.Fatal("operator reviewed combined manifest")
				}
				original := s.DocumentsModel
				s.DocumentsModel = failedLayoutActivationModel{original}
				_, err = ActivateDocument(admin, s, activation)
				s.DocumentsModel = original
				if err == nil {
					t.Fatal("expected activation rollback")
				}
				var audits int64
				if err = s.Conn.QueryRowCtx(context.Background(), &audits, "select count(*) from business.audit_events where object_id=$1 and operation in ('knowledge.ocr_review','knowledge.layout_review')", row.Id); err != nil || audits != 0 {
					t.Fatal("partial review audit survived", err)
				}
				active, err := ActivateDocument(admin, s, activation)
				if err != nil {
					t.Fatal(err)
				}
				if err = s.Conn.QueryRowCtx(context.Background(), &audits, "select count(*) from business.audit_events where object_id=$1 and operation in ('knowledge.ocr_review','knowledge.layout_review')", row.Id); err != nil || audits != 2 {
					t.Fatal("both review audits missing", err)
				}
				run := newTask(t, operator, s, newConversation(t, operator, s).Id)
				lease := leaseTask(t, s, run.Id)
				if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.started", map[string]any{}), lease.Capability); err != nil {
					t.Fatal(err)
				}
				evidence, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{{VersionId: row.Id, ChunkId: uuid.NewString(), PageNumber: 2, Excerpt: pages[1].Text, ContentHash: HashToken(pages[1].Text)}}}, lease.Capability)
				if err != nil || len(evidence.Items) != 1 || evidence.Items[0].ExtractionMethod != "ocr" {
					t.Fatal("page provenance", err)
				}
				citation, err := GetCitation(operator, s, evidence.Items[0].Id)
				if err != nil || citation.PageNumber != 2 || citation.ExtractionMethod != "ocr" {
					t.Fatal("trusted citation", err)
				}
				completed := eventRequest(run.Id, domain.EventMessageCompleted, map[string]any{"message_id": run.CurrentMessage.Id, "content": "Synthetic OCR evidence checked", "citations": []any{}, "action_receipts": []any{}})
				if _, err = IngestEvent(context.Background(), s, completed, lease.Capability); err != nil {
					t.Fatal("complete synthetic message", err)
				}
				if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, domain.EventRunSucceeded, map[string]any{}), lease.Capability); err != nil {
					t.Fatal("complete synthetic run", err)
				}
				if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: row.DocumentId, ExpectedVersion: active.Version}); err != nil {
					t.Fatal(err)
				}
				if _, err = GetCitation(operator, s, evidence.Items[0].Id); err == nil {
					t.Fatal("withdrawn combined citation readable")
				}
			})
		}
	}
}

func TestOCRLayoutCallbackParserAndProvenanceMatrix(t *testing.T) {
	for _, input := range []struct {
		layout, mode, parser, method string
		valid                        bool
	}{
		{"columns", "auto", ocrColumnsParserVersion, "ocr", true},
		{"table", "force", ocrTableParserVersion, "ocr", true},
		{"table", "auto", tableParserVersion, "", true},
		{"columns", "force", columnsParserVersion, "", false},
		{"columns", "off", ocrColumnsParserVersion, "ocr", false},
		{"columns", "auto", ocrTableParserVersion, "ocr", false},
		{"table", "auto", ocrParserVersion, "ocr", false},
		{"plain", "auto", ocrTableParserVersion, "ocr", false},
		{"table", "auto", ocrTableParserVersion, "text", false},
		{"table", "auto", ocrTableParserVersion, "", false},
		{"table", "auto", tableParserVersion, "ocr", false},
	} {
		row := &model.DocumentVersions{Id: "version", ContentHash: "hash", SourceFormat: "pdf", PdfLayout: input.layout, OcrMode: input.mode, IndexStatus: "pending"}
		req := &types.IndexCompleteReq{VersionId: row.Id, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 1, ParserVersion: input.parser, PageTexts: []types.KnowledgePageText{{PageNumber: 1, Text: "synthetic", ExtractionMethod: input.method}}}
		err := applyIndexResult(row, req)
		if (err == nil) != input.valid {
			t.Fatalf("unexpected parser result %+v: %v", input, err)
		}
	}
}
