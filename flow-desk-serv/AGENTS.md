# Go service instructions

## Layout and layering

These conventions adapt `C:/work/go/src/rwa/rwa-tokenization-serv/AGENTS.md` for FlowDesk. Read `../AGENTS.md` as well.

The Go module lives in `flowdesk/`, with module import prefix `flowdesk/...`. It was bootstrapped by running `goctl api new flowdesk` from this service directory. The current baseline is Go 1.27.1, goctl 1.10.2 and go-zero 1.10.3. Run subsequent Go/goctl commands from `flowdesk/`.

- `flowdesk.api` imports API fragments from `api/`; generated example routes were replaced in M1.
- `internal/handler/`: HTTP translation and route registration.
- `internal/logic/`: use cases, authorization decisions and transaction ownership.
- `internal/svc/`: dependency initialization; exposes table-model and client interfaces.
- `internal/config/`: configuration structures.
- `internal/middleware/`: identity, trusted tenant context, service capabilities and tracing.
- `internal/client/`: Python execution client and logistics adapter.
- `internal/worker/`: scheduler loop and lease maintenance; use case implementations stay in logic packages.
- `internal/types/`: generated request/response types.
- `db/model/`: generated table models and custom per-table SQL.
- `db/migrations/`: authoritative goose migration history.
- `etc/`: environment-backed YAML configuration.

Define or change HTTP routes in `.api` before generation. Handlers must not contain business SQL or cross-table orchestration. ServiceContext initializes dependencies, never business workflows, and must not import logic.

## Names and SQL ownership

- Use meaningful business names and named state/limit constants. Do not shadow Go predeclared names such as `max`, `min`, `copy`, `len`, `error` or `any`.
- Use standard Go formatting/imports. With a compatible pinned Go version, use `slices.Contains`/`slices.Index` and integer range loops where appropriate.
- Initialize every table model in ServiceContext. Logic and workers use those initialized interfaces rather than constructing models.
- Each custom table model owns that table's queries, writes, locks and conditional updates. Keep SQL in `db/model/`.
- Use `Insert...`, `Update...`, `Delete...`; soft deletion uses `UpdateSoftDeletion...`. Use `Insert...IfAbsent...` for insert-on-conflict-do-nothing and `Upsert...` only for insert-or-update.
- Use `FindOne...` for key lookups, `Find<Business>...` for one-record/aggregate queries, `List...` for collections and `Count...`/`Exists...` for counts/booleans.
- Locking reads include `ForUpdate`/`ForShare` before `Tx`; lock-only operations may use `Lock...Tx`.
- Keep table-specific custom interfaces, methods and query projections in `db/model/<table>model.go`. Preserve generated `<table>model_gen.go` separately. Place JOIN/EXISTS/aggregate queries in the main table's model.
- Do not introduce an aggregate Store/Repository wrapping multiple table models. Business coordination belongs in the owning logic package.

## Transactions and consistency

- Logic owns cross-table workflows and opens transactions with `svcCtx.Conn.TransactCtx`.
- Every handwritten transaction-aware method has shape `XxxTx(ctx context.Context, session sqlx.Session, ...)`. Standalone methods have no `Tx` suffix and use their own connection.
- Pass the same non-nil transaction session to locking reads, writes, audit and outbox operations. A Tx method must not fall back to its own connection, create a nested transaction or accept arbitrary caller SQL.
- Expose separate standalone/Tx methods when both are required. Keep generated `withSession` helpers private to the model package.
- Preserve expected versions, lock order, lease-generation fencing and affected-row checks. External HTTP/model calls happen outside database transactions.
- Every tenant-owned query includes trusted tenant context and object authorization. Approval and write operations revalidate the current actor and resource permissions.
- Tool mutations atomically record business changes, action receipt, audit and event/outbox. Retrying an action returns the prior receipt rather than repeating the mutation.

## API and model generation

Initial scaffolding must use `goctl api new flowdesk` from `flow-desk-serv/` (already executed). Do not rerun initialization over implemented application code. For subsequent API changes, use the default goctl template from `flowdesk/`:

```sh
goctl api validate --api flowdesk.api
goctl api go -api flowdesk.api -dir .
```

Inspect generation diffs. Preserve generated headers and keep business behavior in logic. API annotations use stable unique handlers and explicit auth/permission metadata as described in the service design.

The migration/model workflow uses `db/goctl-model.sh` (portable Python helper), `db/goose-up.sh` and `db/model-tables.conf`. Verify generator type mappings whenever adding PostgreSQL types.

- Add ordered goose migrations, maintain the table configuration and generate models through `db/goctl-model.sh`.
- The generator creates an isolated temporary PostgreSQL schema, applies migrations, generates models, removes schema qualifiers as configured and cleans up. Never reset an existing schema to generate models.
- Generated structs/base CRUD and `*_gen.go` are generator-owned. Extend custom models and reuse generated types rather than writing duplicate row types.
- Keep exact decimal values as PostgreSQL NUMERIC and Go string/decimal representations; review `NUMERIC` and `BYTEA` type mappings against the pinned generator version.
- Review generated partial/expression-index finders. Override unsafe lookups in custom models without patching generated files.
- Migration names use date plus two-digit sequence, such as `2026110201_create_identity.sql`. Applied shared migrations are immutable; add new ones for subsequent changes.
- PostgreSQL is the target database. Initialization creates roles/databases only; migrations own schema history. Go migrations must not alter Python-owned checkpoint/vector tables.

## Validation

For the current scaffold and subsequent changes, format changed files and run from `flowdesk/`:

```sh
go test ./...
go vet ./...
```

In restricted Windows environments use a repository-local GOCACHE and keep it ignored. Use focused table-driven tests; test transaction rollback with a later-step failure. Database tests use isolated temporary development schemas. Serialize packages if advisory locks could interfere across schemas. Report unavailable external dependencies and still perform independent checks.

Do not edit `.idea/`, reference repositories, production databases or unrelated working changes.
