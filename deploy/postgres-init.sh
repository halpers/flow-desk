#!/usr/bin/env bash
set -euo pipefail
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  --set=business_password="$BUSINESS_PASSWORD" --set=agent_password="$AGENT_PASSWORD" <<'SQL'
CREATE ROLE fd_business LOGIN PASSWORD :'business_password';
CREATE ROLE fd_agent LOGIN PASSWORD :'agent_password';
CREATE SCHEMA business;
CREATE SCHEMA agent AUTHORIZATION fd_agent;
CREATE SCHEMA knowledge AUTHORIZATION fd_agent;
CREATE EXTENSION vector WITH SCHEMA knowledge;
GRANT USAGE ON SCHEMA business TO fd_business;
ALTER DEFAULT PRIVILEGES IN SCHEMA business GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO fd_business;
ALTER DEFAULT PRIVILEGES IN SCHEMA business GRANT USAGE,SELECT ON SEQUENCES TO fd_business;
SQL
