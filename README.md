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
| `WEBHOOK_SECRET` | HMAC key for the source-changed webhook (32+ characters). Empty disables it. |
| `INGEST_ROOT`, `INGEST_POLL_INTERVAL` | Corpus directory and how often the server drains the ingestion queue |
| `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN` | Enables the Slack bot over Socket Mode. `xoxb-` bot token and `xapp-` app-level token. Set both or neither. |
| `SLACK_WORKERS`, `SLACK_QUEUE` | Concurrent Slack answers and the pending-question limit before "busy" replies |

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
| `POST` | `/v1/admin/ingestion/runs` | Admin. Queue every corpus file for ingestion (202, `{"queued": n}`) |
| `GET` | `/v1/admin/ingestion/runs/{id}` | Admin. One ingestion job's status |
| `POST` | `/v1/admin/webhooks/source-changed` | Signed by `X-Handbook-Signature: sha256=<hmac of body>`, no bearer token. Body `{"source_path": "pto-policy.md"}`. Queues one file (202). |
| `GET` | `/v1/admin/analytics/summary` | Admin. Message counts per outcome |
| `GET` | `/v1/admin/analytics/unanswered?limit=N` | Admin. Most-asked unanswered questions. No user identities. |

Admin routes need a token whose audience includes `handbook-admin`, granted through SSO groups (or the dev token list). Outcomes returned with each answer are `answered`, `declined_no_source`, `routed_to_human`, or `clarification_needed`.

## Slack bot

Mention the bot in a channel (`@Handbook how many PTO days do I get?`) or message it directly. It replies in a thread: a placeholder appears at once, then the placeholder is replaced with the cited answer. Slack acknowledgements don't wait for generation. Questions are answered by a bounded worker pool, and when the queue is full the bot replies that it's busy.

Setup in Slack (api.slack.com/apps):

1. Turn on **Socket Mode** and create an app-level token with the `connections:write` scope. Copy it into `SLACK_APP_TOKEN` (`xapp-...`).
2. Under **OAuth & Permissions**, add bot scopes `app_mentions:read`, `chat:write`, `im:history`, `im:read`, and install the app to the workspace. Copy the **Bot User OAuth Token** into `SLACK_BOT_TOKEN` (`xoxb-...`).
3. Under **Event Subscriptions**, turn events on and subscribe to the bot events `app_mention` and `message.im`. Socket Mode needs no request URL. Then click **Reinstall to Workspace**, because Slack applies new subscriptions only after a reinstall.
4. Invite the bot to any channel it should answer in (`/invite @<bot>`). Slack refuses to post to a channel the bot hasn't joined (`not_in_channel`).

**Troubleshooting:** if the bot never replies, check that `app_mention` is listed under bot events and that the app was reinstalled after you added it. Mentions only count when they use Slack's `@` picker. Typed names don't trigger `app_mention`.

The bot reads its own user ID at startup with `auth.test`, so no other values are needed. The server refuses to start if only one of the two tokens is set, and it exits if `auth.test` rejects the bot token.

Access: Slack users get company-wide content only (`all-employees`). Restricted audiences such as `hr-only` aren't reachable from Slack until users are mapped to SSO groups.

## Ingestion and webhooks

The server drains the ingestion queue in one background worker every `INGEST_POLL_INTERVAL`. Use `POST /v1/admin/ingestion/runs` to re-index the whole corpus, or send a signed `source-changed` webhook to re-index one file. Jobs are durable in Postgres and retried with backoff.

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
| `internal/admin` | Admin routes, the signed webhook, and analytics |
| `internal/slack` | Socket Mode listener, the worker pool, and Slack replies |
| `web/` | Browser UI in Yew (Rust → WASM), streaming answers. `make web-test`, `make web` |
| `spec.md`, `proposal.md` | Acceptance criteria and the design reference |

## Status

Built: ingestion (queue, background worker, admin runs, signed webhook), hybrid retrieval, grounded answers with citations and SSE streaming, declines, feedback, the retrieval-only tier, admin analytics, a Slack bot with ack-then-answer threads, and a Yew web UI that streams answers.

Verified: unit and race tests for every package, DB-backed store tests, the privacy gate, the golden eval against the Anthropic API (7/7), the smoke script, and live checks of the admin routes, the signed webhook, and the SSE stream. The Slack bot works live over Socket Mode: it answers mentions in a channel with cited answers, declines questions the handbook doesn't cover, and routes sensitive questions to a human.

Not verified: the Yew UI in a browser, and a live test against a real identity provider. Not built: a local Ollama generator tier.
