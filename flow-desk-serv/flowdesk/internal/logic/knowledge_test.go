package logic

import (
	"context"
	"strings"
	"testing"

	"flowdesk/db/model"
	"flowdesk/internal/svc"
	"flowdesk/internal/types"
	"github.com/google/uuid"
)

func knowledgeAdminContext(t *testing.T, s *svc.ServiceContext, operator context.Context) context.Context {
	t.Helper()
	row := *Current(operator).User
	row.Id = uuid.NewString()
	row.Account = "admin"
	row.Role = "admin"
	if _, err := s.UsersModel.Insert(context.Background(), &row); err != nil {
		t.Fatal(err)
	}
	token, _, err := Login(context.Background(), s, &types.LoginReq{Account: "admin", Password: "integration-password"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := Authenticate(context.Background(), s, token.Data.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	return WithIdentity(context.Background(), identity)
}
func TestKnowledgeActivationEvidenceAndWithdrawal(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	roles := []string{"admin", "operator", "supervisor"}
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Synthetic policy", ReadRoles: roles, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreateKnowledgeBase(ctx, s, &types.KnowledgeBaseWriteReq{Name: "forbidden", ReadRoles: roles, IdempotencyKey: uuid.NewString()}); err == nil {
		t.Fatal("operator created knowledge base")
	}
	content := "合成退货政策：签收后七天内可申请退货，商品须完好。"
	upload := &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "退货政策", Filename: "return.md", Content: content, IdempotencyKey: uuid.NewString()}
	version, err := UploadDocument(admin, s, upload)
	if err != nil {
		t.Fatal(err)
	}
	same, err := UploadDocument(admin, s, upload)
	if err != nil || same.Id != version.Id {
		t.Fatal("upload idempotency", err)
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: version.DocumentId, VersionId: version.Id, ExpectedVersion: 1}); err == nil {
		t.Fatal("activated pending index")
	}
	raw, err := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if err != nil {
		t.Fatal(err)
	}
	complete := &types.IndexCompleteReq{VersionId: version.Id, JobId: raw.JobId, ContentHash: raw.ContentHash, ChunkCount: 1, Status: "ready"}
	if _, err = CompleteIndex(context.Background(), s, complete); err != nil {
		t.Fatal(err)
	}
	active, err := ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: version.DocumentId, VersionId: version.Id, ExpectedVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.started", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	scope, err := GetKnowledgeScope(context.Background(), s, run.Id, lease.Capability)
	if err != nil || len(scope.Versions) != 1 {
		t.Fatal("scope", err)
	}
	candidate := types.EvidenceCandidate{VersionId: version.Id, ChunkId: uuid.NewString(), Excerpt: content, ContentHash: HashToken(content)}
	checked, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{candidate}}, lease.Capability)
	if err != nil || len(checked.Items) != 1 {
		t.Fatal("evidence", err)
	}
	candidate.Excerpt = "虚构退款条款"
	candidate.ContentHash = HashToken(candidate.Excerpt)
	if _, err = ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{candidate}}, lease.Capability); err == nil {
		t.Fatal("accepted fabricated excerpt")
	}
	evidence := checked.Items[0]
	payload := map[string]any{"message_id": lease.MessageId, "content": content, "citations": []interface{}{map[string]any{"id": evidence.Id}}, "action_receipts": []interface{}{}}
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "message.completed", payload), lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.succeeded", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err = GetCitation(ctx, s, evidence.Id); err != nil {
		t.Fatal(err)
	}
	if _, err = WithdrawDocument(admin, s, &types.WithdrawDocumentReq{DocumentId: active.Id, ExpectedVersion: active.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = GetCitation(ctx, s, evidence.Id); err == nil {
		t.Fatal("withdrawn citation exposed")
	}
	snapshot, err := GetRun(ctx, s, run.Id)
	if err != nil || strings.Contains(snapshot.CurrentMessage.Content, "七天") {
		t.Fatal("withdrawn snapshot", err)
	}
	events, err := ListRunEvents(ctx, s, run.Id, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "message.completed" && strings.Contains(marshal(event.Payload), "七天") {
			t.Fatal("SSE replay exposed withdrawn policy")
		}
	}
	if _, err = CompleteIndex(context.Background(), s, complete); err != nil {
		t.Fatal("callback replay", err)
	}
	doc, err := s.DocumentsModel.FindOne(context.Background(), active.Id)
	if err != nil || doc.ActiveVersionId.Valid {
		t.Fatal("late callback reactivated document", err)
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: active.Id, VersionId: version.Id, ExpectedVersion: 1}); err == nil {
		t.Fatal("stale activation accepted")
	}
}

func TestKnowledgeTenantAndAclRevocationBeforeFinal(t *testing.T) {
	s := testService(t)
	ctx, _ := taskContext(t, s)
	admin := knowledgeAdminContext(t, s, ctx)
	base, err := CreateKnowledgeBase(admin, s, &types.KnowledgeBaseWriteReq{Name: "Policy", ReadRoles: []string{"admin", "operator"}, IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	version, err := UploadDocument(admin, s, &types.UploadDocumentReq{KnowledgeBaseId: base.Id, Title: "保修", Filename: "warranty.txt", Content: "合成保修政策：十二个月。", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := s.DocumentVersionsModel.FindOne(context.Background(), version.Id)
	if _, err = CompleteIndex(context.Background(), s, &types.IndexCompleteReq{VersionId: raw.Id, JobId: raw.JobId, ContentHash: raw.ContentHash, ChunkCount: 1, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = ActivateDocument(admin, s, &types.ActivateDocumentReq{DocumentId: raw.DocumentId, VersionId: raw.Id, ExpectedVersion: 1}); err != nil {
		t.Fatal(err)
	}
	run := newTask(t, ctx, s, newConversation(t, ctx, s).Id)
	lease := leaseTask(t, s, run.Id)
	if _, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "run.started", map[string]any{}), lease.Capability); err != nil {
		t.Fatal(err)
	}
	checked, err := ValidateEvidence(context.Background(), s, &types.EvidenceReq{RunId: run.Id, Candidates: []types.EvidenceCandidate{{VersionId: raw.Id, ChunkId: uuid.NewString(), Excerpt: raw.Content, ContentHash: raw.ContentHash}}}, lease.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = UpdateKnowledgeAcl(admin, s, &types.KnowledgeAclReq{KnowledgeBaseId: base.Id, ExpectedRevision: 1, ReadRoles: []string{"admin"}}); err != nil {
		t.Fatal(err)
	}
	scope, err := GetKnowledgeScope(context.Background(), s, run.Id, lease.Capability)
	if err != nil || len(scope.Versions) != 0 {
		t.Fatal("revoked scope", err)
	}
	_, err = IngestEvent(context.Background(), s, eventRequest(run.Id, "message.completed", map[string]any{"message_id": lease.MessageId, "content": raw.Content, "citations": []interface{}{map[string]any{"id": checked.Items[0].Id}}, "action_receipts": []interface{}{}}), lease.Capability)
	if err == nil {
		t.Fatal("final accepted revoked evidence")
	}
	tenant := uuid.NewString()
	if _, err = s.TenantsModel.Insert(context.Background(), &model.Tenants{Id: tenant, Code: "other", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	user := *Current(admin).User
	user.TenantId = tenant
	other := WithIdentity(context.Background(), &Identity{User: &user})
	if _, err = ListDocuments(other, s, base.Id); err == nil {
		t.Fatal("cross-tenant knowledge access")
	}
}
