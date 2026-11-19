# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# Ask the Handbook

Internal RAG assistant for a ~300-person company. Employees ask handbook questions; answers are grounded in ~500 pages of policies with citations.

**Spec:** `spec.md` is the testable contract (ACs + quality gates). `proposal.md` (Draft v3) is the design reference for architecture, data model, API, concurrency, and tradeoffs.

**Status:** v1 built: ingest queue with background worker, audience-filtered hybrid search, grounded cited answers over SSE, declines, feedback, retrieval-only tier, admin routes and signed webhook, Slack bot (`internal/slack`), and a streaming Yew UI (`web/`). Not verified live: Slack in a real workspace, the UI in a browser, SSO against a real IdP. Not built: local Ollama generator tier.

## Layout

- `cmd/server` (API), `cmd/ingest` (corpus → `ingestion_jobs` → versions), `cmd/eval` (golden gates)
- `internal/`: `store` (schema in `migrations/`, publish/jobs, admin queries), `chunk`, `embed`, `retrieval` (privacy SQL, RRF), `answer` (policy checks, outcome logic), `llm`, `auth`, `httpapi`, `admin` (`/v1/admin`, signed webhook), `slack` (Socket Mode listener, worker pool), `ingest`, `eval`, `config`, `app` (wiring)
- `web/`: Yew (Rust → WASM) browser UI. `model.rs` holds the API types and pure helpers (host-testable); `main.rs` is the Yew app; `Trunk.toml` proxies `/v1` to the Go API.
- `testdata/handbook/` is a fictional sample corpus; `testdata/golden/v1.json` is the seed eval set

## Commands

- Unit + race (every code change): `make test` / `make race` (`go test ./...`, `go test -race ./...`)
- Single test: `go test ./internal/retrieval -run AudienceFilter -v` (the privacy gate)
- Privacy gate (fails, not skips, without a DB): `make test-privacy`
- Golden evals (chunking/prompt/model/ingest changes): `make eval` (needs `ANTHROPIC_API_KEY`) or `make eval-retrieval` (recall only). Gates: recall@5 ≥ 0.8, correct-decline ≥ 0.95.
- Env / Anthropic key: `make env` (copies `.env.example` → `.env`), then set `ANTHROPIC_API_KEY=…` in `.env`. Loaded automatically by `cmd/server|ingest|eval` and by Make. Never commit `.env`.
- Ingest the sample corpus: `make ingest`. With LLM: `make run`. Retrieval-only local: `make run-dev`. Gate 4 smoke: `make smoke`.
- Web UI: `make web-test` (host unit tests), `make web-build` (WASM bundle), `make web` (trunk dev server on :8081; start the API first, it proxies `/v1` to :8080).
- DB tests need `TEST_DATABASE_URL` (the Makefile sets `postgres:///handbook_test`) with pgvector installed. `DATABASE_URL` defaults to `postgres:///handbook`.
- Before committing: run `/verify` (checklist and gate details in `spec.md`).

## Architecture (read proposal.md §3, §6, §7, §13 before changing retrieval or concurrency)

- **Stack:** Go (`net/http`), one binary; Postgres + pgvector; Anthropic API primary LLM, local Ollama/MLX fallback, retrieval-only last resort; Slack bot first; browser UI in Yew (Rust/WASM, `web/`), a static client that calls `/v1` and holds no logic the server doesn't enforce; SSO (OIDC); durable `ingestion_jobs` (SKIP LOCKED / River) for offline ingest only.
- **Online ask path** is synchronous and streamed over SSE: SSO identity → audiences → query rewrite → hybrid retrieval (vector + full-text, RRF) filtered by audience → decline without calling the LLM if retrieval is weak → grounded prompt → cited answer.
- **Offline ingest** goes through `ingestion_jobs`. New versions create new chunks, then flip `is_current` in one transaction. Old chunks are kept for audit but excluded from search.
- **Fallback chain:** hosted Anthropic API → local Ollama/MLX (limited concurrency) → retrieval-only.

## Non-negotiables

- **Access control is enforced in retrieval SQL, default-deny.** A document with no `document_audiences` rows never appears in search. The prompt is never a security boundary. Retrieval paths stay audience-filtered.
- Identity always comes from the SSO token, never from client-supplied user ids.
- Cite document, section, and last-updated on every answer; decline when sources are weak.
- Synchronous ask path with SSE. Async only for ingestion, scheduled jobs, Slack acks, best-effort logs.
- Bound every goroutine (`errgroup`, semaphores, request context). Goroutines derive from `r.Context()`, are joined before return, and have an owner. No unbounded `go func()`.
- Do **not** add: LangChain-style frameworks, a dedicated vector DB, Kafka, Kubernetes, or microservices in v1.
- Do **not** take actions (file leave, tickets) or answer personal HRIS questions in v1.

## Working agreements

- Spec before code. Prefer Given/When/Then and testable requirements.
- Work on `main` only. No feature branches, no PRs. Small commits; push main when asked.
- **Hard hook:** always **Read** a file before **Edit/Write** on it (enforced by `.claude/hooks/require-read-before-write.sh` and `.cursor/hooks/`). New-file creates are allowed. The hooks need `jq`. The read record lives in `${TMPDIR:-/tmp}/claude-read-before-write/` and is keyed per session and agent, so a fresh session must re-read.
- After each phase: tests + race/leak checks before proceeding.
- `.cursor/rules/ask-the-handbook.mdc` mirrors this file. `CLAUDE.md` is the source of truth; keep the `.mdc` in sync.

## Key paths (when they exist)

- `spec.md` — acceptance criteria and quality gates
- `proposal.md` — full design
- `docs/best-practices.md` — AI-assisted development practices for this repo
- `docs/claude-use.md` — Claude Code + Claude API usage (CLI, streaming, Context7 ids)
- `docs/failure-log.md` — recurring agent/env failure patterns
- `.claude/skills/` — repeatable workflows (`/review`, `/verify`, `/handoff`, `/claude-use`)
- `.claude/settings.json` — shared permissions and hooks
