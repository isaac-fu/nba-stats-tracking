# NBA Stats Tracking

A portfolio app for comparing NBA players and teams across seasons. The first working pipeline imports season totals, keeps raw revisions, builds and tests analytics with dbt, publishes a release, and serves it through a Go API and React interface.

```text
HTML -> Python -> PostgreSQL raw -> dbt build/tests -> PostgreSQL analytics
                                                   -> Go REST API -> React
                                                                  -> optional Tableau
```

## Implemented checkpoints

| Checkpoint | What is available |
| --- | --- |
| Python ingestion | Local HTML previews, synthetic fixtures, saved-page imports with source identity checks, immutable observations, complete parse batches, and a fetching client. |
| dbt analytics | Typed staging, player/team season marts, reviewed team identities, nullable metrics, and 53 data tests. |
| Publication | Frozen inputs, separate build schemas, artifact and table checks, and an atomic published-release switch. |
| Go API | Health/status/seasons, player/team statistics, and comparisons of 2–5 entities. |
| React | Season and phase filters, search, player/team leaderboards, comparison tables, and source labels. |
| Updates | Repeatable historical and daily season-total refresh commands. Live fetching requires approved access. |

The included sample contains **invented players and totals**, not real NBA results. It has two players and two teams for season-ending 2024. The first slice supports season aggregates; game logs and full-season completeness are not implemented. Team identity seeds currently cover supported NBA codes from season-ending 2005 onward.

## Run all sample checkpoints

Keep Docker Desktop running with Linux containers enabled. Use your existing root `.env`; a fresh checkout can use `.env.example` with its password placeholder replaced. Do not overwrite a working `.env`.

From the repository root, run in either Command Prompt or PowerShell:

```text
scripts\checkpoints.cmd
```

An equivalent PowerShell script is also available:

```powershell
.\scripts\checkpoints.ps1
```

The script prepares dependencies, starts PostgreSQL, imports the sample, builds/tests/publishes it, and starts the API and React. If an earlier Go health-check container is still running on port 8080, stop its run terminal with Ctrl+C first.

Default application URLs:

- [React app](http://localhost:5173)
- [API status](http://localhost:8080/api/v1/status)
- [Player statistics](http://localhost:8080/api/v1/players?season=2024)

Optional `API_PORT` and `WEB_PORT` values in `.env` change the host ports. PostgreSQL still uses port 5432 inside Docker. The database volume and your existing initialization are preserved.

To learn what each command does and run checkpoints individually, follow [the checkpoint guide](docs/CHECKPOINTS.md). Trace one statistic from HTML to React in [the sample walkthrough](docs/SAMPLE_WALKTHROUGH.md). Container details are in [the Docker README](docker/README.md).

## Repository layout

| Folder/file | Purpose |
| --- | --- |
| `ingestion/` | Parsing, source fetching, raw loading, fixtures, and ingestion tests. |
| `dbt/` | The actual transformation project, models, identity seed, macros, and data tests. |
| `orchestration/` | Candidate preparation, dbt execution, and guarded publication. |
| `api/build/server/` | Go server and its tests/module files. |
| `web/build/client/` | React/Vite app. |
| `contracts/` | OpenAPI contract for the eight implemented routes. |
| `database/migrations/` | Additive migrations for the existing database. |
| `database.sql` | Original first-initialization schema; do not rerun it on an initialized database. |
| `docker/` | Compose, image recipes, dependency lists, and connection checks. |
| `scripts/` | Sample startup and approved-source refresh helpers. |
| `docs/` | Checkpoint explanations, data contract, verification notes, and optional Tableau queries. |

## Data rules

- Preserve source strings and every accepted observation in raw storage.
- Prefer a player's all-team row over their team stints; never add both together.
- Keep unavailable statistics NULL and show them as a dash in the interface.
- Compute shooting percentages from total makes and attempts.
- Keep synthetic and real sources in separate releases.
- Publish only a complete tested build; failures leave the last good release available.

For authorized saved HTML or live imports, see [ingestion instructions](ingestion/README.md). The live client honors access checks, pacing, conditional responses, and blocks. Automated requests to Basketball Reference returned 403 during this implementation, so live-page compatibility has not been established. Sports Reference describes permission requirements for apps based on scraped data in its [data-use policy](https://www.sports-reference.com/data_use.html).

Historical and daily refreshes are manual commands at this stage; a running daily scheduler is a later addition. Dagster packages are installed in the Python/pipeline environment, but no Dagster service or schedule is configured yet.

## Verification and next work

See [verification notes](docs/VERIFICATION.md) for checks completed and environment limits. Initial pipeline integration used an isolated PostgreSQL-compatible test runtime. The running Docker application was subsequently checked on the target machine: all eight API routes, invalid requests, and React player/team comparisons passed.

Next work: validate an approved real source export, expand identity coverage if needed, add game-level ingestion/models, configure scheduling, and prepare a production deployment. [Tableau setup](docs/TABLEAU.md) is optional.
