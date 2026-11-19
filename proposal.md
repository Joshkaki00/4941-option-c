# Proposal: "Ask the Handbook" - an internal RAG assistant

**Status:** Draft v2 | **Date:** 2026-11-19
**Summary:** A Go service that answers employee questions from the company's ~500 pages of policies and how-to guides. It retrieves relevant passages, asks an LLM to answer only from them, and shows cited sources. Requests are handled synchronously, with goroutines used inside a request for bounded parallel work. Async processing is used only where work must outlive a request (ingestion, scheduled jobs, Slack acknowledgements). If the hosted LLM is unavailable it falls back to a local model on a 32 GB Apple Silicon Mac, then to retrieval-only results.

---

## 1. Goals and non-goals

**Goals**
- Answer natural-language questions ("How many vacation days do I get?") grounded in the handbook.
- Cite the document, section, and last-updated date for every answer.
- Say "I couldn't find this" and point to a human (HR/IT/doc owner) when the handbook doesn't cover it.
- Respect access rules (SSO identity, per-document audiences).
- Stay current: document changes appear in answers the same day.
- Remain low cost and operable part-time by one engineer.

**Non-goals (v1)**
- Taking actions (filing leave requests, tickets).
- Personal data lookups ("how many days do *I* have left?"). That needs HRIS integration and per-user authorization.
- Sources beyond the handbook (Slack, email).
- Legal, medical, or disciplinary advice; sensitive topics route to a human.
- Fine-tuning a model; RAG covers the need.

## 2. Scale

| Quantity | Estimate |
|---|---|
| Employees | 300 |
| Weekly active users | ~100-150 |
| Questions/day | ~50-100 (peaks maybe 10x after announcements) |
| Peak rate | well under 1 request/sec |
| Corpus | ~500 pages, about 250-400k tokens, about 1-2k chunks (~12 MB of vectors) |

Throughput is not the hard problem. Answer quality, access control, and freshness are.

## 3. Architecture

```
ONLINE (per question, synchronous to the caller, streamed)
Client (Slack/Web) -> Go API -> SSO verify
                       |-> Postgres (pgvector + full-text) -> hybrid search -> (rerank)
                       |-> LLM (Claude API or local fallback) -> cited answer (SSE)

OFFLINE (async, durable)
Webhook / nightly sync -> ingestion_jobs table -> worker goroutine(s)
   -> parse -> chunk -> embed -> Postgres
```

**Query flow:** authenticate, rewrite follow-ups into a standalone query, run hybrid retrieval (vector + keyword, merged with reciprocal rank fusion) filtered to documents the user may see, rerank to ~5, build the prompt with grounding rules, stream the answer with citations, log.

**Key decisions**
- **RAG over long-context stuffing or fine-tuning:** cheaper per question, precise citations, permission filtering before the model sees anything, and updates by re-indexing one document.
- **Hybrid search:** vectors catch paraphrases ("PTO"), keywords catch exact terms (form names, policy IDs).
- **Structure-aware chunking:** split by heading, prefix each chunk with title and heading path, keep tables intact.
- **Access control at retrieval, default-deny:** the prompt is not a security boundary.
- **Postgres + pgvector for everything:** at ~2k chunks a dedicated vector DB is unnecessary, and one database gives transactional re-indexing.

## 4. Concurrency model: synchronous by default, async only where needed

**Principle:** The user's question is one synchronous request with a streamed response. Inside it, goroutines are used only for independent, network-bound steps. Anything that must survive a restart or cannot block the caller is async and durable. Nothing is async "because we can."

### 4.1 Synchronous path (goroutines inside the request)

| Step | Concurrency | Why |
|---|---|---|
| Load user audiences + conversation history | Parallel (2 goroutines) | Independent reads at request start |
| Query embedding (API call) and keyword search | Parallel | Keyword search does not need the embedding; the embedding call is the slow leg |
| Vector search | After embedding | Depends on the embedding |
| Merge (RRF) + rerank | Sequential | Depends on both legs |
| LLM generation | Streamed: generation goroutine -> channel -> SSE handler | Tokens reach the user as they are produced |
| Save the assistant message row | Synchronous, before the final `done` event | The client needs `message_id` for feedback |

**Rules for these goroutines**
- Request-scoped: they derive from `r.Context()`, never `context.Background()`, and are joined before the handler returns.
- Use `errgroup` with a context. Choose per step: **fail-fast** for must-have work (audiences, history); **partial results** (WaitGroup + joined errors) for retrieval legs, so one failed leg degrades to the other instead of failing the question.
- Per-stage timeouts (e.g. retrieval ~2 s, time-to-first-token ~10 s).
- Client disconnect cancels the upstream LLM call by passing the request context. The SSE loop selects on `r.Context().Done()`.
- The server's `WriteTimeout` applies to the whole response, so streaming routes need it relaxed (per-response write deadlines).
- Measure before parallelizing small steps. At ~2k chunks each database query is a few milliseconds; the real gain is overlapping the embedding API call.

### 4.2 Async paths (only these)

| Work | Why async | How |
|---|---|---|
| Document ingestion / re-embedding | Slow, can fail halfway, must not be lost on restart | Webhook returns 202 and enqueues a row in `ingestion_jobs`; worker claims with `FOR UPDATE SKIP LOCKED`; retries with backoff. (River is a Postgres-backed Go queue that does this out of the box.) |
| Nightly sync, retention cleanup, Postgres snapshot to the Mac | Scheduled, idempotent | Ticker goroutine (or River periodic jobs) |
| Slack events | Slack expects a quick acknowledgement, so the answer cannot be produced inside that window | Acknowledge immediately, post a placeholder, generate in a bounded worker, update the message; timeout and error text if it fails |
| Detailed retrieval logging (all retrieved chunks per question) | Useful for debugging, not needed by the user | Bounded in-process channel with one writer goroutine; drop-and-count if full |

**Deliberately NOT async:** answering questions (the user is waiting anyway), feedback submission (one insert), eval runs (a CLI/CI job), and the assistant message row.

### 4.3 Guardrails (apply to every goroutine)
- **Bounded:** a global semaphore caps concurrent LLM calls (e.g. 2 for the local fallback); `errgroup.SetLimit` on fan-out; fixed number of ingestion workers (respects embedding API rate limits); `pgxpool` max connections sized so (concurrent requests x parallel queries per request) fits the pool.
- **Backpressure:** when the cap is reached, wait briefly, then return "busy, try again" (429) for the hosted path, or queue with a visible position for the local fallback.
- **Owned:** every goroutine has an owner and an exit condition (context, closed channel). Graceful shutdown: stop accepting requests, cancel contexts, wait for in-flight work and the ingestion worker.
- **Panic isolation:** an unrecovered panic in any goroutine kills the whole process, so goroutine wrappers at boundaries recover, log, and mark the request failed.
- **Leak detection:** `go test -race`, `goleak` in tests, goroutine-count metric, pprof on an internal-only port. Watch for the classic leak: unbuffered result channels where the receiver has already quit.

## 5. Data model (Postgres)

| Table | Purpose / key fields |
|---|---|
| `documents` | id, source_system, source_id, title, url, owner, category, status, current_version_id |
| `document_versions` | id, document_id, content_hash, effective_date, last_updated, is_current |
| `chunks` | **stable text id**, version_id, document_id, heading_path[], content, chunk_type, ordinal, embedding vector, tsv tsvector, is_current |
| `audiences`, `document_audiences` | named access scopes; no row = deny |
| `users`, `user_groups` | synced from SSO (country, employment type, audiences) |
| `conversations`, `messages` | question/answer, rewritten_query, outcome, model, prompt_version, latency, tokens |
| `message_citations` | message_id, chunk_id, rank, score, used_in_answer (store all retrieved, not only cited) |
| `feedback` | message_id, rating, comment |
| `ingestion_jobs` | id, document_id (or null = full sync), kind, status, attempts, run_after, locked_at, last_error |
| `ingestion_runs` | status, counts, errors |
| `golden_questions`, `eval_runs`, `eval_results` | frozen eval set and scores per configuration |

Notes: new ingestion creates a new version and new chunks, then flips `is_current` in one transaction (old chunks are kept for audit and excluded from search). Stable chunk IDs let evals and audits survive re-ingestion. Jobs are enqueued transactionally with the change that caused them.

## 6. API (REST/JSON, `/v1`, SSO bearer token)

| Method and path | Purpose |
|---|---|
| `POST /conversations` | Start a conversation |
| `POST /conversations/{id}/messages` | **Ask a question.** Returns `{answer, citations[], outcome, handoff}`; `stream:true` gives SSE events (`token`, `citations`, `done`) |
| `GET /conversations/{id}`, `GET /conversations`, `DELETE /conversations/{id}` | History (owner only) |
| `POST /messages/{id}/feedback` | Thumbs up/down plus comment |
| `GET /chunks/{id}` | Cited passage preview (re-checks access) |
| `POST /admin/ingestion/runs`, `GET /admin/ingestion/runs/{id}` | Manual re-index (enqueues jobs, returns 202), status |
| `POST /admin/webhooks/source-changed` | Signed webhook; enqueues an ingestion job and returns 202 |
| `GET /admin/analytics/unanswered`, `GET /admin/analytics/summary` | Handbook-gap report (aggregated, no identities) |
| `GET /healthz` | Liveness/readiness |

`outcome` is one of `answered`, `declined_no_source`, `routed_to_human`, `clarification_needed`. User identity comes only from the token. Clients send only the new question; the server holds conversation context. Optional `Idempotency-Key` header on asks.

## 7. Technology stack (Go, low cost)

| Layer | Choice |
|---|---|
| Service | Go, stdlib `net/http`; one binary; `errgroup`, semaphores, and `context` for concurrency (no custom channel frameworks) |
| DB | Postgres + pgvector via `pgx` and `pgvector-go` |
| Job queue | `ingestion_jobs` table with SKIP LOCKED (or River) - no extra infrastructure |
| LLM (primary) | Anthropic API via the official Go SDK; start with a Sonnet-class model, test a Haiku-class model on the golden set |
| Embeddings | Open-weight model (e.g. bge-m3 or nomic-embed-text) so the same vectors can be produced hosted or locally |
| Reranker | Skip in v1; add a hosted rerank endpoint if evals show a gain |
| Front end | Slack bot first |
| Doc parsing | Go for Markdown/HTML/DOCX; a small Python step for messy PDFs (offline only) |
| Hosting | **Option B (recommended):** small container host plus managed Postgres with backups. **Option A:** one VPS with Postgres in Docker and a nightly dump |

Avoid: LangChain-style frameworks, a dedicated vector DB, Kafka, Kubernetes, microservices.

**Estimated monthly cost** (about 3,000 questions/month, ~4k input and ~300 output tokens each; verify current rates):

| Item | Cost |
|---|---|
| LLM, Haiku-class | ~$2 |
| LLM, Sonnet-class | ~$33 |
| Embeddings | ~$0 |
| Compute + Postgres | $0-$25 |
| **Total** | **~$5-$60** |

## 8. Resilience and fallback

| Tier | Trigger | Behavior |
|---|---|---|
| 0 Normal | Healthy | Hybrid search, rerank, hosted LLM |
| 1 Local LLM | Hosted LLM times out, returns 5xx, or is rate-limited (circuit breaker) | Same retrieval, shorter context, local model on the Mac, "backup mode" banner |
| 2 Retrieval-only | Local box down or overloaded | Top 3 passages with links, no generated text |

**Local host:** 32 GB Apple Silicon (M2 Pro or Max; chip to be confirmed, since bandwidth is 200 vs 400 GB/s). Usable GPU memory is about 22-24 GB.
- Primary candidate: **Qwen3.6 35B-A3B, 4-bit MLX** (MoE, fast; ~20 GB, tight fit). Alternatives: Gemma 4 26B A4B; Qwen3.6 27B dense (~17 GB, slower); a 14B dense model.
- Run via Ollama (MLX tags) or `mlx_lm.server`; thinking mode off; model pinned in memory.
- Concurrency cap of 2 (the semaphore from section 4.3), queue the rest; about 3 chunks and ~250 output tokens.
- Benchmark prefill and decode separately on a real ~4k-token prompt.
- The Mac holds a nightly Postgres snapshot, so it is self-contained (same Go binary with `--llm=ollama`).
- Prefer an always-on, plugged-in machine over a travelling laptop; keep it out of Low Power Mode.
- Weekly drill: force the breaker open and run sample golden questions.
- If the hosted embedding API is down, Tier 1 uses keyword-only retrieval.

## 9. Evaluation and quality

- **Golden set:** 100-300 real questions (start from the HR inbox), each labeled with the chunks that contain the answer, plus out-of-scope questions that should be declined. Frozen and versioned.
- **Metrics:** recall@5 (target 0.8 or higher), context precision, faithfulness, citation correctness, correct-decline rate. Score retrieval and generation separately.
- **CI:** run on every change to chunking, embeddings, prompts, or model; run after each ingestion. Include `-race` and goroutine-leak tests.
- **Feedback loop:** every thumbs-down or bad-answer report becomes a golden-set entry.

## 10. Failure modes and blast radius

| Failure | Impact | Mitigation |
|---|---|---|
| Silent quality decay (bad parse, edits shift chunks) | Wrong answers, unnoticed | Evals after ingestion, parse-quality checks, alerts on thumbs-down spikes |
| Hosted LLM outage or rate limit | All answers fail | Tiered fallback above |
| Bad ingestion run | Index damaged | Build new version beside old, validate, then flip; keep rollback |
| Permission mistake | Restricted content leaks | Default-deny, audience-tag audits, "user X must not see doc Y" tests, short SSO sync |
| Postgres down | Total outage | Managed failover, backups, static "search the handbook" link |
| Query-embedding API down | Vector search fails | Keyword-only fallback |
| Goroutine leak or unbounded fan-out | Memory growth, slow degradation, then outage | Bounded limits, context everywhere, leak tests, goroutine-count alert |
| Panic in a goroutine | Whole process exits (single-binary blast radius) | Recover wrappers at goroutine boundaries, fast restart under supervisor |
| Ingestion starves the API (CPU, memory, DB connections) | Slow answers during re-index | Separate worker limits and pool budget; split into a second process if needed |
| Lost in-flight work on deploy/restart | Missed re-index | Durable `ingestion_jobs` plus nightly sync as a safety net |

**Growth path:** LLM latency/rate limits and cost break first, then the reranker (move it to its own service), then API instances (stateless, easy), then message-table growth (retention, partitioning). Cache answers keyed by (normalized question, audience set, document version), never by question alone.

## 11. Security and privacy
- SSO (OIDC); identity from the token only; per-user rate limits.
- Retrieval filtered by audience before any text reaches a model.
- Treat retrieved text as data (prompt-injection hygiene).
- Questions may contain sensitive disclosures: restrict log access, set retention (e.g. 90 days), aggregate admin analytics, let users delete their history, and tell users what is stored.
- Use an LLM vendor agreement that excludes training on company data (legal review).
- Safest v1 scope: index only company-wide documents; add restricted ones later with audience filters.

## 12. Tradeoffs

**Core design**

| Decision | Chosen | Alternative | Why / what we give up |
|---|---|---|---|
| Grounding method | RAG | Whole handbook in the prompt; fine-tuning | Cheaper, precise citations, permission filtering, easy updates. Gives up: retrieval can miss the right chunk (hence evals). |
| Retrieval | Hybrid (vector + keyword) | Vector only | Handles both paraphrases and exact terms. Gives up: a merge step to tune. |
| Reranker | Add only if evals show gain | Always rerank | Saves a component and latency in v1. |
| Chunking | By heading with path prefix | Fixed-size windows | Keeps context attached. Gives up: parser work for messy PDFs. Verify on your own golden set. |
| Store | Postgres + pgvector | Dedicated vector DB | One system, transactional re-index. Gives up: headroom if the corpus grows 1,000x. |
| Access control | Filter at retrieval | Instruct the LLM to hide content | Prompts are not a security boundary. Gives up: permission data must stay in sync with SSO. |
| Unknown answers | Decline and route to a human | Let the model fill gaps | A wrong policy answer is worse than none. Gives up: some users find it less helpful. |
| LLM hosting | Hosted API with local fallback | Self-host only | Quality and low ops. Gives up: third-party dependency (mitigated by tiers). |

**Concurrency and async**

| Decision | Chosen | Alternative | Why / what we give up |
|---|---|---|---|
| Question handling | Synchronous request with streamed response | Queue every question and poll/push the answer | Simpler, lower latency, no job state, streaming works naturally. Gives up: each question holds a connection and goroutine for ~3-5 s, and spikes show "busy" instead of eventually answering. Fine at under 1 request/sec. |
| Async scope | Only ingestion, scheduled jobs, Slack acks, and best-effort logging | Async everywhere | Avoids durable job state, polling UIs, and harder debugging for work the user is waiting on anyway. Gives up: two execution styles to understand. |
| In-request parallelism | Goroutines only for independent network-bound steps (embedding call vs keyword search, audiences vs history) | Fully sequential, or parallelize everything | Overlaps the slow embedding call. Gives up: added complexity, error handling, and more DB connections per request. The gain from parallelizing tiny database queries is negligible, so measure first. |
| Error semantics in fan-out | Partial results for retrieval legs; fail-fast for must-have work | Fail-fast everywhere | One failed leg degrades to the other instead of failing the question. Gives up: more code than a plain errgroup. |
| Goroutine limits | Bounded (semaphore, SetLimit, fixed workers) | Unbounded `go func()` | Predictable memory and protects the database and LLM rate limits. Gives up: some requests wait or get 429. |
| Ingestion execution | Durable `ingestion_jobs` table (or River) with SKIP LOCKED | A goroutine launched from the webhook handler | Survives restarts and supports retries; enqueue is transactional. Gives up: one table and a worker loop. |
| Best-effort logging | Bounded channel + writer goroutine, drop when full | Synchronous inserts, or a durable queue | Keeps logging off the answer path. Gives up: a few detail rows can be lost on crash or overload. The message row itself is written synchronously. |
| Client disconnects | Cancel the in-flight LLM call | Let generation finish | Saves cost and, importantly, local-model capacity. Gives up: the abandoned answer is not stored. |
| Slack integration | Ack immediately, process in a bounded worker | Synchronous reply | Required by Slack's short acknowledgement window. Gives up: no durability if the process dies mid-answer (placeholder message plus timeout text mitigates). |
| Process layout | One binary with API and worker goroutines | Separate API and worker processes | Simplest deploy. Gives up: a panic or a runaway ingestion hits everything; mitigated by recover wrappers and limits, and a flag can split the worker out later. |
| Concurrency primitives | `errgroup`, semaphores, context | Custom channel pipelines and actor-style code | Easier to review and test. Gives up: less flexibility for exotic flows we don't need. |

## 13. Delivery plan

1. **MVP (1-2 weeks):** ingestion for company-wide docs (job table + one worker), hybrid search with parallel legs, grounded cited answers over SSE, Slack bot with ack-then-process, SSO.
2. **Quality pass:** golden set, evals in CI, table handling, query rewriting, optional reranker.
3. **Resilience:** generator interface, circuit breaker, LLM semaphore, local fallback on the Mac, Postgres snapshot, retrieval-only tier, weekly drill.
4. **Hardening:** goroutine-leak tests, `-race` in CI, graceful shutdown, pprof and goroutine metrics, load test with 20-30 simultaneous questions.
5. **Operate:** feedback, unanswered-question report (reveals handbook gaps), dashboards and alerts.
6. **Later:** HRIS integration for personal answers, restricted documents, web UI, answer cache.

## 14. Open questions
1. Is any handbook content restricted by role, team, or country?
2. Where do the documents live, and who owns updates?
3. Slack, Teams, or an intranet page? (Affects the async acknowledgement design.)
4. Acceptable-risk bar: is an occasional wrong answer tolerable if it always shows its source?
5. Which M2 chip (Pro or Max) is the fallback host, and can it be a dedicated always-on machine?
6. Does legal permit sending employee questions to a hosted LLM?
7. Build vs. buy: would an off-the-shelf enterprise assistant meet the access and citation needs at lower maintenance cost?

## Sources
- Unstructured, "RAG Pipeline Best Practices for Enterprise Systems" (2026)
- StackAI, "RAG best practices: chunking, embeddings, reranking, hybrid search"
- Olostep, "How to Evaluate RAG Systems" (2026)
- Glukhov, "Bounding Concurrency in Go: errgroup, Worker Pools, and Backpressure"
- Encore, "Background Jobs in Go: Queues, Workers and Scheduled Tasks" and "How to Implement Server-Sent Events (SSE) in Go" (Nov 2026)
- Brandur, "River: a Fast, Robust Job Queue for Go + Postgres"
- MetaCTO / Finout, Anthropic API pricing summaries (2026; verify against Anthropic's pricing page)
- Neon, "Cheapest Postgres hosting for developers (2026)"; Toolradar, "Serverless Postgres pricing compared"
- pgvector-go README; pgdash, "RAG with PostgreSQL" (Go)
- InsiderLLM, Mac local-LLM guides; ApX and llmrun M2 Max 32 GB pages; Ollama/MLX runtime write-ups