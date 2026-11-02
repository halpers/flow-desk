# Python Agent instructions

- Read `../AGENTS.md`, `docs/design.md`, `../docs/contracts.md` and `../docs/data-model.md`.
- Use a src layout under `src/flowdesk_agent/`. Separate HTTP/Pydantic schemas, graph nodes, tool adapters, retrieval, storage and observability.
- Graph nodes use validated structured state. Trusted tenant/actor/run/attempt context is injected by the service and never overwritten by model arguments.
- Business mutations must call the Go tools API. The Python database account must not have business-schema write privileges.
- Persist checkpoints and effect identifiers before resumable side effects. Resumed nodes may execute again; preserve action_id and enforce Go-side idempotency.
- Each run uses its own stable graph thread ID. Conversation history is a separate Go-owned resource and must not allow concurrent graph writes into one thread.
- Enforce one executor per run, durable command deduplication, cancellation, deadlines and retry budgets. In-memory FastAPI background tasks alone are not a durable queue.
- Await persistence of business-visible events before acknowledging them. Events and pending callbacks must survive restart; redact tool results and model inputs.
- RAG must enforce tenant and knowledge-base access filters before ranking, and Go revalidates cited versions before any policy-backed mutation.
- Keep document parsers constrained by file types, size and page limits. Use parameterized queries and server-selected object paths.
- Use synthetic evaluation data with fixed task IDs and deterministic expected assertions. Distinguish mock CI from real-model quality/cost evaluation.
- After M1 creates project configuration, pin a supported Python/framework combination and lock dependencies. Planned checks: Ruff formatting/linting, type checking, Pytest and dataset validation; do not present nonexistent scripts as runnable.
