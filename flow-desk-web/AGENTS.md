# Frontend instructions

- Read `../AGENTS.md`, `../docs/contracts.md` and `docs/design.md`.
- Use React, TypeScript and Vite; isolate pages/features from shared UI, API and authentication code.
- Generate API types from the Go API's generated OpenAPI. Validate SSE events against the versioned event contract. Do not maintain parallel handwritten copies of server enums.
- Call only the Go API through relative URLs. Keep access tokens in memory and refresh credentials in the agreed HttpOnly cookie flow.
- Use cancellable fetch streaming for SSE with bearer headers. Reconnect with the last processed event ID, deduplicate replayed events and recover snapshots when history expires.
- Render approval cards from the server's immutable action snapshot. Send approval IDs and decisions; do not submit edited tool arguments as an approval.
- Treat Markdown, document titles, citations and tool output as untrusted content. Disable raw HTML, constrain links and render attachment previews through authorized endpoints.
- Route visibility and disabled controls improve UX; backend authorization remains authoritative.
- Keep tests focused on meaningful behavior: event replay, cancellation races, confirmation conflicts, auth expiry and accessible navigation.
- After implementation exists, use the package scripts for type checking, linting, unit tests and builds; use Playwright for affected workflows. Current scaffolding does not yet contain these scripts.
