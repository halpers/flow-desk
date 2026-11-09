#!/usr/bin/env bash
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=business_password="$BUSINESS_PASSWORD" --set=agent_password="$AGENT_PASSWORD" \
  --set=agent_migration_password="$AGENT_MIGRATION_PASSWORD" <<'SQL'
CREATE ROLE fd_business LOGIN PASSWORD :'business_password';
CREATE ROLE fd_agent LOGIN PASSWORD :'agent_password';
CREATE ROLE fd_agent_migrator LOGIN PASSWORD :'agent_migration_password';
CREATE SCHEMA business;
CREATE SCHEMA agent AUTHORIZATION fd_agent_migrator;
CREATE SCHEMA knowledge AUTHORIZATION fd_agent_migrator;
CREATE EXTENSION vector WITH SCHEMA knowledge;
REVOKE CREATE,TEMP ON DATABASE flowdesk FROM PUBLIC,fd_agent;
REVOKE CREATE ON SCHEMA public FROM PUBLIC,fd_agent;
REVOKE ALL ON SCHEMA agent,knowledge FROM PUBLIC;
GRANT USAGE ON SCHEMA agent,knowledge TO fd_agent;
GRANT USAGE ON SCHEMA business TO fd_business;
ALTER DEFAULT PRIVILEGES IN SCHEMA business GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO fd_business;
ALTER DEFAULT PRIVILEGES IN SCHEMA business GRANT USAGE,SELECT ON SEQUENCES TO fd_business;
SQL
