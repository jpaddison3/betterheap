# betterheap

Agent-friendly CLI for Better Stack logs. Queries the live hot buffer **and** the
S3 archive as one continuous time window, so the range you ask for is the range
you get — fixing bslog's silent ~35-minute cap. Models dharma's spine (cobra +
thin client + config + output) with bslog's query ergonomics.

## Layout

- `cmd/betterheap/main.go` — binary entrypoint → `cli.Execute()`
- `internal/cli/` — cobra commands, one file per concern (`logs.go`, `tail.go`,
  `stats.go`, `trace.go`, `sources.go`, `sql.go`, `schema.go`, `auth.go`,
  `config.go`). `helpers.go` holds the shared query flags + plumbing; `sql.go` is
  the escape hatch
- `internal/client/` — thin HTTP wrappers: `clickhouse.go` (Query API, streams
  `FORMAT JSONEachRow`) and `telemetry.go` (source discovery)
- `internal/source/` — Telemetry → Source resolution, region derivation, disk
  cache (`~/.betterheap/cache/sources.json`, 1h TTL)
- `internal/routing/` — the tier-routing engine. `routing.Decide(...)` is a pure,
  unit-tested function; the live horizon probe is separate. This is the heart of
  the tool
- `internal/envelope/` — friendly-name → JSON-extraction SQL, one Profile per
  platform (`vercel`, `render`, `raw`)
- `internal/query/` — builds the cross-tier UNION SQL and the stats SQL
- `internal/config/` — `~/.betterheap/config.json` (0600) + the credential chain
- `internal/output/` — format sinks (json/ndjson/table/csv/pretty) + `--jq`

## Dev

```sh
go build -o betterheap ./cmd/betterheap
go build ./... && go test ./... && go vet ./...   # full green gate before committing
```

Install the pre-commit hook once per clone:

```sh
git config core.hooksPath .githooks
```

The hook runs `gofmt -l` on staged `.go` files and fails the commit if anything
is unformatted.

## Conventions

- **Committing directly on `main` is fine** for this repo — no feature branches.
- **stdout is data, stderr is diagnostics.** Query rows → stdout; warnings,
  `--explain` plans, progress → stderr (via `warn(...)`). Never mix the two — an
  agent piping `| jq` must see only rows.
- **Exit codes are part of the contract:** `0` ok · `1` query error · `2` auth
  error · `3` no results · `4` partial tier. Return `exitErr{code, err}` from a
  `RunE` to set them.
- `betterheap sql` is the escape hatch (dharma's `api` analogue). It expands
  `{logs}`/`{live}`/`{archive}` and envelope tokens (`{level}`, `{message}`, …)
  so even hand-written SQL stays tier- and envelope-aware.
- Default output is TTY-aware (pretty on a terminal, ndjson when piped) — don't
  hardcode a format.

## Credentials

Credential resolution: `--flag` → `BETTERHEAP_*` env → `BETTERSTACK_*` env →
`~/.betterheap/config.json` → bslog's `~/.bslog/env`. bslog users get zero setup;
the `~/.bslog/env` shim is read **without** sourcing it. (`~/.bslog/config.json` is
also read, but only for non-secret defaults — query host, default source, limit —
since bslog keeps secrets in its `env` file, not its JSON config.)

`auth login` is interactive (masked stdin), so an agent can't drive it — export
`BETTERSTACK_*` vars to run a live smoke test instead.

## Gotchas (live-validated — don't rediscover these)

- **Query creds are per-region.** A credential set authenticates only against its
  own region. Sources in other regions route correctly but return
  `AUTHENTICATION_FAILED` until given that region's creds. Each source is queryable
  only via its own `<region>-connect.betterstackdata.com`, so the CLI binds the
  query client to the resolved source's region. Multi-region cred config is the main M4 item.
- **Live and archive tables have different physical columns** (live: `bytes`/`json`;
  archive: `_row_type`). Any cross-tier UNION must project explicit shared columns
  (`dt, raw`) — never `SELECT *`.
- **Vercel nests everything under `$.vercel.*`.** `request_id` is
  `$.vercel.request_id` (NOT `$.request_id`); status is
  `$.vercel.proxy.status_code` / `$.vercel.statusCode`. `$.message_json.*` (module,
  logged context) exists only on app-logger lines, not HTTP access logs — trust
  `internal/envelope/envelope.go` over any platform doc.
- **Platform reports as `vercel_integration`, not `vercel`** — profile detection
  matches substrings, not exact strings.
- **The source cache is sticky (1h TTL).** After changing region/profile
  derivation, run `sources list --refresh` to bust stale derived data.
