package logic

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func TestPDFSourceAndImmutablePageEvidence(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "PDF policies", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	// Go stores binary bytes verbatim; parser behavior is covered with real PDF fixtures in Python.
	original := []byte("%PDF-1.7\n\x00\xff synthetic binary source")
	req := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "PDF保修", Filename: "policy.pdf", ContentBase64: base64.StdEncoding.EncodeToString(original), IdempotencyKey: uuid.NewString()}
	if _, err = UploadDocument(ctx, s, req); err == nil {
		t.Fatal("operator uploaded PDF")
	}
	version, err := UploadDocument(admin, s, req)
	if err != nil {
		t.Fatal(err)
	}
	same, err := UploadDocument(admin, s, req)
	if err != nil || same.Id != version.Id {
		t.Fatal("upload replay", err)
	}
	row, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if err != nil {
		t.Fatal(err)
	}
	if string(row.SourceBytes) != string(original) || row.Content != "" || version.SourceSize != int64(len(original)) {
		t.Fatal("binary source changed")
	}
	source, err := GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: row.JobId})
	if err != nil || source.SourceBase64 != req.ContentBase64 || source.ContentHash != HashToken(string(original)) {
		t.Fatal("source provenance", err)
	}
	if _, err = GetIndexSource(context.Background(), s, &types.IndexSourceReq{VersionId: row.Id, JobId: uuid.NewString()}); err == nil {
		t.Fatal("foreign job read source")
	}
	pages := []types.KnowledgePageText{{PageNumber: 1, Text: "配送三个工作日"}, {PageNumber: 2, Text: "合成保修：三十六个月"}, {PageNumber: 3, Text: ""}}
	done := &types.IndexCompleteReq{VersionId: row.Id, JobId: row.JobId, ContentHash: row.ContentHash, Status: "ready", ChunkCount: 2, PageTexts: pages, ParserVersion: pdfParserVersion}
	bad := *done
	bad.PageTexts = []types.KnowledgePageText{{PageNumber: 2, Text: "wrong order"}}
	if _, err = CompleteIndex(context.Background(), s, &bad); err == nil {
		t.Fatal("accepted nonsequential pages")
	}
	result, err := CompleteIndex(context.Background(), s, done)
	if err != nil || result.PageCount != 3 || result.TextPageCount != 2 {
		t.Fatal("complete", err)
	}
	if _, err = CompleteIndex(context.Background(), s, done); err != nil {
		t.Fatal("completion replay", err)
	}
	bad = *done
	bad.PageTexts = []types.KnowledgePageText{{PageNumber: 1, Text: "mutated"}}
	if _, err = CompleteIndex(context.Background(), s, &bad); err == nil {
		t.Fatal("overwrote committed extraction")
	}
	bad = *done
	bad.Status = "failed"
	bad.ChunkCount = 0
	bad.PageTexts = nil
	bad.ParserVersion = ""
	bad.ErrorCode = "PDF_INVALID"
	if _, err = CompleteIndex(context.Background(), s, &bad); err == nil {
		t.Fatal("late failure overwrote ready result")
	}
	previewReq := &types.VersionPagesReq{DocumentId: row.DocumentId, VersionId: row.Id}
	if _, err = PreviewVersionPages(ctx, s, previewReq); err == nil {
		t.Fatal("operator read full PDF")
	}
	foreignUser := *Current(admin).User
	foreignUser.TenantId = uuid.NewString()
	foreign := WithIdentity(context.Background(), &Identity{User: &foreignUser})
	if _, err = PreviewVersionPages(foreign, s, previewReq); err == nil {
		t.Fatal("cross-tenant PDF preview")
	}
	preview, err := PreviewVersionPages(admin, s, previewReq)
	if err != nil || len(preview.Pages) != 3 {
		t.Fatal("preview", err)
	}
	wrong := *previewReq
	wrong.DocumentId = uuid.NewString()
	if _, err = PreviewVersionPages(admin, s, &wrong); err == nil {
		t.Fatal("preview ignored document identity")
	}
	active, err := ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: row.DocumentId, VersionId: row.Id, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.started", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	candidate := types.EvidenceCandidate{VersionId: row.Id, ChunkId: uuid.NewString(), Excerpt: pages[1].Text, ContentHash: HashToken(pages[1].Text), PageNumber: 1}
	evidenceReq := &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{candidate}}
	if _, err = ValidateEvidence(context.Background(), s, evidenceReq, lease.Capability); err == nil {
		t.Fatal("accepted wrong page")
	}
	evidenceReq.Candidates[0].PageNumber = 0
	if _, err = ValidateEvidence(context.Background(), s, evidenceReq, lease.Capability); err == nil {
		t.Fatal("accepted missing PDF page")
	}
	evidenceReq.Candidates[0].PageNumber = 2
	checked, err := ValidateEvidence(context.Background(), s, evidenceReq, lease.Capability)
	if err != nil || checked.Items[0].PageNumber != 2 {
		t.Fatal("page evidence", err)
	}
	payload := map[string]any{"message_id": lease.MessageId, "content": pages[1].Text, "citations": []any{map[string]any{"id": checked.Items[0].Id, "page_number": 49}}, "action_receipts": []any{}}
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "message.completed", payload), lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.succeeded", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || !strings.Contains(marshal(snapshot.CurrentMessage.Citations), `"page_number":2`) {
		t.Fatal("canonical page", err)
	}
	if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: active.Id, ExpectedVersion: active.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = GetCitation(ctx, s, checked.Items[0].Id); err == nil {
		t.Fatal("withdrawn PDF exposed")
	}
	if _, err = CompleteIndex(context.Background(), s, done); err != nil {
		t.Fatal("late callback", err)
	}
	doc, _ := s.DocumentsModel.FindOne(context.Background(), active.Id)
	if doc.ActiveVersionId.Valid {
		t.Fatal("late callback activated PDF")
	}
}

func TestKnowledgeFileBounds(t *testing.T) {
	for _, input := range []struct{ filename, content, encoded string }{
		{"../policy.pdf", "", "JVBERi0="}, {"policy.pdf", "mixed", "JVBERi0="},
		{"policy.pdf", "", "bad-base64"}, {"policy.txt", "", "JVBERi0="},
		{"policy.pdf", "", base64.StdEncoding.EncodeToString([]byte("not PDF"))},
		{"policy.pdf", "", base64.StdEncoding.EncodeToString([]byte("%PDF-" + strings.Repeat("x", maxPDFBytes)))},
	} {
		if _, err := knowledgeUpload(input.filename, input.content, input.encoded); err == nil {
			t.Fatal("accepted invalid upload", input.filename)
		}
	}
}
