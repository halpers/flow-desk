# AGENTS.md

## Project overview

FlowDesk is an enterprise ticket-processing Agent application with React, go-zero and FastAPI services. M1 provides engineering/identity; M2 provides read-only tasks, durable graph execution and SSE. See docs/progress.md for delivery evidence. PostgreSQL is the user-selected database. Design documents describe the full intended system, not a claim that every feature exists.

- `flow-desk-web/`: React and TypeScript frontend. The browser calls the Go service only.
- `flow-desk-serv/flowdesk/`: Go module location for the go-zero business API and scheduler.
- `flow-desk-agent/`: Python Agent API, graph executor, retrieval, checkpoints and evaluation.
- `docs/`: authoritative cross-service design, API behavior, data ownership and delivery criteria.
- `shared/contracts/`: generated API artifacts and authoritative event/tool schemas; source ownership is defined in its README.
- `deploy/`: deployment documentation and future runtime configuration.

Read the narrower service's AGENTS.md before changing that service. This file adapts conventions from `C:/work/go/src/rwa/rwa-tokenization-serv/AGENTS.md`; that reference is read-only unless the user's task explicitly includes it.

## Working conventions

- Keep the Go API, Python API, frontend and design contracts consistent when changing a cross-service feature.
- Go owns identity, tenant and object authorization, business data, approvals, business transactions and public run status. Python owns graph state, checkpoints and retrieval indexes. Do not write another service's database tables directly.
- Every tenant-scoped read and mutation must use trusted caller context. Never trust tenant, actor, permissions or approval decisions produced by a model.
- Business mutations go through Go tools and must enforce action-bound approval, current authorization and idempotency in Go.
- Treat model output, retrieved documents and tool descriptions as untrusted input. Validate structured output and constrain tool names and arguments.
- Use meaningful names, explicit state constants and named business/configuration limits. Do not shadow language built-ins in handwritten code.
- Store environment-specific configuration outside source code. Never commit passwords, model keys, tokens, customer data or production connection strings. Keep examples synthetic and logs redacted.
- Propagate request/run/trace IDs across HTTP and background work. Record visible progress summaries and tool results; do not persist private model reasoning.
- Preserve unrelated user files. Do not edit reference repositories, editor state or generated artifacts outside the requested scope.

## Contract and generation discipline

- Design behavior is documented in `docs/contracts.md`; implementation must add its machine-readable contracts before generating clients.
- Bootstrap the Go application with `goctl api new flowdesk` from `flow-desk-serv/`; do not handwrite a substitute framework scaffold. The initial generation has been completed. For subsequent API changes, edit `.api` and regenerate in the existing module instead of rerunning initialization over application code.
- Go HTTP route/type sources are `flow-desk-serv/flowdesk/flowdesk.api` and `api/*.api`. Python HTTP schemas come from FastAPI/Pydantic. Run scripts/export-contracts.ps1 after source changes. Add versioned event/tool schemas in shared/contracts as those features are implemented.
- Generated OpenAPI, TypeScript types, Go/Python client artifacts and `*_gen.go` files must retain generated headers. Modify generators or source definitions instead of editing outputs.
- Database schema changes require ordered migrations and isolated-schema model generation. See the Go service instructions for table-model and transaction rules.
- Do not mark a planned command as working until its required source files and scripts exist and the command has been executed successfully.

## Validation and delivery

- Documentation changes: verify local Markdown links, fenced code blocks, JSON examples and cross-document consistency. Report the actual validation performed.
- Application changes: run the affected service's formatting, static checks and meaningful tests. Use integration tests for transactions, permission boundaries, replay, approval and recovery.
- Tests must use disposable databases or isolated schemas, never reset an existing business schema.
- Future dependency baselines must be pinned in lockfiles and verified together. Do not claim a version is latest based on the reference project.
- Before finishing, inspect changed files and Git status/diff where available. If this directory is not a Git repository, use a file inventory and content checks instead.
- Report implemented behavior separately from proposals, targets and unexecuted commands.
