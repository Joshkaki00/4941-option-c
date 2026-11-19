# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Ask the Handbook

Internal RAG assistant for a ~300-person company. Employees ask handbook questions; answers are grounded in ~500 pages of policies with citations.

**Spec:** `spec.md` is the testable contract (ACs + quality gates). `proposal.md` (Draft v3) is the design reference for architecture, data model, API, concurrency, and tradeoffs.

**Status:** pre-code. No Go module, Makefile, or cmd/internal packages yet. Commands below are the spec's targets and will fail until those exist.

## Commands (from spec.md quality gates)

- Unit + race (every code change): `go test ./...` and `go test -race ./...`
- Single test: `go test ./internal/retrieval -run AudienceFilter -v` (the privacy gate)
- Golden evals (chunking/prompt/model/ingest changes): `make eval` or `go run ./cmd/eval -set testdata/golden/v1.json`. Must pass recall@5 ≥ 0.8 and correct-decline ≥ 0.95.
- Privacy gate: `make test-privacy`
- Ask-path smoke: `./scripts/smoke-ask.sh`

## Architecture (read proposal.md §3, §6, §7, §13 before changing retrieval or concurrency)

- **Online ask path** is synchronous and streamed over SSE: SSO identity → audiences → query rewrite → hybrid retrieval (vector + full-text, RRF) filtered by audience → decline without calling the LLM if retrieval is weak → grounded prompt → cited answer.
- **Access control is enforced in retrieval SQL, default-deny.** A document with no `document_audiences` rows never appears in search. The prompt is never a security boundary.
- **Offline ingest** goes through `ingestion_jobs` (`FOR UPDATE SKIP LOCKED` / River). New versions create new chunks, then flip `is_current` in one transaction. Old chunks are kept for audit but excluded from search.
- **Concurrency:** goroutines live inside a request only for independent I/O, derive from `r.Context()`, and are joined before return. Every goroutine must be bounded (`errgroup`, semaphore) and have an owner. Async is limited to ingestion, scheduled jobs, Slack acks, and best-effort logs.
- **Fallback chain:** hosted Anthropic API → local Ollama/MLX (limited concurrency) → retrieval-only.

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
- **Hard hook:** always **Read** a file before **Edit/Write** on it (enforced in `.claude/hooks/` and `.cursor/hooks/`). New-file creates are allowed.
- Tests first for new behavior; verify boundaries (empty, null, auth deny, disconnect).
- After each phase: tests + race/leak checks before proceeding.
- Shared agent config lives in `.claude/` and is committed. Personal prefs stay in `~/.claude/`.
- Keep this file short. Put procedures in `.claude/skills/`, not here. Point to paths instead of pasting code.

## Key paths (when they exist)

- `spec.md` — acceptance criteria and quality gates
- `proposal.md` — full design
- `docs/best-practices.md` — AI-assisted development practices for this repo
- `docs/claude-use.md` — Claude Code + Claude API usage (CLI, streaming, Context7 ids)
- `docs/failure-log.md` — recurring agent/env failure patterns
- `.claude/skills/` — repeatable workflows (`/review`, `/verify`, `/handoff`)
- `.claude/settings.json` — shared permissions and hooks

## Verify before commit

1. Behavior matches the relevant section of `proposal.md`
2. Tests pass (`go test ./...` once the module exists; include `-race` for concurrency)
3. No secrets in commits; identity always from SSO token, never from client-supplied user ids
4. Retrieval paths stay audience-filtered
