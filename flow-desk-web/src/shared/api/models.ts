import type { paths } from "./generated";

type JsonResponse<T> = T extends {
  responses: { 200: { content: { "application/json": infer Body } } };
}
  ? Body
  : never;
type Data<T> =
  JsonResponse<T> extends { data?: infer Body } ? NonNullable<Body> : never;
export type Models = {
  SourceCopyHistoryPage: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/archive-copies/history"]["get"]
  >;
  SourceMetadataArchiveData: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/archive-copies/{object_id}/archive-metadata"]["post"]
  >;
  TenantSourceInventoryPage: Data<paths["/api/v1/source-inventory"]["get"]>;
  SourceInventoryPage: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/source-inventory"]["get"]
  >;
  VersionHistoryPage: Data<
    paths["/api/v1/documents/{document_id}/history"]["get"]
  >;
  VersionHistoryItem: Data<
    paths["/api/v1/documents/{document_id}/history"]["get"]
  >["items"][number];
  ArchiveCopiesData: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/archive-copies"]["get"]
  >;
  RetentionPlan: Data<
    paths["/api/v1/documents/{document_id}/retention"]["get"]
  >;
  RetentionItem: Data<
    paths["/api/v1/documents/{document_id}/retention"]["get"]
  >["items"][number];
  ManagedUserPage: Data<paths["/api/v1/admin/users"]["get"]>;
  ManagedRolePage: Data<paths["/api/v1/admin/roles"]["get"]>;
  OriginalSourceData: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/source"]["get"]
  >;
  RunOperationPage: Data<paths["/api/v1/runs"]["get"]>;
  RunTraceData: Data<paths["/api/v1/runs/{run_id}/trace"]["get"]>;
  TeamPage: Data<paths["/api/v1/teams"]["get"]>;
  TeamUserPage: Data<paths["/api/v1/team-users"]["get"]>;
  TicketData: Data<paths["/api/v1/tickets/{ticket_id}"]["get"]>;
  TicketPage: Data<paths["/api/v1/tickets"]["get"]>;
  TimelinePage: Data<paths["/api/v1/tickets/{ticket_id}/events"]["get"]>;
  KnowledgeBasePage: Data<paths["/api/v1/knowledge-bases"]["get"]>;
  RetentionPolicyData: Data<
    paths["/api/v1/knowledge-bases/{knowledge_base_id}/retention-policy"]["get"]
  >;
  DocumentPage: Data<
    paths["/api/v1/knowledge-bases/{knowledge_base_id}/documents"]["get"]
  >;
  VersionPage: Data<paths["/api/v1/documents/{document_id}/versions"]["get"]>;
  VersionPagesData: Data<
    paths["/api/v1/documents/{document_id}/versions/{version_id}/pages"]["get"]
  >;
  EvidenceData: Data<paths["/api/v1/citations/{evidence_id}"]["get"]>;
  ConversationData: Data<
    paths["/api/v1/conversations/{conversation_id}"]["get"]
  >;
  ConversationPage: Data<paths["/api/v1/conversations"]["get"]>;
  MessagePage: Data<
    paths["/api/v1/conversations/{conversation_id}/messages"]["get"]
  >;
  MessageData: Data<
    paths["/api/v1/conversations/{conversation_id}/messages"]["get"]
  >["items"][number];
  RunData: Data<paths["/api/v1/runs/{run_id}"]["get"]>;
  EventData: JsonResponse<paths["/api/v1/runs/{run_id}/events"]["get"]>;
  OrderPage: Data<paths["/api/v1/orders"]["get"]>;
  LogisticsPage: Data<paths["/api/v1/orders/{order_id}/logistics"]["get"]>;
};
