-- +goose Up
CREATE TABLE knowledge_bases (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id),
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 120),
 read_roles JSONB NOT NULL DEFAULT '["operator","supervisor","admin"]',
 acl_revision BIGINT NOT NULL DEFAULT 1 CHECK(acl_revision>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(tenant_id,id)
);
CREATE TABLE documents (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, knowledge_base_id UUID NOT NULL,
 title TEXT NOT NULL CHECK(length(title) BETWEEN 1 AND 120),
 active_version_id UUID, version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
 status TEXT NOT NULL DEFAULT 'draft' CHECK(status IN ('draft','active','withdrawn')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), FOREIGN KEY(tenant_id,knowledge_base_id) REFERENCES knowledge_bases(tenant_id,id)
);
CREATE TABLE document_versions (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, document_id UUID NOT NULL,
 job_id UUID NOT NULL UNIQUE, filename TEXT NOT NULL, content TEXT NOT NULL CHECK(length(content) BETWEEN 1 AND 20000),
 content_hash TEXT NOT NULL, index_status TEXT NOT NULL DEFAULT 'pending' CHECK(index_status IN ('pending','ready','failed')),
 chunk_count BIGINT NOT NULL DEFAULT 0 CHECK(chunk_count BETWEEN 0 AND 1000),
 error_code TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,id), UNIQUE(tenant_id,document_id,id),
 FOREIGN KEY(tenant_id,document_id) REFERENCES documents(tenant_id,id)
);
ALTER TABLE documents ADD CONSTRAINT document_active_version_fk FOREIGN KEY(tenant_id,id,active_version_id)
 REFERENCES document_versions(tenant_id,document_id,id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE run_evidence (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, run_id UUID NOT NULL, version_id UUID NOT NULL,
 chunk_id UUID NOT NULL, title TEXT NOT NULL, excerpt TEXT NOT NULL CHECK(length(excerpt) BETWEEN 1 AND 1800),
 content_hash TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,run_id,chunk_id),
 FOREIGN KEY(tenant_id,run_id) REFERENCES runs(tenant_id,id),
 FOREIGN KEY(tenant_id,version_id) REFERENCES document_versions(tenant_id,id)
);
CREATE INDEX document_base_idx ON documents(tenant_id,knowledge_base_id,created_at,id);
CREATE INDEX document_version_idx ON document_versions(tenant_id,document_id,created_at,id);
CREATE INDEX run_evidence_run_idx ON run_evidence(tenant_id,run_id);
-- +goose Down
DROP TABLE run_evidence;
ALTER TABLE documents DROP CONSTRAINT document_active_version_fk;
DROP TABLE document_versions;
DROP TABLE documents;
DROP TABLE knowledge_bases;
