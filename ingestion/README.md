# Python ingestion checkpoint

This directory implements the first working data step: saved HTML → parsed raw strings → PostgreSQL raw tables. It also provides an explicitly enabled, conservative HTTP client for authorized source access. It imports season totals; it does not invent individual game records.

## Start with the synthetic sample

Preview the bundled HTML before loading anything. These commands do not connect
to PostgreSQL or make HTTP requests; PostgreSQL can be stopped for this preview:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli inspect --kind players --season 2024 --file /workspace/ingestion/fixtures/players_2024.html --source-name demo_fixture --url demo://season/2024/regular/players
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli inspect --kind teams --season 2024 --file /workspace/ingestion/fixtures/teams_2024.html --source-name demo_fixture --url demo://season/2024/regular/teams
```

The player preview reports four rows, two distinct players, two real team codes,
and one all-team row. `missing_metric_fields` reports `stl: 1` and `blk: 1`:
one player has intentionally unavailable steals and blocks. That is a successful
preview, not a parsing error. The team preview reports two rows and no missing
canonical metric values. `detected_tables` includes tables hidden in comments.

The preview also reports your chosen kind, ending year, phase, and source identity.
When HTML has no canonical URL, it explicitly says that phase comes from your
argument and has not been verified from the file. The preview neither imports
rows nor publishes a release. It also runs directly with standard-library Python
as `python -m ingestion.cli inspect ...`, because inspect loads no database/HTTP
packages. The source-name and URL arguments are optional for a generic preview.

Run commands from the repository root after PostgreSQL is healthy:

```text
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli sample
```

The command prints two JSON summaries. Expect `row_count: 4` for players and `row_count: 2` for teams. Snapshot and run IDs will differ on your computer.

The bundled HTML fixtures contain **invented names and statistics** for the 2024 season. They are labeled `source_name=demo_fixture`; this label is carried through publication and displayed by the frontend. The fixtures exercise:

- A player with an all-team `2TM` row and two team stints.
- A second player with blank steals/blocks, which must remain unknown.
- HTML tables hidden inside comments, repeated headers, and a leading rank column.
- Team win/loss records and opponent totals joined by team links.

`sample` never makes HTTP requests. Run it twice to observe two immutable raw revisions. Later publication chooses one revision per source page, so repeated observations do not double the displayed totals.

## What the Python code does

1. `cli.py` chooses a source and reads its HTML.
2. `store.py` records an ingest run, source page, accepted fetch observation, and compressed immutable body.
3. `parser.py` extracts stable player/team IDs from links and retains raw string values.
4. The loader commits the entire parse batch and all rows in one transaction.
5. dbt, in the next checkpoint, converts numbers, applies basketball rules, and tests the result.

The raw data lives in `raw.player_season_row` and `raw.team_season_totals_row`. Trace each row through `raw.parse_batch`, `raw.page_snapshot`, `raw.source_page`, and `meta.pipeline_run`.

An unsupported layout, duplicate entity, missing identity, absent totals column, empty table, or access challenge fails the import. A failed parse retains its nonempty body and failure metadata but creates no usable parse batch. Team games/wins/losses must be present; unavailable records are never filled with zero.

## Import an authorized saved page

Place your saved file somewhere inside the repository so Docker can see it through `/workspace`. For example, create `ingestion/local/players_2024.html`. This folder is ignored by Git; retain full HTML, including source links and `data-stat` attributes. Screenshots, PDFs, CSV exports, and copied table text are not inputs for this parser.

Preview it before importing:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli inspect --kind players --season 2024 --phase regular --file /workspace/ingestion/local/players_2024.html --source-name basketball_reference --url https://www.basketball-reference.com/leagues/NBA_2024_totals.html
```

Then import the reviewed saved page:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli import --kind players --season 2024 --phase regular --file /workspace/ingestion/local/players_2024.html --source-name basketball_reference --url https://www.basketball-reference.com/leagues/NBA_2024_totals.html
```

Repeat for an authorized team summary file using `--kind teams` and its canonical URL. The season is its ending year: `2024` means 2023–24. Both player and team source pages are needed for a coherent season release. Keep fixture releases and real-source releases separate.

Basketball Reference source URLs must match the declared kind, ending year, and
phase before a saved import opens PostgreSQL or writes metadata. If the saved
HTML contains a canonical URL, it must agree too. A missing canonical URL is
allowed but cannot establish the file's phase; the caller must supply the correct
label. A bare canonical filename or fragment likewise cannot verify phase;
filename year/kind contradictions still fail. Phase evidence requires a matching
absolute or root-relative source path containing the league/playoff folder.
Matching canonical metadata checks page identity, not source permission
or the truth of every statistic. Synthetic and generic provider HTML keeps its
existing parser behavior. Supplemental team win/loss and opponent rows must link
to the same season as the main totals table.

Player table IDs supported by this parser are `totals_stats`, `totals`, and `player-stats-base`. Team totals use `totals-team`, `team_stats`, `team-stats-base`, or `team_totals`. Team misc/opponent tables can supplement wins, losses, and opponent points. A source layout outside this explicit contract must be reviewed and added with a saved fixture and parser test; the parser does not guess.

## Authorized historical and daily fetching

The HTTP client is available, but source permission and current site access must be established before enabling it. `--allow-live` acknowledges that you have authorization; it cannot grant permission or bypass a block.

Historical refresh:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli fetch --start-season 2023 --end-season 2024 --phase regular --allow-live
```

Daily refresh of one season's aggregate totals:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli daily --season 2024 --phase regular --allow-live
```

Use `--phase playoffs` to request playoff season totals. `daily` is a repeatable command, not a scheduler; Dagster schedules can invoke it after the pipeline has been configured. A refresh reads aggregate totals from completed games represented by the source and does not provide live scores or game logs.

HTTP behavior:

- One client, sequential requests, at least 3.1 seconds between requests; a longer robots crawl delay is honored.
- Verify `robots.txt` before source requests; deny, inaccessible robots, malformed rules, unsupported wildcard patterns, or HTTP 403 stops fetching.
- At most three attempts for transient failures. Honor `Retry-After`; a wait beyond 120 seconds halts this run rather than retrying early.
- Stop on HTTP 401/403, challenge pages, unexpected content types, or redirects. No proxy, browser, or challenge bypass.
- Retain ETag/Last-Modified validators from the last good snapshot. HTTP 304 reuses that snapshot and parse batch; a new accepted body creates a fresh observation even if unchanged.
- A 20-second network timeout, 10-second connection timeout, and 8 MiB response limit bound individual requests.

Live source compatibility has not been established by the synthetic fixtures. Fetching halts on a changed source contract; old published analytics remain available until a new complete release passes its tests.

## Tests

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m pytest ingestion/tests -q
```

Parser and HTTP policy tests run offline. PostgreSQL integration tests are skipped unless `NBA_TEST_DATABASE_DSN` points to a **disposable database initialized with this repository's schema**. Those tests append test observations and check unchanged-body revisions, failed-parse atomicity, and HTTP 304 reuse; they must not run against your normal application database.

Library interfaces for the orchestrator are `ingestion.parser.parse_rows`, `ingestion.store.load_html`, and `ingestion.cli.load_sample`. Database callers use an autocommit psycopg connection; the ingestion functions manage their own short transactions. Connections use Docker's existing `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, and `PGPASSWORD` values. Credentials are never printed or stored in the source files.
