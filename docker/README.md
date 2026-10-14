# Docker development setup

Compose now includes a persistent PostgreSQL database, Go API, and React/Vite app, plus optional Python, dbt, pipeline, and Go tool containers. Run every command from the repository root with Docker Desktop running.

## Start the complete synthetic example

```text
scripts\checkpoints.cmd
```

The script builds dependencies, waits for PostgreSQL, imports the sample, runs dbt and publication, and starts the app. An equivalent `scripts/checkpoints.ps1` supports `-SkipBuild` after the images are prepared if no image inputs have changed. For individual commands and expected results, see [the checkpoint guide](../docs/CHECKPOINTS.md).

## Services

| Service | Profile | Default task |
| --- | --- | --- |
| `postgres` | Always enabled | PostgreSQL server, persistent named volume and health check. |
| `api` | Always enabled | Compiled Go HTTP server on container port 8080. |
| `web` | Always enabled | React/Vite development server on container port 5173; proxies API requests to Go. |
| `python` | `tools` | Prints the Python version; override it to run ingestion or checks. |
| `dbt` | `tools` | Prints the dbt version; uses the actual mounted `dbt/` project. |
| `pipeline` | `tools` | Prints CLI help; override it to prepare/build/test/publish a release. |
| `go` | `tools` | Prints the Go version; supports development commands with a source mount. |

After sample publication, start or rebuild the running application with:

```text
docker compose --env-file .env -f docker/compose.yaml up -d --build
```

`up -d` now starts PostgreSQL, API, and web because those services have persistent commands and no optional profile. `-d` runs them in the background. Tool containers exit normally after version/help commands; background mode does not keep a completed command alive.

## Prepare dependencies without starting the app

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools pull --ignore-buildable
docker compose --env-file .env -f docker/compose.yaml --profile tools build
```

Python and dbt requirements are installed during image builds. The pipeline image includes both environments so it can call Python and dbt in one run. The API image downloads Go modules and compiles the server; the web image runs `npm ci`. Docker caches images and build steps.

The Python requirement ranges and direct dbt pins remain the existing project choices. Node 24 is used for the web image. Application source and dependencies can be updated separately from the database volume.

## Files

| File | Purpose |
| --- | --- |
| `compose.yaml` | Services, profiles, environment variables, mounts, ports, and dependencies. |
| `python.Dockerfile`, `python.requirements.txt` | Python ingestion/development environment, including installed Dagster packages. |
| `dbt.Dockerfile`, `dbt.requirements.txt` | dbt tools and Git; direct dbt pins are core 1.12.5 and postgres adapter 1.11.0. |
| `pipeline.Dockerfile` | Python plus dbt for the release workflow; writable `/artifacts` directory. |
| `api.Dockerfile` | Builds a Go executable, then runs it as a non-root user. |
| `web.Dockerfile` | Installs React dependencies and runs the local Vite server. |
| `profiles.yml` | dbt's PostgreSQL connection profile, copied to `/opt/dbt`. |
| `dbt_project.yml` | Diagnostic fallback copied into the standalone dbt image. The actual models use `../dbt/dbt_project.yml` through the Compose mount. |
| `check.py` | Python dependency, PostgreSQL relation, and optional artifact checks. |

The older `dbt.yml` is not used by the Dockerfiles. `.yaml` and `.yml` both use YAML; referenced filenames must match exactly.

## Environment and ports

Keep your working root `.env`. `DATABASE_PASS` is required; defaults are database `nba_stats`, user `nba_admin`, host database port 5432, and local SSL mode `disable`. The existing example uses host database port 5433. Optional values are `API_PORT` (8080), `WEB_PORT` (5173), and `DBT_THREADS` (2).

`${DATABASE_PASS:?Copy .env.example to .env and set DATABASE_PASS}` reads the password from `.env`; the text after `:?` is an error if it is missing. The password belongs in `.env`, not directly in the YAML. Root `.dockerignore` excludes credentials from application build contexts.

Containers connect to `postgres:5432` through the Compose network. `DATABASE_PORT` only changes access from your computer. Vite uses `http://api:8080` internally, regardless of the host's `API_PORT`.

The existing shared PostgreSQL credentials are used for local development. Production role separation and deployment are later work; the Go handlers issue read-only transactions.

## Mounts and persistence

- Python, dbt, pipeline, and Go see the repository at `/workspace` through a read-only bind mount. Edit files in your Windows editor and rerun the tool command.
- dbt uses `/workspace/dbt` for transformations and `/opt/dbt/profiles.yml` for connection settings.
- The pipeline writes dbt artifacts to the named `pipeline_artifacts` volume under `/artifacts/<release UUID>`.
- PostgreSQL data persists in `postgres_data`. The original root `database.sql` runs only when initializing an empty data directory. Do not rerun it on an existing database.
- Pipeline startup applies additive migrations in `database/migrations/` to the existing schema. It does not reset the database.
- API and web source is copied into images. Rebuild these images after source edits; these services do not mount live host source.

## Useful checks

Start PostgreSQL and wait for its health check:

```text
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
```

Check Python dependencies and the original expected relations:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python /workspace/docker/check.py --database
```

Check dbt's configuration and connection:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm dbt debug
```

Run the first pipeline and API checks:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source demo_fixture
curl.exe -i http://localhost:8080/health/ready
curl.exe "http://localhost:8080/api/v1/players?season=2024"
```

The root OpenAPI design path formerly expected by `check.py --artifacts` is now updated to the implemented contract in `contracts/openapi.yaml`.

## Inspect, rebuild, and stop

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools ps -a
docker compose --env-file .env -f docker/compose.yaml logs --tail 60 postgres api web
docker compose --env-file .env -f docker/compose.yaml --profile tools build python dbt pipeline
docker compose --env-file .env -f docker/compose.yaml up -d --build api web
docker compose --env-file .env -f docker/compose.yaml stop api web postgres
```

Stopping services preserves database and pipeline artifact volumes. Dagster is installed as a dependency, but no Dagster UI, daemon, or daily schedule is started by this Compose file.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Compose cannot find its YAML. | Include `-f docker/compose.yaml` and run from the repository root. |
| A Docker build cannot find `dbt_project.yml`. | Keep the diagnostic configuration in this Docker folder; the Dockerfile copies that exact filename. |
| PostgreSQL cannot bind host port 5432. | Choose an available `DATABASE_PORT`, such as 5433. Keep internal `PGPORT=5432`. |
| The API cannot bind host port 8080. | Stop the earlier one-off Go server or choose an available `API_PORT`. |
| A tool cannot resolve `postgres`. | Check database health and both containers' Compose network membership. |
| `dbt debug` cannot find Git. | Rebuild the current dbt image, which includes Git. |
| Statistics return 503. | Import raw data and run the pipeline successfully before requesting statistics. `/health/live` can still return 200. |
| A source returns 403 or a challenge. | Fetching stops. Use an approved saved-page import or resolve access with the source provider. |
| A build/test fails. | Inspect pipeline output. The previous published release stays available. |

See [verification notes](../docs/VERIFICATION.md) for completed checks and runtime limits.
