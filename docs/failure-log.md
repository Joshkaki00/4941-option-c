# Failure log

Record agent/session failures here so patterns can move into `CLAUDE.md` or hooks.

| Date | Symptom | Root cause | Fix / warning to keep |
|---|---|---|---|
| 2026-11-19 | `depscope.dev` checks return HTTP 410 | DepScope was retired (2026-09-25) | Verify Go modules with `https://api.deps.dev/v3/systems/go/packages/<url-escaped path>` instead. |
| 2026-11-19 | `golangci-lint` fails with "export data version 5 is greater than maximum supported version 4" | Installed golangci-lint was built with an older Go than the toolchain (go1.27.4) | Rely on `go vet` until golangci-lint is rebuilt with go1.27; do not trust its "typecheck" output. |
| 2026-11-19 | `brew install pgvector` bottle has no Postgres 14 build | Bottles exist only for PG18/19 | Build from source: `make PG_CONFIG=/opt/homebrew/opt/postgresql@14/bin/pg_config && make install`. |
| 2026-11-19 | `ollama pull nomic-embed-text` fails with "unexpected EOF" | Flaky model download | Retry in the background and check the log; the Makefile's hash embedder works offline for dev. |
