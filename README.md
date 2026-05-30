# betterheap

An agent-friendly CLI for Better Stack logs that queries the **live hot buffer
and the S3 archive as one continuous window** — so the time range you ask for is
the time range you get, not a silent ~35-minute cap.

`bslog` is a nice surface but nearly useless past ~35 minutes: every friendly
command queries only the live `remote()` buffer. The real data — months of it —
lives in the S3 archive, reachable only by hand-writing ClickHouse SQL.
betterheap routes across both tiers automatically and understands the JSON log
envelope, so `level` / `message` / `module` are first-class.

> Name: "Better Stack" → "better heap" (a stack/heap pun).

## Status

**M1–M3 complete**, validated live against a real Better Stack team. Working:
auth/config, source discovery, the tier-aware routing engine, envelope profiles,
`logs`/`tail`/`errors`/`warnings`/`search`, `stats`, `trace`, `schema`, embedded
`gojq` (`--jq`), and the `sql` escape hatch with `{…}` template tokens. Not yet
built (M4): completion polish and per-region credential config for multi-region
querying.

## Install

Requires Go 1.26+.

```sh
go install github.com/jpaddison3/betterheap/cmd/betterheap@latest
# or, in this repo:
go build -o betterheap ./cmd/betterheap
```

## Auth

Two credentials, both created in the Better Stack dashboard (Integrations →
"Connect ClickHouse HTTP client" for the query user/pass; an API token for
source discovery):

```sh
betterheap auth login    # prompts, validates, stores in ~/.betterheap/config.json (0600)
betterheap auth status   # validates the resolved credentials
betterheap auth logout   # clears stored credentials
```

Credentials resolve in this order: `--flag` → `BETTERHEAP_*` env →
`BETTERSTACK_*` env → `~/.betterheap/config.json` → bslog's `~/.bslog/env`. So if
you already use bslog, betterheap works with zero re-setup. (`~/.bslog/config.json`
is also read, but only for non-secret defaults: query host, default source,
default limit.)

| Purpose            | Native env                                        | bslog fallback              |
|--------------------|---------------------------------------------------|-----------------------------|
| Source discovery   | `BETTERHEAP_API_TOKEN`                            | `BETTERSTACK_API_TOKEN`     |
| Query username     | `BETTERHEAP_QUERY_USERNAME`                       | `BETTERSTACK_QUERY_USERNAME`|
| Query password     | `BETTERHEAP_QUERY_PASSWORD`                       | `BETTERSTACK_QUERY_PASSWORD`|
| Region / host      | `BETTERHEAP_REGION` / `BETTERHEAP_QUERY_HOST`     | `BSLOG_QUERY_HOST`          |

## Usage

```sh
betterheap errors --since 7d                       # spans the archive automatically
betterheap logs --since 2026-05-28 --until 2026-05-29 --level error --module jobRecommender
betterheap logs --where status=500 --where env=production --since 24h
betterheap search "Failed to truncate" --since 14d # full-text across both tiers
betterheap tail -f                                 # follow the live buffer
betterheap stats --by module --level error --since 30d
betterheap trace <request_id> --since 1d           # one request across tiers/sources
betterheap errors --since 7d --jq '{mod:.module, msg:.message}'
betterheap sources list
betterheap sources show myapp                      # table ids, region, tier ranges
betterheap schema                                  # field map + exit codes (JSON)
betterheap sql "SELECT {level} AS lvl, count() FROM {logs} GROUP BY lvl" --since 7d
```

### Template tokens (sql)

`sql` expands `{logs}` (the tier-routed relation for `--since/--until`),
`{live}`/`{archive}` (bare table functions), and envelope tokens `{message}`,
`{level}`, `{module}`, `{env}`, `{status}`, `{request_id}`, `{path}`, `{dt}` for
the active source — so even hand-written SQL stays tier- and envelope-aware.
`--jq` runs an embedded jq program over each row (no external `jq` needed).

### Tier-aware time routing

For any time-bounded query betterheap:

1. Resolves the source to its `{live, archive}` ClickHouse table ids.
2. Probes the **buffer horizon** = `min(dt)` of the live table (cached ~30s).
3. Routes by where the `[since, until]` window falls:
   - **within the buffer** → live `remote()` only (fast path; default `tail`),
   - **older** → archive `s3Cluster()` only,
   - **straddling** → both tiers, partitioned at the horizon so the boundary
     never duplicates or drops rows.

Add `--explain` to see the generated SQL, the tier(s) hit, and the row count.
Override routing with `--tier live|archive|both` or `--live` (buffer-only).

**Archive guard:** archive-touching queries require a time bound (`--since` /
`--until`); pass `--all-time` to override.

### Envelope-aware fields

Better Stack stores everything in a JSON `raw` column. betterheap ships profiles
(`vercel`, `render`, `raw`) that map friendly names to JSON extraction, so:

- `--fields dt,level,message,module` actually populates,
- `--level error` is an **exact** match (no more substring over-match),
- `--module`, `--where status=500`, `--where env=production` all work.

The profile auto-detects from the source's platform; override with `--profile`.

## Output & exit codes

`--format json|ndjson|table|csv|pretty`. The default is TTY-aware: `pretty` on a
terminal, `ndjson` when piped (so `| jq` just works). Data goes to stdout,
diagnostics/warnings to stderr. `--fields` is ordered and deterministic.

| Exit | Meaning            |
|------|--------------------|
| 0    | ok                 |
| 1    | query error        |
| 2    | auth error         |
| 3    | no results         |
| 4    | partial tier (reserved) |

## Development

```sh
go build ./... && go test ./... && go vet ./...
git config core.hooksPath .githooks   # enable the gofmt pre-commit hook
```

## License

MIT — see [LICENSE](LICENSE).
