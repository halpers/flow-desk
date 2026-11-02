import type { paths } from "./generated";

type JsonResponse<T> = T extends {
  responses: { 200: { content: { "application/json": infer Body } } };
}
  ? Body
  : never;
type Data<T> =
  JsonResponse<T> extends { data?: infer Body } ? NonNullable<Body> : never;
export type Models = {
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
