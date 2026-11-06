/** Generated from Go .api via OpenAPI. DO NOT EDIT. */
export interface paths {
    "/api/v1/admin/roles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tenant roles and permission catalog | permission=account:manage or role:manage | auth=required */
        get: operations["listManagedRoles"];
        put?: never;
        /** Create additive role | permission=role:manage | auth=required */
        post: operations["createManagedRole"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/admin/roles/{role_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Update additive role | permission=role:manage | auth=required */
        put: operations["updateManagedRole"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/admin/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tenant account directory | permission=account:manage | auth=required */
        get: operations["listManagedUsers"];
        put?: never;
        /** Create tenant account | permission=account:manage | auth=required */
        post: operations["createManagedUser"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/admin/users/{user_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Update account and roles | permission=account:manage | auth=required */
        put: operations["updateManagedUser"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/admin/users/{user_id}/reset-password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Reset account password | permission=account:manage | auth=required */
        post: operations["resetManagedPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/approvals/{approval_id}/decision": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Decide immutable action | permission=conversation:use | auth=required */
        post: operations["decideApproval"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/audit-events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tenant audit | permission=audit:read | auth=required */
        get: operations["listAuditEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
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
    "/api/v1/citations/{evidence_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetCitation | permission=conversation:use | auth=required */
        get: operations["getCitation"];
        put?: never;
        post?: never;
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
    "/api/v1/documents/{document_id}/activate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** ActivateDocument | permission=knowledge:manage | auth=required */
        post: operations["activateDocument"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListVersionHistory | permission=knowledge:manage | auth=required */
        get: operations["listVersionHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/retention": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetRetentionPlan | permission=knowledge:manage | auth=required */
        get: operations["getRetentionPlan"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListDocumentVersions | permission=knowledge:manage | auth=required */
        get: operations["listDocumentVersions"];
        put?: never;
        /** UploadDocumentVersion | permission=knowledge:manage | auth=required */
        post: operations["uploadDocumentVersion"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** RequestSourceArchive | permission=knowledge:manage | auth=required */
        post: operations["requestSourceArchive"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive-copies": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListArchiveCopies | permission=knowledge:manage | auth=required */
        get: operations["listArchiveCopies"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive-copies/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListSourceCopyHistory | permission=knowledge:manage | auth=required */
        get: operations["listSourceCopyHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive-copies/{object_id}/archive-metadata": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** ArchiveSourceMetadata | permission=knowledge:manage | auth=required */
        post: operations["archiveSourceMetadata"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive-copies/{object_id}/cleanup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** CleanupArchiveCopy | permission=knowledge:manage | auth=required */
        post: operations["cleanupArchiveCopy"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/archive-metadata": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** ArchiveVersionMetadata | permission=knowledge:manage | auth=required */
        post: operations["archiveVersionMetadata"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/cleanup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** CleanupDocumentVersion | permission=knowledge:manage | auth=required */
        post: operations["cleanupDocumentVersion"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/pages": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** PreviewVersionPages | permission=knowledge:manage | auth=required */
        get: operations["previewVersionPages"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/restore-source": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** RestoreOriginalSource | permission=knowledge:manage | auth=required */
        post: operations["restoreOriginalSource"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/source": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** DownloadOriginalSource | permission=knowledge:manage | auth=required */
        get: operations["downloadOriginalSource"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/source-adoptions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** AdoptSourceObject | permission=knowledge:manage | auth=required */
        post: operations["adoptSourceObject"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/versions/{version_id}/source-inventory": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetSourceInventory | permission=knowledge:manage | auth=required */
        get: operations["getSourceInventory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/documents/{document_id}/withdraw": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** WithdrawDocument | permission=knowledge:manage | auth=required */
        post: operations["withdrawDocument"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/knowledge-bases": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListKnowledgeBases | permission=conversation:use | auth=required */
        get: operations["listKnowledgeBases"];
        put?: never;
        /** CreateKnowledgeBase | permission=knowledge:manage | auth=required */
        post: operations["createKnowledgeBase"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/knowledge-bases/{knowledge_base_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** UpdateKnowledgeAcl | permission=knowledge:manage | auth=required */
        patch: operations["updateKnowledgeAcl"];
        trace?: never;
    };
    "/api/v1/knowledge-bases/{knowledge_base_id}/documents": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListDocuments | permission=conversation:use | auth=required */
        get: operations["listDocuments"];
        put?: never;
        /** UploadDocument | permission=knowledge:manage | auth=required */
        post: operations["uploadDocument"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/knowledge-bases/{knowledge_base_id}/retention-policy": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetRetentionPolicy | permission=knowledge:manage | auth=required */
        get: operations["getRetentionPolicy"];
        /** UpdateRetentionPolicy | permission=knowledge:manage | auth=required */
        put: operations["updateRetentionPolicy"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/knowledge-bases/{knowledge_base_id}/team": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Set knowledge resource scope | permission=team:manage | auth=required */
        patch: operations["setKnowledgeTeam"];
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
    "/api/v1/runs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List authorized run metadata | permission=conversation:use/run:observe | auth=required */
        get: operations["listRuns"];
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
    "/api/v1/runs/{run_id}/approvals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List own run approvals | permission=conversation:use | auth=required */
        get: operations["listApprovals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/runs/{run_id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Cancel own run | permission=conversation:use | auth=required */
        post: operations["cancelRun"];
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
    "/api/v1/runs/{run_id}/inputs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Supply requested input | permission=conversation:use | auth=required */
        post: operations["submitInput"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/runs/{run_id}/retry": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Retry own failed run without prepared actions | permission=conversation:use | auth=required */
        post: operations["retryRun"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/runs/{run_id}/trace": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read authorized sanitized trace | permission=conversation:use/run:observe | auth=required */
        get: operations["getRunTrace"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/source-inventory": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetTenantSourceInventory | permission=knowledge:manage | auth=required | role=admin */
        get: operations["getTenantSourceInventory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/source-investigations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** ListSourceInvestigations | permission=knowledge:manage | auth=required | role=admin */
        get: operations["listSourceInvestigations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/source-investigations/{object_ref}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetSourceInvestigation | permission=knowledge:manage | auth=required | role=admin */
        get: operations["getSourceInvestigation"];
        /** SaveSourceInvestigation | permission=knowledge:manage | auth=required | role=admin */
        put: operations["saveSourceInvestigation"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/team-users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Tenant member directory | permission=team:manage | auth=required */
        get: operations["listTeamUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/teams": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List accessible teams | permission=authenticated | auth=required */
        get: operations["listTeams"];
        put?: never;
        /** Create team | permission=team:manage | auth=required */
        post: operations["createTeam"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/teams/{team_id}/members": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Team members | permission=team:manage | auth=required */
        get: operations["listTeamMembers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/teams/{team_id}/members/{user_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /** Update membership | permission=team:manage | auth=required */
        put: operations["updateTeamMembership"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tickets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List tenant tickets | permission=ticket:read | auth=required */
        get: operations["listTickets"];
        put?: never;
        /** Create manual ticket | permission=ticket:create | auth=required */
        post: operations["createTicket"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Get tenant ticket | permission=ticket:read | auth=required */
        get: operations["getTicket"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}/assignment": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Assign ticket | permission=ticket:manage | auth=required */
        patch: operations["assignTicket"];
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Ticket timeline | permission=ticket:read | auth=required */
        get: operations["listTicketEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}/notes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Add internal note | permission=ticket:note | auth=required */
        post: operations["addTicketNote"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Change ticket status | permission=ticket:manage | auth=required */
        patch: operations["changeTicketStatus"];
        trace?: never;
    };
    "/api/v1/tickets/{ticket_id}/team": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /** Set ticket resource scope | permission=team:manage | auth=required */
        patch: operations["setTicketTeam"];
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
    "/internal/v1/knowledge/versions/{version_id}/complete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** CompleteIndex | permission=- | auth=service */
        post: operations["completeIndex"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/knowledge/versions/{version_id}/source": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetIndexSource | permission=- | auth=service */
        get: operations["getIndexSource"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/actions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Prepare immutable action | permission=- | auth=run_capability */
        post: operations["prepareAction"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/actions/{action_id}/execute": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Execute approved action | permission=- | auth=run_capability */
        post: operations["executeAction"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/actions/{action_id}/receipt": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Read action receipt | permission=- | auth=run_capability */
        get: operations["getActionReceipt"];
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
    "/internal/v1/runs/{run_id}/knowledge-scope": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** GetKnowledgeScope | permission=- | auth=run_capability */
        get: operations["getKnowledgeScope"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/internal/v1/runs/{run_id}/knowledge/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** ValidateEvidence | permission=- | auth=run_capability */
        post: operations["validateEvidence"];
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
    requestBodies: {
        assignTicketBody: {
            content: {
                "application/json": {
                    assignee_user_id?: string;
                    expected_version: number;
                    status?: string;
                };
            };
        };
        archiveSourceMetadataBody: {
            content: {
                "application/json": {
                    confirm: boolean;
                    expected_source_epoch: number;
                };
            };
        };
        withdrawDocumentBody: {
            content: {
                "application/json": {
                    expected_version: number;
                };
            };
        };
    };
    headers: never;
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    listManagedRoles: {
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
                            extra_permissions: string[];
                            items: {
                                builtin_role: string;
                                enabled: boolean;
                                id: string;
                                name: string;
                                permissions: string[];
                                revision: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    createManagedRole: {
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
                    name: string;
                    permissions: string[];
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
                            builtin_role: string;
                            enabled: boolean;
                            id: string;
                            name: string;
                            permissions: string[];
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    updateManagedRole: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                role_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    enabled: boolean;
                    expected_revision: number;
                    name: string;
                    permissions: string[];
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
                            builtin_role: string;
                            enabled: boolean;
                            id: string;
                            name: string;
                            permissions: string[];
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listManagedUsers: {
        parameters: {
            query?: {
                limit?: number;
                cursor?: string;
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
                                account: string;
                                display_name: string;
                                enabled: boolean;
                                id: string;
                                permissions: string[];
                                revision: number;
                                role: string;
                                role_ids: string[];
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    createManagedUser: {
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
                    account: string;
                    display_name: string;
                    password: string;
                    role_ids: string[];
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
                            account: string;
                            display_name: string;
                            enabled: boolean;
                            id: string;
                            permissions: string[];
                            revision: number;
                            role: string;
                            role_ids: string[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    updateManagedUser: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                user_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    display_name: string;
                    enabled: boolean;
                    expected_revision: number;
                    role_ids: string[];
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
                            account: string;
                            display_name: string;
                            enabled: boolean;
                            id: string;
                            permissions: string[];
                            revision: number;
                            role: string;
                            role_ids: string[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    resetManagedPassword: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                user_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_revision: number;
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
                            account: string;
                            display_name: string;
                            enabled: boolean;
                            id: string;
                            permissions: string[];
                            revision: number;
                            role: string;
                            role_ids: string[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    decideApproval: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                approval_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    decision: string;
                    expected_version: number;
                    reason?: string;
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
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    listAuditEvents: {
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
                                actor_user_id: string;
                                created_at: string;
                                id: string;
                                object_id: string;
                                operation: string;
                                source: string;
                                summary: string;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
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
    getCitation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                evidence_id: string;
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
                            chunk_id: string;
                            excerpt: string;
                            extraction_method?: string;
                            id: string;
                            page_number: number;
                            title: string;
                            version_id: string;
                        };
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
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    activateDocument: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    confirm_layout?: boolean;
                    confirm_ocr?: boolean;
                    expected_version: number;
                    version_id: string;
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
                            active_version_id: string;
                            id: string;
                            knowledge_base_id: string;
                            status: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listVersionHistory: {
        parameters: {
            query?: {
                cursor?: string;
                limit?: number;
            };
            header?: never;
            path: {
                document_id: string;
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
                            document_version: number;
                            has_more: boolean;
                            items: {
                                archive_block_reason: string;
                                archived_at: string;
                                can_archive: boolean;
                                chunk_count: number;
                                cleanup_state: string;
                                content_hash: string;
                                created_at: string;
                                document_id: string;
                                error_code: string;
                                filename: string;
                                id: string;
                                index_status: string;
                                metadata_archived: boolean;
                                ocr_mode: string;
                                parser_version: string;
                                pdf_layout: string;
                                source_format: string;
                                source_size: number;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getRetentionPlan: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
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
                            document_id: string;
                            document_version: number;
                            items: {
                                cleanup_state: string;
                                created_at: string;
                                eligible: boolean;
                                filename: string;
                                reasons: string[];
                                source_size: number;
                                version_id: string;
                            }[];
                            keep_latest: number;
                            minimum_age_days: number;
                            policy_revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listDocumentVersions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
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
                                chunk_count: number;
                                cleanup_state: string;
                                content_hash: string;
                                document_id: string;
                                error_code: string;
                                filename: string;
                                id: string;
                                index_status: string;
                                ocr_mode: string;
                                ocr_page_count: number;
                                page_count: number;
                                pdf_layout: string;
                                source_format: string;
                                source_size: number;
                                source_storage: string;
                                text_page_count: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    uploadDocumentVersion: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    content?: string;
                    content_base64?: string;
                    filename: string;
                    ocr_mode?: string;
                    pdf_layout?: string;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    requestSourceArchive: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listArchiveCopies: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
                version_id: string;
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
                            enabled: boolean;
                            items: {
                                eligible: boolean;
                                id: string;
                                reasons: string[];
                                source_epoch: number;
                                source_size: number;
                                status: string;
                            }[];
                            source_epoch: number;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listSourceCopyHistory: {
        parameters: {
            query?: {
                cursor?: string;
                limit?: number;
            };
            header?: never;
            path: {
                document_id: string;
                version_id: string;
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
                                archive_block_reason: string;
                                archived_at: string;
                                can_archive: boolean;
                                created_at: string;
                                id: string;
                                metadata_archived: boolean;
                                retired_at: string;
                                source_epoch: number;
                                source_size: number;
                                status: string;
                            }[];
                            next_cursor: string;
                            source_epoch: number;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    archiveSourceMetadata: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
                object_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["archiveSourceMetadataBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            archived_at: string;
                            object_id: string;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    cleanupArchiveCopy: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
                object_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["archiveSourceMetadataBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            eligible: boolean;
                            id: string;
                            reasons: string[];
                            source_epoch: number;
                            source_size: number;
                            status: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    archiveVersionMetadata: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    confirm: boolean;
                    expected_document_version: number;
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
                            archived_at: string;
                            document_id: string;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    cleanupDocumentVersion: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    confirm: boolean;
                    expected_document_version: number;
                    expected_policy_revision?: number;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    previewVersionPages: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
                version_id: string;
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
                            pages: {
                                extraction_method?: string;
                                page_number: number;
                                text: string;
                            }[];
                            parser_version: string;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    restoreOriginalSource: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    downloadOriginalSource: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
                version_id: string;
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
                            content_base64: string;
                            content_hash: string;
                            filename: string;
                            source_format: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    adoptSourceObject: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                document_id: string;
                version_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    adoption_token: string;
                    confirm: boolean;
                    expected_source_epoch: number;
                    object_ref: string;
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
                            object_id: string;
                            object_ref: string;
                            source_epoch: number;
                            verified_at: string;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getSourceInventory: {
        parameters: {
            query?: {
                cursor?: string;
                limit?: number;
            };
            header?: never;
            path: {
                document_id: string;
                version_id: string;
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
                            adoption_enabled: boolean;
                            content_verified: boolean;
                            has_more: boolean;
                            items: {
                                adoption_eligible: boolean;
                                adoption_reason: string;
                                adoption_token: string;
                                key_kind: string;
                                modified_at: string;
                                object_ref: string;
                                reconciliation: string;
                                registered_id: string;
                                registered_status: string;
                                size: number;
                                source_epoch: number;
                            }[];
                            next_cursor: string;
                            observed_at: string;
                            source_epoch: number;
                            source_size: number;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    withdrawDocument: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                document_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["withdrawDocumentBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            active_version_id: string;
                            id: string;
                            knowledge_base_id: string;
                            status: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listKnowledgeBases: {
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
                            items: {
                                acl_revision: number;
                                id: string;
                                name: string;
                                read_roles: string[];
                                team_id: string;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    createKnowledgeBase: {
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
                    name: string;
                    read_roles: string[];
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
                            acl_revision: number;
                            id: string;
                            name: string;
                            read_roles: string[];
                            team_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    updateKnowledgeAcl: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                knowledge_base_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_revision: number;
                    read_roles: string[];
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
                            acl_revision: number;
                            id: string;
                            name: string;
                            read_roles: string[];
                            team_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listDocuments: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                knowledge_base_id: string;
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
                                active_version_id: string;
                                id: string;
                                knowledge_base_id: string;
                                status: string;
                                title: string;
                                version: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    uploadDocument: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                knowledge_base_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    content?: string;
                    content_base64?: string;
                    filename: string;
                    ocr_mode?: string;
                    pdf_layout?: string;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getRetentionPolicy: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                knowledge_base_id: string;
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
                            keep_latest: number;
                            knowledge_base_id: string;
                            minimum_age_days: number;
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    updateRetentionPolicy: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                knowledge_base_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_revision: number;
                    keep_latest: number;
                    minimum_age_days: number;
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
                            keep_latest: number;
                            knowledge_base_id: string;
                            minimum_age_days: number;
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    setKnowledgeTeam: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                knowledge_base_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_revision: number;
                    team_id: string;
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
                            acl_revision: number;
                            id: string;
                            name: string;
                            read_roles: string[];
                            team_id: string;
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
    listRuns: {
        parameters: {
            query: {
                /** @example own */
                scope: string;
                status?: string;
                run_id?: string;
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
                                actor_user_id: string;
                                can_retry: boolean;
                                conversation_id: string;
                                created_at: string;
                                error_code: string;
                                id: string;
                                retried_by_run_id: string;
                                retry_block_reason: string;
                                retry_depth: number;
                                retry_of_run_id: string;
                                status: string;
                                tool_call_count: number;
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
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    listApprovals: {
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
                            items: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    cancelRun: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["withdrawDocumentBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    submitInput: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    content: string;
                    expected_version: number;
                    input_id: string;
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
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    retryRun: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                run_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["withdrawDocumentBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            action_receipts: {
                                action_id: string;
                                business_object_id: string;
                                business_object_type: string;
                                executed_at: string;
                                result: {
                                    [key: string]: unknown;
                                };
                                tool_name: string;
                            }[];
                            approvals: {
                                action_id: string;
                                decision_reason: string;
                                display_snapshot: {
                                    [key: string]: unknown;
                                };
                                expires_at: string;
                                id: string;
                                payload_digest: string;
                                run_id: string;
                                status: string;
                                version: number;
                            }[];
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
                            pending_inputs: {
                                expires_at: string;
                                id: string;
                                question: string;
                                status: string;
                                version: number;
                            }[];
                            retry_depth: number;
                            retry_of_run_id: string;
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
    getRunTrace: {
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
                            observation: {
                                available: boolean;
                                models: {
                                    duration_known: boolean;
                                    duration_ms: number;
                                    error_code: string;
                                    input_tokens: number;
                                    output_tokens: number;
                                    status: string;
                                    tokens_known: boolean;
                                }[];
                                nodes: {
                                    completed_at: string;
                                    duration_ms: number;
                                    generation: number;
                                    node: string;
                                    status: string;
                                }[];
                                tools: {
                                    completed_count: number;
                                    tool: string;
                                }[];
                                truncated: boolean;
                                usage: {
                                    cost_status: string;
                                    embedding_attempts: number;
                                    embedding_input_tokens: number;
                                    embedding_tokens_known: boolean;
                                    input_tokens: number;
                                    model_attempts: number;
                                    model_tokens_known: boolean;
                                    output_tokens: number;
                                    retrieval_attempts: number;
                                };
                            };
                            run: {
                                actor_user_id: string;
                                can_retry: boolean;
                                conversation_id: string;
                                created_at: string;
                                error_code: string;
                                id: string;
                                retried_by_run_id: string;
                                retry_block_reason: string;
                                retry_depth: number;
                                retry_of_run_id: string;
                                status: string;
                                tool_call_count: number;
                                updated_at: string;
                                version: number;
                            };
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getTenantSourceInventory: {
        parameters: {
            query?: {
                cursor?: string;
                limit?: number;
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
                            content_verified: boolean;
                            has_more: boolean;
                            items: {
                                association: string;
                                document_id: string;
                                investigation_token: string;
                                key_kind: string;
                                modified_at: string;
                                object_ref: string;
                                reconciliation: string;
                                registered_id: string;
                                registered_status: string;
                                size: number;
                                source_epoch: number;
                                version_id: string;
                            }[];
                            next_cursor: string;
                            observed_at: string;
                            scanned_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listSourceInvestigations: {
        parameters: {
            query?: {
                status?: string;
                cursor?: string;
                limit?: number;
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
                            allowed_statuses: string[];
                            has_more: boolean;
                            items: {
                                created_at: string;
                                object_ref: string;
                                revision: number;
                                status: string;
                                updated_at: string;
                                updated_by: string;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getSourceInvestigation: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                object_ref: string;
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
                            allowed_statuses: string[];
                            note: string;
                            object_ref: string;
                            revision: number;
                            status: string;
                            updated_at: string;
                            updated_by: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    saveSourceInvestigation: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                object_ref: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    confirm: boolean;
                    expected_revision: number;
                    note: string;
                    selection_token?: string;
                    status: string;
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
                            allowed_statuses: string[];
                            note: string;
                            object_ref: string;
                            revision: number;
                            status: string;
                            updated_at: string;
                            updated_by: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listTeamUsers: {
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
                            items: {
                                display_name: string;
                                enabled: boolean;
                                id: string;
                                role: string;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listTeams: {
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
                            items: {
                                id: string;
                                name: string;
                                revision: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    createTeam: {
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
                    name: string;
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
                            id: string;
                            name: string;
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listTeamMembers: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                team_id: string;
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
                                display_name: string;
                                enabled: boolean;
                                id: string;
                                role: string;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    updateTeamMembership: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                team_id: string;
                user_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_revision: number;
                    member: boolean;
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
                            id: string;
                            name: string;
                            revision: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listTickets: {
        parameters: {
            query: {
                cursor?: string;
                /** @example 20 */
                limit: number;
                order_id?: string;
                status?: string;
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
                                assignee_user_id: string;
                                category: string;
                                created_at: string;
                                description: string;
                                id: string;
                                order_id: string;
                                priority: string;
                                status: string;
                                team_id: string;
                                ticket_no: string;
                                title: string;
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
    createTicket: {
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
                    category: string;
                    description: string;
                    expected_version: number;
                    order_id: string;
                    priority: string;
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
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getTicket: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                ticket_id: string;
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
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    assignTicket: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                ticket_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["assignTicketBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    listTicketEvents: {
        parameters: {
            query: {
                cursor?: string;
                /** @example 20 */
                limit: number;
            };
            header?: never;
            path: {
                ticket_id: string;
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
                                actor_user_id: string;
                                created_at: string;
                                id: string;
                                object_id: string;
                                operation: string;
                                source: string;
                                summary: string;
                            }[];
                            next_cursor: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    addTicketNote: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                ticket_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    content: string;
                    expected_version: number;
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
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    changeTicketStatus: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                ticket_id: string;
            };
            cookie?: never;
        };
        requestBody: components["requestBodies"]["assignTicketBody"];
        responses: {
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        code?: string;
                        data?: {
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    setTicketTeam: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path: {
                ticket_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    expected_version: number;
                    team_id: string;
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
                            assignee_user_id: string;
                            category: string;
                            created_at: string;
                            description: string;
                            id: string;
                            order_id: string;
                            priority: string;
                            status: string;
                            team_id: string;
                            ticket_no: string;
                            title: string;
                            version: number;
                        };
                        request_id?: string;
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
    completeIndex: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                version_id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    chunk_count: number;
                    content_hash: string;
                    error_code?: string;
                    job_id: string;
                    page_texts?: {
                        extraction_method?: string;
                        page_number: number;
                        text: string;
                    }[];
                    parser_version?: string;
                    status: string;
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
                            chunk_count: number;
                            cleanup_state: string;
                            content_hash: string;
                            document_id: string;
                            error_code: string;
                            filename: string;
                            id: string;
                            index_status: string;
                            ocr_mode: string;
                            ocr_page_count: number;
                            page_count: number;
                            pdf_layout: string;
                            source_format: string;
                            source_size: number;
                            source_storage: string;
                            text_page_count: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getIndexSource: {
        parameters: {
            query: {
                job_id: string;
            };
            header?: never;
            path: {
                version_id: string;
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
                            content: string;
                            content_hash: string;
                            document_id: string;
                            knowledge_base_id: string;
                            ocr_mode: string;
                            pdf_layout: string;
                            source_base64: string;
                            source_format: string;
                            tenant_id: string;
                            version_id: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    prepareAction: {
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
                    action_id: string;
                    arguments: {
                        [key: string]: unknown;
                    };
                    evidence_ids: string[];
                    tool_name: string;
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
                            action_id: string;
                            decision_reason: string;
                            display_snapshot: {
                                [key: string]: unknown;
                            };
                            expires_at: string;
                            id: string;
                            payload_digest: string;
                            run_id: string;
                            status: string;
                            version: number;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    executeAction: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                run_id: string;
                action_id: string;
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
                            action_id: string;
                            business_object_id: string;
                            business_object_type: string;
                            executed_at: string;
                            result: {
                                [key: string]: unknown;
                            };
                            tool_name: string;
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    getActionReceipt: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                run_id: string;
                action_id: string;
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
                            action_id: string;
                            business_object_id: string;
                            business_object_type: string;
                            executed_at: string;
                            result: {
                                [key: string]: unknown;
                            };
                            tool_name: string;
                        };
                        request_id?: string;
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
    getKnowledgeScope: {
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
                            tenant_id: string;
                            versions: {
                                acl_revision: number;
                                document_id: string;
                                knowledge_base_id: string;
                                title: string;
                                version_id: string;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
    validateEvidence: {
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
                    candidates: {
                        chunk_id: string;
                        content_hash: string;
                        excerpt: string;
                        page_number?: number;
                        version_id: string;
                    }[];
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
                            items: {
                                chunk_id: string;
                                excerpt: string;
                                extraction_method?: string;
                                id: string;
                                page_number: number;
                                title: string;
                                version_id: string;
                            }[];
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
                            resume_data: {
                                [key: string]: unknown;
                            };
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
                            tickets?: {
                                assignee_user_id: string;
                                category: string;
                                created_at: string;
                                description: string;
                                id: string;
                                order_id: string;
                                priority: string;
                                status: string;
                                team_id: string;
                                ticket_no: string;
                                title: string;
                                version: number;
                            }[];
                        };
                        request_id?: string;
                    };
                };
            };
        };
    };
}
