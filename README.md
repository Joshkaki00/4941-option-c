# Ask the Handbook

**AI use:** I used Claude Code only to debug, build, make, and run tests; the design decisions are mine.

An internal assistant that answers employee questions from the company handbook. Every answer cites its source passages. When the handbook doesn't cover a question, the assistant declines instead of guessing.

- **Grounded:** answers come only from retrieved handbook passages, and each claim cites one.
- **Access-controlled:** search is filtered by the caller's audiences in SQL. A document with no audience rows is never returned.
- **Declines by design:** weak evidence, legal, medical, or disciplinary topics, and personal balance questions never reach the model.
- **Degrades gracefully:** with no LLM configured, it returns the matching passages and routes the user to a human.

## Quick start (local)

Requirements: Go 1.27.4, PostgreSQL 14+ with the `pgvector` extension, and optionally [Ollama](https://ollama.com) with `nomic-embed-text`.

```sh
make env                 # create .env from .env.example
make ingest              # load testdata/handbook into Postgres
make run-dev             # retrieval-only server on :8080, no LLM key needed
make smoke               # end-to-end checks against the running server
make web                 # Yew UI on http://localhost:8081 (proxies /v1 to :8080)
```

The web UI needs `trunk` and the `wasm32-unknown-unknown` target: `cargo install trunk --version 0.22.2 --locked` and `rustup target add wasm32-unknown-unknown`.

Local defaults use the Unix socket and your OS user: `DATABASE_URL=postgres:///handbook` and `TEST_DATABASE_URL=postgres:///handbook_test`. Override them in `.env`.

With an Anthropic key, set `ANTHROPIC_API_KEY` in `.env` and run `make run` instead of `make run-dev`.

## Configuration

Settings come from environment variables. The Go binaries load `.env` without overwriting variables already set in the shell. `make` targets include `.env` and export its values, so they override the shell. See [`.env.example`](.env.example) for the full list.

| Variable | Purpose |
|---|---|
| `LLM_PROVIDER` | `anthropic` or `none` (retrieval-only) |
| `ANTHROPIC_API_KEY`, `ANTHROPIC_MODEL` | Required when `LLM_PROVIDER=anthropic` |
| `EMBED_PROVIDER` | `ollama` for real retrieval, `hash` for offline tests |
| `EMBED_DIM` | Must be `768` to match the `vector(768)` column |
| `DATABASE_URL` | Postgres connection string |
| `OIDC_ISSUER`, `OIDC_CLIENT_ID` | Production SSO |
| `DEV_AUTH_TOKENS` | Local-only static tokens: `token\|subject\|group1,group2;...` |
| `MIN_SIMILARITY`, `TOP_K`, `RETRIEVE_TIMEOUT` | Retrieval tuning |

The server refuses to start without either `OIDC_ISSUER` or `DEV_AUTH_TOKENS`. Identity always comes from the token, never from request bodies.

**Important:** the embedding model that built the corpus must match `EMBED_PROVIDER` at query time. If they differ, similarity scores collapse and the assistant declines questions it should answer. Re-run `make ingest` after changing the embedder.

## API

All `/v1` routes require `Authorization: Bearer <token>`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness check (no auth) |
| `POST` | `/v1/conversations` | Create a conversation |
| `POST` | `/v1/conversations/{id}/messages` | Ask a question. Body: `{"content": "...", "stream": true}`. With `stream: true` the response is Server-Sent Events (`token`, then `citations`, then `done`). Otherwise it's a single JSON answer. |
| `GET` | `/v1/chunks/{id}` | Preview one passage (404 unknown, 403 not in your audiences) |
| `POST` | `/v1/messages/{id}/feedback` | Rate an answer: `{"rating": 1 or -1, "comment": "..."}` |

Outcomes returned with each answer are `answered`, `declined_no_source`, or `routed_to_human`.

## Testing and quality gates

```sh
make test                # unit tests
make race                # unit tests with the race detector and goroutine leak checks
make test-privacy        # audience-filter gate; fails if no database is available
make eval                # golden set against the LLM (needs ANTHROPIC_API_KEY)
make eval-retrieval      # golden set, retrieval only
```

The eval gates are:

- **Recall@5 ≥ 0.80** on answerable questions.
- **Correct decline ≥ 0.95** on questions the handbook doesn't answer.

The golden set lives in [`testdata/golden/v1.json`](testdata/golden/v1.json). The sample handbook in `testdata/handbook/` is fictional and exists only for development and evals.

## Layout

| Path | Contents |
|---|---|
| `cmd/server` | HTTP API |
| `cmd/ingest` | Loads a corpus into versioned, embedded documents |
| `cmd/eval` | Runs the golden set against the quality gates |
| `internal/store` | Postgres schema, publishing, and the ingestion job queue |
| `internal/retrieval` | Audience-filtered hybrid search (vector and full-text, fused with RRF) |
| `internal/answer` | Policy checks, weak-evidence gate, grounded prompt, citations |
| `internal/auth`, `internal/httpapi` | Bearer auth and the HTTP handlers |
| `web/` | Browser UI in Yew (Rust → WASM). `make web-test`, `make web` |
| `spec.md`, `proposal.md` | Acceptance criteria and the design reference |

## Status

Built: ingestion, hybrid retrieval, grounded answers with citations and SSE streaming, declines, feedback, the retrieval-only tier, and a Yew web UI (token sign-in, answers with sources, feedback). The UI reads full answers and doesn't stream them yet.

Not yet built: the Slack bot, admin and webhook endpoints, a local Ollama generator tier, and a live test against a real identity provider.
