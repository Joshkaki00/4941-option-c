# Spec: Ask the Handbook (v1.0 MVP)

**Status:** v1.0 | **Date:** 2026-11-19  
**Design reference:** `proposal.md` (Draft v3) — architecture, data model, and tradeoffs live there. This file is the testable contract for delivery.

## Scope

Build a Go service that answers employee handbook questions via RAG: hybrid retrieval over audience-filtered chunks, grounded LLM answers with citations, or an explicit **"I don't know"** when the corpus does not support an answer.

**In scope:** company-wide handbook ingest, SSO-authenticated ask API (SSE), Slack bot (ack-then-process), decline/handoff, frozen eval gate, retrieval-time privacy.

**Out of scope (v1):** HRIS personal balances, actions (tickets/leave), Slack/email as sources, fine-tuning, restricted-doc rollout beyond company-wide, dedicated vector DB / Kafka / K8s / microservices.

---

## Quality gates

Each gate must pass before the corresponding phase is “done.” Do not proceed to the next delivery phase until the gate is green.

### Gate 1 — Unit + race (every code change)

| | |
|---|---|
| **When** | After any change to handlers, retrieval, concurrency, or auth |
| **Invoke** | `go test ./...` and `go test -race ./...` from repo root (once the Go module exists). Optionally: `go test -race ./internal/...` for a faster subset while iterating. |
| **Success** | Exit code `0` for both. No `-race` data-race reports. Goroutine-leak tests (e.g. `goleak`) pass where added. Failures block commit. |

### Gate 2 — Golden evals (chunking, prompt, model, or ingest changes)

| | |
|---|---|
| **When** | After changes to chunking, embeddings, prompts, model choice, or a completed ingestion run |
| **Invoke** | `go run ./cmd/eval -set testdata/golden/v1.json` (or `make eval`). Fixture must include answerable items **and** at least one expected decline (e.g. G06: “Can I get reimbursed for a home espresso machine?” → **"I don't know"**). |
| **Success** | On answerable items: **recall@5 ≥ 0.8**. On decline-class items: **correct-decline rate ≥ 0.95** (answer contains “I don't know” / standard decline copy; `outcome` is `declined_no_source` or `routed_to_human`; no invented policy numbers or eligibility). Exit code `0`; CI fails otherwise. |

### Gate 3 — Privacy at retrieval (before any restricted-doc enablement and in CI)

| | |
|---|---|
| **When** | Before indexing any non–company-wide document; on every PR/CI run that touches retrieval SQL or audience sync |
| **Invoke** | `go test ./internal/retrieval -run AudienceFilter` (or `make test-privacy`). Seed: user **U** with audiences `{all-employees}`; doc **D_secret** tagged only `{hr-only}`; chunk **C** belonging to **D_secret**. Call retrieval for **U** with a query that would match **C** by embedding/keyword if unfiltered. |
| **Success** | Result set contains **zero** chunks from `D_secret`. Assert in test that the LLM client mock received **no** call whose prompt includes `C.content`. `GET /v1/chunks/{id}` for **C** as **U** returns **403**. Default-deny: a document with **no** `document_audiences` rows never appears in search. |

### Gate 4 — Ask-path smoke (before calling MVP “shippable”)

| | |
|---|---|
| **When** | End of MVP; after deploy to a staging-like environment with sample handbook loaded |
| **Invoke** | `./scripts/smoke-ask.sh` (or curl): obtain SSO test token → `POST /v1/conversations` → `POST /v1/conversations/{id}/messages` with `{"content":"How many vacation days do full-time employees get?","stream":false}` and a second ask with the G06 decline question. |
| **Success** | Answerable ask: HTTP **200**, `outcome=answered`, non-empty `citations[]` each with document title/section/last-updated, latency **p95 &lt; 10s** time-to-first-byte on stream (or total for non-stream). Decline ask: `outcome=declined_no_source`, answer text includes **"I don't know"**, empty or unused citations for authority, handoff present. Unauthenticated request → **401**. |

---

## Acceptance criteria (Given / When / Then)

### AC1 — Grounded vacation answer

**Given** a full-time employee authenticated via SSO whose audiences include `all-employees`, and the current handbook index contains a PTO policy chunk stating the annual vacation allotment  
**When** they ask “How many vacation days do I get?” via `POST /v1/conversations/{id}/messages`  
**Then** the response has `outcome=answered`, the answer states the allotment only as supported by retrieved chunks, and `citations[]` includes that policy’s document title, section/heading path, and last-updated date

### AC2 — Unknown answer is not invented

**Given** an authenticated employee and a handbook index that does **not** mention home-appliance or espresso reimbursements  
**When** they ask “Can I get reimbursed for a home espresso machine?”  
**Then** the response has `outcome=declined_no_source`, the answer includes **“I don't know”** (or the standard decline copy), no citation is presented as proof of a reimbursement rule, and a human handoff target is returned

### AC3 — Personal balance is out of scope

**Given** an authenticated employee and handbook text that describes PTO policy generally but no HRIS integration  
**When** they ask “How many vacation days do I have left this year?”  
**Then** the system does not invent a personal balance; `outcome` is `declined_no_source` or `routed_to_human`, and the answer indicates the handbook cannot answer personal balances

### AC4 — Audience filter before the LLM

**Given** document “Compensation Bands” is tagged only for audience `hr-only`, and employee E has audiences `{all-employees}` only  
**When** E asks a question whose embedding/keywords would match that document if unfiltered  
**Then** retrieval returns no chunks from “Compensation Bands”, the LLM is not prompted with that document’s text, and E’s answer does not disclose restricted compensation content

### AC5 — Citation preview re-checks access

**Given** employee E is not in audience `hr-only`, and chunk C belongs to an `hr-only` document  
**When** E requests `GET /v1/chunks/{C.id}`  
**Then** the API returns **403** (or equivalent deny) and does not return `C.content`

### AC6 — Streaming ask completes with message id

**Given** an authenticated employee and a non-empty company-wide index  
**When** they send an ask with `stream:true`  
**Then** the server emits SSE events including tokens (and/or citations) and a final `done` event that includes a persisted `message_id` usable for `POST /v1/messages/{id}/feedback`

### AC7 — Slack ack does not block on generation

**Given** the Slack bot is configured and the ask worker is healthy  
**When** a user posts a handbook question in Slack  
**Then** Slack receives an acknowledgement within the platform’s required window, a placeholder appears, and the message is updated later with either a cited answer or an **“I don't know”** decline (or timeout/error text if generation fails)

---

## Non-functional checks (tie-breakers)

| Check | Verify |
|---|---|
| Identity | Only SSO token identity is used; body/header user ids cannot escalate |
| Sync ask path | Question handling is request-scoped + streamed; not “enqueue and poll” for the happy path |
| Cost sanity | Logged tokens/question roughly ~4k in / ~300 out; alert if monthly LLM spend ≫ Sonnet ballpark in `proposal.md` §9.1 |

## Versioning

| Version | Meaning |
|---|---|
| v1.0 | MVP contract above |
| v1.1 | Restricted documents + stronger audience evals (proposal later phase) |
| v1.2 | HRIS personal answers (explicit new ACs; not implied by v1.0) |
