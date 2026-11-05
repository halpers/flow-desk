-- +goose Up
ALTER TABLE users ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0);
ALTER TABLE users ADD COLUMN auth_version BIGINT NOT NULL DEFAULT 1 CHECK(auth_version>0);
ALTER TABLE users ADD COLUMN extra_permissions JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(extra_permissions)='array');
CREATE TABLE roles (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL REFERENCES tenants(id),
 name TEXT NOT NULL, builtin_role TEXT NOT NULL DEFAULT '' CHECK(builtin_role IN ('','operator','supervisor','admin')),
 permissions JSONB NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(permissions)='array'),
 enabled BOOLEAN NOT NULL DEFAULT true, revision BIGINT NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(tenant_id,id)
);
CREATE UNIQUE INDEX roles_name_unique ON roles(tenant_id,lower(name));
CREATE UNIQUE INDEX roles_builtin_unique ON roles(tenant_id,builtin_role) WHERE builtin_role<>'';
CREATE TABLE user_roles (
 id UUID PRIMARY KEY, tenant_id UUID NOT NULL, user_id UUID NOT NULL, role_id UUID NOT NULL,
 FOREIGN KEY(tenant_id,user_id) REFERENCES users(tenant_id,id),
 FOREIGN KEY(tenant_id,role_id) REFERENCES roles(tenant_id,id), UNIQUE(tenant_id,user_id,role_id)
);
CREATE INDEX user_roles_role_idx ON user_roles(tenant_id,role_id,user_id);
-- +goose StatementBegin
CREATE FUNCTION create_builtin_roles() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO roles(id,tenant_id,name,builtin_role)
 SELECT md5(NEW.id::text||':role:'||value)::uuid,NEW.id,value,value
 FROM unnest(ARRAY['operator','supervisor','admin']) value;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER tenant_builtin_roles AFTER INSERT ON tenants FOR EACH ROW EXECUTE FUNCTION create_builtin_roles();
INSERT INTO roles(id,tenant_id,name,builtin_role)
 SELECT md5(t.id::text||':role:'||value)::uuid,t.id,value,value
 FROM tenants t CROSS JOIN unnest(ARRAY['operator','supervisor','admin']) value;
-- +goose StatementBegin
CREATE FUNCTION link_initial_role() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO user_roles(id,tenant_id,user_id,role_id)
 SELECT md5(NEW.id::text||':initial-role')::uuid,NEW.tenant_id,NEW.id,r.id
 FROM roles r WHERE r.tenant_id=NEW.tenant_id AND r.builtin_role=NEW.role;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER user_initial_role AFTER INSERT ON users FOR EACH ROW EXECUTE FUNCTION link_initial_role();
INSERT INTO user_roles(id,tenant_id,user_id,role_id)
 SELECT md5(u.id::text||':initial-role')::uuid,u.tenant_id,u.id,r.id
 FROM users u JOIN roles r ON r.tenant_id=u.tenant_id AND r.builtin_role=u.role;
-- +goose Down
-- Preserve identity history, assigned permissions and revocation generations.
SELECT 1;
