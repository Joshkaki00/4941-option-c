# Ask the Handbook

Internal RAG assistant for a ~300-person company. Employees ask handbook questions; answers are grounded in ~500 pages of policies with citations.

**Spec:** see `proposal.md` (Draft v2). That document is the source of truth for architecture, data model, API, concurrency, and tradeoffs.

## Stack (v1)

- Go (`net/http`), one binary
- Postgres + pgvector (hybrid search: vector + full-text, RRF merge)
- Anthropic API primary LLM; local Ollama/MLX fallback; retrieval-only last resort
- Slack bot first; SSO (OIDC)
- Durable `ingestion_jobs` (SKIP LOCKED / River) for offline ingest only

## Non-negotiables

- Access control at **retrieval** (default-deny). The prompt is not a security boundary.
- Cite document, section, and last-updated on every answer; decline when sources are weak.
- Synchronous ask path with SSE; async only for ingestion, scheduled jobs, Slack acks, best-effort logs.
- Bound every goroutine (`errgroup`, semaphores, request context). No unbounded `go func()`.
- Do **not** add: LangChain-style frameworks, a dedicated vector DB, Kafka, Kubernetes, or microservices in v1.
- Do **not** take actions (file leave, tickets) or answer personal HRIS questions in v1.

## Working agreements

- Spec before code. Prefer Given/When/Then and testable requirements.
- Work on `main` only. No feature branches, no PRs. Small commits; push main when asked.
- Tests first for new behavior; verify boundaries (empty, null, auth deny, disconnect).
- After each phase: tests + race/leak checks before proceeding.
- Shared agent config lives in `.claude/` and is committed. Personal prefs stay in `~/.claude/`.
- Keep this file short. Put procedures in `.claude/skills/`, not here. Point to paths instead of pasting code.

## Key paths (when they exist)

- `proposal.md` — full design
- `docs/best-practices.md` — AI-assisted development practices for this repo
- `docs/failure-log.md` — recurring agent/env failure patterns
- `.claude/skills/` — repeatable workflows (`/review`, `/verify`, `/handoff`)
- `.claude/settings.json` — shared permissions and hooks

## Verify before commit

1. Behavior matches the relevant section of `proposal.md`
2. Tests pass (`go test ./...` once the module exists; include `-race` for concurrency)
3. No secrets in commits; identity always from SSO token, never from client-supplied user ids
4. Retrieval paths stay audience-filtered
