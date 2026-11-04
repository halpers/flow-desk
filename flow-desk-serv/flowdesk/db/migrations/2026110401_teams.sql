-- +goose Up
CREATE TABLE teams (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 80), revision BIGINT NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(tenant_id,id), UNIQUE(tenant_id,name)
);
CREATE TABLE team_members (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, team_id UUID NOT NULL, user_id UUID NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(tenant_id,team_id,user_id),
 FOREIGN KEY(tenant_id,team_id) REFERENCES teams(tenant_id,id),
 FOREIGN KEY(tenant_id,user_id) REFERENCES users(tenant_id,id)
);
CREATE INDEX team_members_user_idx ON team_members(tenant_id,user_id,team_id);
ALTER TABLE tickets ADD COLUMN team_id UUID;
ALTER TABLE tickets ADD FOREIGN KEY(tenant_id,team_id) REFERENCES teams(tenant_id,id);
ALTER TABLE knowledge_bases ADD COLUMN team_id UUID;
ALTER TABLE knowledge_bases ADD FOREIGN KEY(tenant_id,team_id) REFERENCES teams(tenant_id,id);
CREATE TABLE run_ticket_resources (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, run_id UUID NOT NULL, ticket_id UUID NOT NULL,
 UNIQUE(tenant_id,run_id,ticket_id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id),
 FOREIGN KEY(tenant_id,ticket_id) REFERENCES tickets(tenant_id,id)
);

-- +goose Down
DROP TABLE run_ticket_resources;
ALTER TABLE knowledge_bases DROP COLUMN team_id;
ALTER TABLE tickets DROP COLUMN team_id;
DROP TABLE team_members;
DROP TABLE teams;
