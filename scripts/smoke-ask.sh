#!/usr/bin/env bash
# Gate 4 smoke test: exercises a running server end to end (spec.md Gate 4).
#
#   BASE_URL   default http://localhost:8080
#   TOKEN      employee bearer token (dev: DEV_AUTH_TOKENS entry, default dev-alice)
#   EXPECT_LLM 1 when a hosted LLM is configured (answerable asks must be "answered")
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
TOKEN="${TOKEN:-dev-alice}"
OTHER_TOKEN="${OTHER_TOKEN:-dev-bob}"
EXPECT_LLM="${EXPECT_LLM:-0}"
ANSWERABLE_Q="How many vacation days do full-time employees get?"
DECLINE_Q="Can I get reimbursed for a home espresso machine?"
HR_ONLY_Q="What is the salary range for a senior engineer?"

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "ok:   $*"; }

command -v jq >/dev/null || fail "jq is required"
command -v curl >/dev/null || fail "curl is required"

# req METHOD PATH [TOKEN] [BODY] -> sets STATUS and BODY
req() {
  local method="$1" path="$2" token="${3:-}" body="${4:-}"
  local tmp
  tmp=$(mktemp)
  local args=(-sS -o "$tmp" -w '%{http_code}' -X "$method" "$BASE_URL$path" -H 'Content-Type: application/json')
  if [[ -n "$token" ]]; then args+=(-H "Authorization: Bearer $token"); fi
  if [[ -n "$body" ]]; then args+=(--data-raw "$body"); fi
  STATUS=$(curl "${args[@]}")
  BODY=$(cat "$tmp")
  rm -f "$tmp"
}

# 1. Liveness
req GET /healthz
[[ "$STATUS" == "200" ]] || fail "healthz returned $STATUS"
pass "healthz 200"

# 2. Unauthenticated ask is rejected
req POST /v1/conversations/00000000-0000-4000-8000-000000000000/messages "" '{"content":"hi"}'
[[ "$STATUS" == "401" ]] || fail "unauthenticated ask returned $STATUS, want 401"
pass "unauthenticated ask -> 401"

# 3. Start a conversation as the employee
req POST /v1/conversations "$TOKEN"
[[ "$STATUS" == "201" ]] || fail "create conversation returned $STATUS"
CONV=$(jq -r '.id' <<<"$BODY")
[[ -n "$CONV" && "$CONV" != "null" ]] || fail "no conversation id in response"
pass "conversation created ($CONV)"

# 4. Answerable question (AC1 / Gate 4 happy path)
req POST "/v1/conversations/$CONV/messages" "$TOKEN" "$(jq -cn --arg c "$ANSWERABLE_Q" '{content:$c, stream:false}')"
[[ "$STATUS" == "200" ]] || fail "answerable ask returned $STATUS: $BODY"
OUTCOME=$(jq -r '.outcome' <<<"$BODY")
MSG_ANSWERABLE=$(jq -r '.message_id' <<<"$BODY")
CITES=$(jq '.citations | length' <<<"$BODY")
CHUNK_ID=$(jq -r '.citations[0].chunk_id // empty' <<<"$BODY")
if [[ "$EXPECT_LLM" == "1" ]]; then
  [[ "$OUTCOME" == "answered" ]] || fail "answerable ask outcome=$OUTCOME, want answered"
  [[ "$CITES" -ge 1 ]] || fail "answered ask has no citations"
  jq -e '.citations[0] | has("document_title") and has("section") and has("last_updated")' <<<"$BODY" >/dev/null \
    || fail "citation missing title/section/last_updated"
  pass "answerable ask answered with $CITES citation(s)"
else
  # Retrieval-only tier: sources are shown, but no generated answer is produced.
  [[ "$OUTCOME" == "routed_to_human" ]] || fail "retrieval-only tier outcome=$OUTCOME, want routed_to_human"
  [[ "$CITES" -ge 1 ]] || fail "retrieval-only tier must still show source passages"
  pass "answerable ask in retrieval-only tier shows $CITES source(s) and routes to a human"
fi

# 5. Decline question (AC2 / G06): no invented policy, "I don't know", handoff present
req POST "/v1/conversations/$CONV/messages" "$TOKEN" "$(jq -cn --arg c "$DECLINE_Q" '{content:$c, stream:false}')"
[[ "$STATUS" == "200" ]] || fail "decline ask returned $STATUS"
OUTCOME=$(jq -r '.outcome' <<<"$BODY")
ANSWER=$(jq -r '.answer' <<<"$BODY")
[[ "$OUTCOME" == "declined_no_source" ]] || fail "decline ask outcome=$OUTCOME, want declined_no_source"
grep -q "I don't know" <<<"$ANSWER" || fail "decline answer missing \"I don't know\": $ANSWER"
[[ "$(jq -r '.handoff // empty' <<<"$BODY")" != "" ]] || fail "decline has no handoff"
pass "decline ask -> declined_no_source with handoff"

# 6. Restricted topic is not visible to an employee (AC4 at the API level)
req POST "/v1/conversations/$CONV/messages" "$TOKEN" "$(jq -cn --arg c "$HR_ONLY_Q" '{content:$c, stream:false}')"
[[ "$STATUS" == "200" ]] || fail "hr-only probe returned $STATUS"
OUTCOME=$(jq -r '.outcome' <<<"$BODY")
[[ "$OUTCOME" == "declined_no_source" ]] || fail "hr-only probe outcome=$OUTCOME, want declined_no_source"
if grep -q "Compensation Bands" <<<"$BODY"; then fail "restricted document title leaked to employee"; fi
pass "hr-only probe declined; restricted title not disclosed"

# 7. Streaming ask emits token/citations/done, with message_id on done (AC6)
req POST "/v1/conversations/$CONV/messages" "$TOKEN" "$(jq -cn --arg c "$ANSWERABLE_Q" '{content:$c, stream:true}')"
[[ "$STATUS" == "200" ]] || fail "stream ask returned $STATUS"
grep -q '^event: token' <<<"$BODY" || fail "no token event in stream"
grep -q '^event: done' <<<"$BODY" || fail "no done event in stream"
DONE_JSON=$(awk '/^event: done/{getline; sub(/^data: /,""); print; exit}' <<<"$BODY")
MSG_STREAM=$(jq -r '.message_id' <<<"$DONE_JSON")
[[ -n "$MSG_STREAM" && "$MSG_STREAM" != "null" ]] || fail "done event has no message_id"
pass "stream ask completed with done.message_id"

# 8. Feedback on a message the caller owns
req POST "/v1/messages/$MSG_ANSWERABLE/feedback" "$TOKEN" '{"rating":1,"comment":"smoke"}'
[[ "$STATUS" == "201" ]] || fail "feedback returned $STATUS"
pass "feedback recorded"

# 9. Chunk preview for a passage the caller may see (cited in step 4)
req GET "/v1/chunks/$CHUNK_ID" "$TOKEN"
[[ "$STATUS" == "200" ]] || fail "chunk preview for cited passage returned $STATUS"
pass "chunk preview 200 for a cited passage"

# 10. Another valid user cannot ask in this conversation (must be 404, not 200)
req POST "/v1/conversations/$CONV/messages" "$OTHER_TOKEN" '{"content":"hi","stream":false}'
[[ "$STATUS" == "404" ]] || fail "cross-user ask returned $STATUS, want 404 (is OTHER_TOKEN a valid second user?)"
pass "cross-user access denied (404)"

echo "smoke: all checks passed"
