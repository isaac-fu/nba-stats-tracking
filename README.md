# NBA Stats Tracking

A portfolio project for comparing NBA players and teams using historical seasons and daily updates for completed games.

The planned data flow is:

```text
Python ingestion -> PostgreSQL raw data -> dbt transformations and tests
                 -> PostgreSQL analytics -> Go REST API -> React
                                                     -> optional Tableau
```

Basketball Reference is the planned source. The next data milestone is parsing a small local sample and loading it into PostgreSQL before adding source fetching.

## Progress so far

| Component | Current status |
| --- | --- |
| Docker | PostgreSQL and development tools for Python, dbt, and Go are configured. Python and dbt images have been built. |
| PostgreSQL | The database schema has been initialized from [database.sql](database.sql). The container passed its health check, and the Python database check found the expected relations. |
| dbt | `dbt debug` passed: configuration, Git, and PostgreSQL connectivity work. The current dbt project is for setup checks; transformation models are still to be written. |
| Go API | [main.go](api/build/server/main.go) serves `GET /health/live`. A request in Postman returned `200 OK`. This endpoint does not query the database. |
| React | A React/Vite starter exists in [web/build/client](web/build/client). It still contains the default starter interface and has not been connected to the API. |

Python ingestion, dbt transformations, analytics publication, NBA statistics endpoints, and the NBA frontend remain to be implemented. Existing database tables do not mean NBA data has been loaded.

## Repository layout

```text
nba-stats-tracking/
├── api/build/server/       Go module and initial HTTP server
├── contracts/             Reserved for API and metric specifications
├── database/              Reserved for database migrations and related files
├── dbt/                   Reserved for transformation models and tests
├── docker/                Compose, Dockerfiles, dependency lists, setup checks
├── docs/                  Reserved for detailed project documentation
├── ingestion/             Reserved for Python data ingestion
├── orchestration/         Reserved for pipeline scheduling
├── web/build/client/      React/Vite starter
├── .env.example           Example development settings
├── .env                   Your local settings and password
├── database.sql           Database schema used during initial setup
└── README.md              Project overview and quick start
```

The current dbt setup files are in `docker/`. The `dbt/` folder is where the actual models and tests will be added.

## Quick start

Install and start Docker Desktop with Linux containers enabled. Run the following commands from the repository root, which is currently:

```text
C:\Users\user\Documents\nba-stats-tracking
```

For a fresh setup, copy `.env.example` to `.env` and replace `DATABASE_PASS=change-me` with your own local password. If you already have `.env`, keep it. The example uses database `nba_stats`, user `nba_admin`, and host port `5433`.

### Prepare the Docker images

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools pull --ignore-buildable
docker compose --env-file .env -f docker/compose.yaml --profile tools build
```

Docker downloads the PostgreSQL and Go images and installs the Python and dbt dependencies while building their images. You do not need to rebuild everything every time you open the project.

### Start PostgreSQL

```text
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
```

See the [Docker guide](docker/README.md) for the database check, `dbt debug`, environment settings, and troubleshooting.

### Run the Go health endpoint

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm -p 127.0.0.1:8080:8080 -w /workspace/api/build/server go go run .
```

Leave this terminal running. In a second terminal, send a request:

```text
curl.exe -i http://127.0.0.1:8080/health/live
```

The expected result is HTTP `200 OK` with `Content-Type: application/json` and this body:

```json
{"status":"ok"}
```

You can also send a `GET` request to the same URL in Postman. Its response status, headers, and body appear in separate sections. The Go terminal currently prints the startup message; request logging has not been added.

Press `Ctrl+C` in the server terminal to stop the API. PostgreSQL continues running in the background.

## Next checkpoints

1. Parse a small local sample with Python and load it into raw tables.
2. Add dbt models and data tests to produce analytics tables.
3. Add a Go endpoint that reads real statistics from PostgreSQL.
4. Add source fetching and repeatable historical/daily updates.
5. Connect the React interface to the API; add Tableau comparisons if useful.

Update these instructions as each checkpoint becomes available. For container commands and configuration details, use [docker/README.md](docker/README.md).
