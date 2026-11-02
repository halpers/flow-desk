#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
: "${GOOSE_DB_URL:?Set GOOSE_DB_URL}"
export PGOPTIONS='-c search_path=business'
exec goose -dir migrations postgres "$GOOSE_DB_URL" "${@:-status}"
