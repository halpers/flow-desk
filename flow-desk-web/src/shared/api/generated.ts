/** Generated from Go .api via OpenAPI. DO NOT EDIT. */
export interface paths {
    "/api/v1/auth/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Login | permission=- | auth=public */
        post: operations["login"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Logout | permission=- | auth=refresh_cookie */
        post: operations["logout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Current user | permission=- | auth=required */
        get: operations["me"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/auth/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Refresh | permission=- | auth=refresh_cookie */
        post: operations["refresh"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/conversations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List own conversations | permission=conversation:use | auth=required */
        get: operations["listConversations"];
        put?: never;
        /** Create conversation | permission=conversation:use | auth=required */
        post: operations["createConversation"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/conversations/{conversation_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get own conversation | permission=conversation:use | auth=required */
        get: operations["getConversation"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/conversations/{conversation_id}/messages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List messages | permission=conversation:use | auth=required */
        get: operations["listMessages"];
        put?: never;
        /** Send message | permission=conversation:use | auth=required */
        post: operations["sendMessage"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List orders | permission=order:read | auth=required */
        get: operations["listOrders"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/orders/{order_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get order | permission=order:read | auth=required */
        get: operations["getOrder"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/orders/{order_id}/logistics": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get logistics | permission=order:read | auth=required */
        get: operations["getLogistics"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/runs/{run_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Run snapshot | permission=conversation:use | auth=required */
        get: operations["getRun"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/runs/{run_id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Persisted event stream | permission=conversation:use | auth=required */
        get: operations["streamEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/health/live": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Liveness | permission=- | auth=public */
        get: operations["live"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/health/ready": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Readiness | permission=- | auth=public */
        get: operations["ready"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Persist agent event | permission=- | auth=run_capability */
        post: operations["ingestEvent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/lease": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Renew run capability | permission=- | auth=service */
        post: operations["renewLease"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/tools/{tool_name}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Read business tool | permission=order:read | auth=run_capability */
        post: operations["callTool"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: never;
    responses: never;
    parameters: never;
    requestBodies: never;
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    login: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    account: string;
                    password: string;
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            access_token: string;
                            expires_in: number;
                        };
                        message?: string;
                        request_id?: string;
                    };
                };
            };
        };
    };
    logout: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        message?: string;
                        request_id?: string;
                    };
                };
            };
        };
    };
    me: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            account: string;
                            display_name: string;
                            id: string;
                            permissions: string[];
                            role: string;
                            tenant_id: string;
                        };
                        message?: string;
                        request_id?: string;
                    };
                };
            };
        };
    };
    refresh: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            access_token: string;
                            expires_in: number;
                        };
                        message?: string;
                        request_id?: string;
                    };
                };
            };
        };
    };
    listConversations: {
        parameters: {
            query: {
                cursor?: string;
                /** @example 20 */
                limit: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            has_more: boolean;
                            items: {
                                active_run_id: string;
                                id: string;
                                status: string;
                                title: string;
                                updated_at: string;
                                version: number;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    createConversation: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    title: string;
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            active_run_id: string;
                            id: string;
                            status: string;
                            title: string;
                            updated_at: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getConversation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                conversation_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            active_run_id: string;
                            id: string;
                            status: string;
                            title: string;
                            updated_at: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listMessages: {
        parameters: {
            query: {
                cursor?: string;
                /** @example 20 */
                limit: number;
            };
            header?: never;
            path: {
                conversation_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            has_more: boolean;
                            items: {
                                action_receipts: {
                                    [key: string]: unknown;
                                }[];
                                citations: {
                                    [key: string]: unknown;
                                }[];
                                content: string;
                                created_at: string;
                                generation: number;
                                id: string;
                                position: number;
                                role: string;
                                run_id: string;
                                status: string;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    sendMessage: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                conversation_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    attachments?: string[];
                    client_message_id: string;
                    content: string;
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            conversation_id: string;
                            current_message: {
                                action_receipts: {
                                    [key: string]: unknown;
                                }[];
                                citations: {
                                    [key: string]: unknown;
                                }[];
                                content: string;
                                created_at: string;
                                generation: number;
                                id: string;
                                position: number;
                                role: string;
                                run_id: string;
                                status: string;
                            };
                            error_code: string;
                            id: string;
                            last_event_seq: number;
                            status: string;
                            tool_call_count: number;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listOrders: {
        parameters: {
            query: {
                cursor?: string;
                /** @example 20 */
                limit: number;
                order_no?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            has_more: boolean;
                            items: {
                                currency: string;
                                id: string;
                                masked_customer_name: string;
                                order_no: string;
                                placed_at: string;
                                status: string;
                                total_amount: string;
                                version: number;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            currency: string;
                            id: string;
                            masked_customer_name: string;
                            order_no: string;
                            placed_at: string;
                            status: string;
                            total_amount: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getLogistics: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            items: {
                                description: string;
                                id: string;
                                occurred_at: string;
                                source_updated_at: string;
                                status: string;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getRun: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            conversation_id: string;
                            current_message: {
                                action_receipts: {
                                    [key: string]: unknown;
                                }[];
                                citations: {
                                    [key: string]: unknown;
                                }[];
                                content: string;
                                created_at: string;
                                generation: number;
                                id: string;
                                position: number;
                                role: string;
                                run_id: string;
                                status: string;
                            };
                            error_code: string;
                            id: string;
                            last_event_seq: number;
                            status: string;
                            tool_call_count: number;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    streamEvents: {
        parameters: {
            query: {
                /** @example 0 */
                after_seq: number;
            };
            header?: never;
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        occurred_at?: string;
                        payload?: {
                            [key: string]: unknown;
                        };
                        run_id?: string;
                        schema_version?: string;
                        seq?: number;
                        type?: string;
                    };
                };
            };
        };
    };
    live: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        service?: string;
                        status?: string;
                    };
                };
            };
        };
    };
    ready: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        service?: string;
                        status?: string;
                    };
                };
            };
        };
    };
    ingestEvent: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    lease_generation: number;
                    occurred_at: string;
                    payload: {
                        [key: string]: unknown;
                    };
                    source_event_id: string;
                    type: string;
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            seq: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    renewLease: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    dispatch_id: string;
                    lease_generation: number;
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            actor_user_id: string;
                            capability: string;
                            content: string;
                            deadline_at: string;
                            lease_generation: number;
                            message_id: string;
                            run_id: string;
                            tenant_id: string;
                            tools: string[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    callTool: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                tool_name: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    arguments: {
                        [key: string]: unknown;
                    };
                };
            };
        };
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            logistics?: {
                                description: string;
                                id: string;
                                occurred_at: string;
                                source_updated_at: string;
                                status: string;
                            }[];
                            order?: {
                                currency: string;
                                id: string;
                                masked_customer_name: string;
                                order_no: string;
                                placed_at: string;
                                status: string;
                                total_amount: string;
                                version: number;
                            };
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
}
