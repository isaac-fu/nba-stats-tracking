# Go API checkpoint

The server now reads published player and team season statistics from PostgreSQL.
Its live check is independent of the database; readiness requires both a database
connection and a published analytics release.

## Files

- `build/server/main.go`: connection pool, HTTP timeouts, startup and graceful shutdown.
- `build/server/handlers.go`: routes, validation and safe JSON errors.
- `build/server/store.go`: DTOs and parameterized PostgreSQL queries.
- `build/server/handlers_test.go`: handler tests that do not need PostgreSQL.
- `../contracts/openapi.yaml`: the implemented HTTP contract.

The existing `go.mod` and `go.sum` are preserved. This implementation uses Go's
standard `net/http` router and the already declared `pgx/v5` PostgreSQL package.

## Run through Docker

Run these commands from the project root. Start the pipeline first if you want
statistics; `/health/live` works before any data has been published.

```text
docker compose --env-file .env -f docker/compose.yaml up -d --build api
curl.exe -i http://127.0.0.1:8080/health/live
curl.exe -i http://127.0.0.1:8080/health/ready
curl.exe http://127.0.0.1:8080/api/v1/status
curl.exe http://127.0.0.1:8080/api/v1/seasons
curl.exe "http://127.0.0.1:8080/api/v1/players?season=2024&phase=regular&sort=pts_per_game"
curl.exe "http://127.0.0.1:8080/api/v1/teams?season=2024&phase=regular&sort=wins"
```

Use an ending year returned by `/api/v1/seasons`. A season ending in 2024 means
2023–24. Copy actual IDs from the lists when requesting comparisons:

```text
curl.exe "http://127.0.0.1:8080/api/v1/comparisons/players?season=2024&ids=FIRST_ID,SECOND_ID"
curl.exe "http://127.0.0.1:8080/api/v1/comparisons/teams?season=2024&ids=BOS:2024,LAL:2024"
```

The ID placeholders are not sample player identities. Use IDs actually returned
by the selected release. Comparisons accept 2–5 unique IDs and preserve order.
Every requested entity must have statistics in the selected season and phase.

## Parameters and responses

Lists require `season`; `phase` defaults to `regular` and also accepts `playoffs`.
`q` searches names (and team abbreviations), has a 100-character maximum and treats
SQL wildcard characters literally. `limit` defaults to 100 and accepts 1–1000.
There is no pagination cursor in this first implementation.

Allowed sorts: `pts_per_game`, `pts`, `reb_per_game`, `ast_per_game`, `ts_pct`,
`games_played`, `name`. Teams also accept `wins` and `win_pct`. Metrics sort highest
first with unavailable values last; names sort ascending. ID breaks ties.

Lists, seasons and comparisons return `{ "release_id": "...", "data": [...] }`.
Missing statistics are `null`; measured zeros are `0`. Percentages are fractions
(0.5 means 50%), computed from season totals rather than averaged game percentages.
Status identifies `demo_fixture` and `sample_data: true` for synthetic fixtures.

Invalid queries return 400, absent comparison entities return 404 and an absent
published release or unavailable database returns 503. Errors use
`application/problem+json`; database exceptions and credentials are never returned.
All implemented responses currently use `Cache-Control: no-store`.

Each statistics request opens a read-only repeatable-read transaction, selects
the published release once, then binds that release ID to its analytics queries.
SQL values are parameters; the only interpolated SQL is a server-owned sort map.

## Check the code

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm -w /workspace/api/build/server go go test ./...
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm -w /workspace/api/build/server go go vet ./...
```

For a native run, set the standard `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER` and
`PGPASSWORD` variables in your terminal, then run `go run .` in `api/build/server`.
`API_ADDR` defaults to `:8080`. Do not paste your password into documentation or Git.

The Docker MVP currently uses the development database credentials supplied by
Compose. Before deploying, provision a dedicated reader role with only SELECT on
analytics, `api.published_release` and `api.release_metadata`.
