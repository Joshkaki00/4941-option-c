# Load local secrets/overrides from .env when present (never commit .env).
ifneq (,$(wildcard .env))
include .env
export
endif

# Local defaults use the Unix socket and the current OS user. Override in the environment.
DATABASE_URL      ?= postgres:///handbook
TEST_DATABASE_URL ?= postgres:///handbook_test
export DATABASE_URL TEST_DATABASE_URL

.PHONY: test race vet test-privacy eval eval-retrieval ingest run run-dev smoke env

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

# Gate 3: privacy at retrieval and at the LLM boundary. Fails instead of skipping when no DB.
test-privacy:
	REQUIRE_DB=1 go test -race -count=1 -v ./internal/retrieval ./internal/answer -run AudienceFilter

# Gate 2: golden evals (needs ANTHROPIC_API_KEY, or use eval-retrieval).
eval:
	go run ./cmd/eval -set testdata/golden/v1.json

eval-retrieval:
	LLM_PROVIDER=none go run ./cmd/eval -set testdata/golden/v1.json -retrieval-only

ingest:
	go run ./cmd/ingest -dir testdata/handbook

# Local run: hash embedder and retrieval-only tier, no external services except Postgres.
run-dev:
	EMBED_PROVIDER=hash LLM_PROVIDER=none DEV_AUTH_TOKENS='dev-alice|alice|;dev-bob|bob|' go run ./cmd/server

smoke:
	./scripts/smoke-ask.sh

# Create .env from the example if missing.
env:
	@test -f .env || cp .env.example .env
	@echo "Edit .env and set ANTHROPIC_API_KEY=sk-..."

# Local run with Anthropic (requires ANTHROPIC_API_KEY in .env or the environment).
run:
	@test -n "$$ANTHROPIC_API_KEY" || (echo "ANTHROPIC_API_KEY is empty; set it in .env" >&2; exit 1)
	go run ./cmd/server

