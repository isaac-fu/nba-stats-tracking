# Docker setup

This folder prepares the database and development tools for the NBA stats tracking app. The current setup includes PostgreSQL, Python packages, dbt, and the Go toolchain. The Go health endpoint runs through the Go tool container. React has a starter project, but it does not have a Compose service yet.

Run every command below from the repository root: the directory containing `.env`, `database.sql`, and the `docker` folder. Keep Docker Desktop running with Linux containers enabled.

For the project overview and current progress, see the [root README](../README.md).

## What has been verified

- PostgreSQL started successfully and initialized the schema from `database.sql`.
- The Python dependency and database check connected to PostgreSQL and found all 14 relations checked by the script.
- `dbt debug` found valid configuration, Git, and a working PostgreSQL connection. All checks passed.
- The Go server implements `GET /health/live`; a request in Postman returned `200 OK`.

These checks establish the development environment. NBA data ingestion, dbt transformations, and statistics endpoints are the next implementation work.

## Files in this folder

| File | Purpose |
| --- | --- |
| `compose.yaml` | Defines services, environment variables, mounts, networking, and the PostgreSQL health check. |
| `python.Dockerfile` | Builds the Python tool image and installs its packages. |
| `python.requirements.txt` | Lists Python packages for fetching, parsing, database access, orchestration, and development checks. |
| `dbt.Dockerfile` | Builds the dbt image, installs Git and dbt packages, and copies the dbt configuration into the image. |
| `dbt.requirements.txt` | Pins the direct dbt dependencies: `dbt-core==1.12.5` and `dbt-postgres==1.11.0`. |
| `profiles.yml` | Tells dbt how to connect to PostgreSQL using environment variables. |
| `dbt_project.yml` | Defines the current dbt diagnostic project. Its model paths are empty; it does not transform data yet. |
| `check.py` | Imports Python dependencies and can check the PostgreSQL connection and expected relations. |

The older `dbt.yml` file is also present, but the Dockerfile copies `dbt_project.yml`. That is the configuration file used by this setup. `.yaml` and `.yml` both use YAML syntax; filenames referenced by the Dockerfile must still match exactly.

## Environment variables

Compose reads values from the root `.env` because the commands explicitly include `--env-file .env`. If you are setting up a new checkout, create `.env` from `.env.example` and replace the password placeholder. If you already have a working `.env`, keep using it.

| Variable | Purpose | Default if omitted |
| --- | --- | --- |
| `DATABASE_PASS` | PostgreSQL password passed to the database and tool containers. | Required; no default. |
| `DATABASE_NAME` | PostgreSQL database name. | `nba_stats` |
| `DATABASE_USER` | PostgreSQL user. | `nba_admin` |
| `DATABASE_PORT` | PostgreSQL port published on your computer. | `5432` |
| `DATABASE_SSLMODE` | Connection SSL mode for tools. | `disable` for this local setup. |
| `DBT_SCHEMA` | Default schema used by the dbt connection profile. | `build_dev` |
| `DBT_THREADS` | dbt worker thread count. | `2` |

This Compose expression:

```yaml
POSTGRES_PASSWORD: ${DATABASE_PASS:?Copy .env.example to .env and set DATABASE_PASS}
```

reads the password from `DATABASE_PASS`. The text after `:?` is an error message if the value is missing or empty. Put the password in `.env`, not directly in this YAML expression. `.env` is ignored by the repository's current `.gitignore`.

Inside the Python, dbt, and Go containers, Compose supplies `PGHOST=postgres` and `PGPORT=5432`, along with the database name, user, and password. Containers reach PostgreSQL by its service name on the Docker network. `DATABASE_PORT` controls access from your computer; it does not change PostgreSQL's internal port.

## Prepare the images together

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools pull --ignore-buildable
docker compose --env-file .env -f docker/compose.yaml --profile tools build
```

The first command downloads the PostgreSQL and Go images. The second builds the Python and dbt images, installing their requirements. Docker caches images and build steps, so these dependencies do not need to be reinstalled every day.

The Go image supplies the compiler and standard library. Application libraries are declared in `api/build/server/go.mod`; this setup does not yet build a dedicated API image that installs those libraries. React packages are declared in `web/build/client/package.json` and are not installed by these Compose commands.

## Start PostgreSQL

```text
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
```

`-d` runs the database in the background. `--wait` waits for the configured health check to pass.

PostgreSQL stores its data in the named volume `postgres_data`. The root `database.sql` is mounted as an initialization script. PostgreSQL runs that script when it initializes an empty data directory; restarting an existing database does not rerun it. Later schema changes will need a migration process.

## Check Python and the database

Check installed Python packages:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python /workspace/docker/check.py
```

Check those packages, the database connection, and the expected relations:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python /workspace/docker/check.py --database
```

A successful database check prints the database name, PostgreSQL version, and 14 `Database relation:` lines. The script is read-only: it does not create tables, load NBA data, or check Go code. Start PostgreSQL before running it; the Python service does not automatically start the database.

`check.py` also has an optional `--artifacts` mode for design-file validation. It currently expects an `openapi.yaml` at the repository root; that file is not present in this checkout. Use the dependency and database checks above for the current checkpoint.

## Check dbt

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm dbt debug
```

The dbt service waits for PostgreSQL to become healthy. A successful check ends with:

```text
All checks passed!
```

This checks dbt's configuration, required tools, and database connection. The diagnostic project has no models, so this result does not mean that NBA data transformations have been implemented.

The current Dockerfile includes Git and uses the dbt engine environment variables for configuration, logs, and temporary build artifacts. No local Python or dbt installation is needed for this workflow.

## Run the Go health endpoint

The Go source and module files are in `api/build/server`. Run the server with:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm -p 127.0.0.1:8080:8080 -w /workspace/api/build/server go go run .
```

The first `go` selects the Compose service; `go run .` is the command executed inside that container. This temporarily overrides the service's default `go version` command. `-w` selects the directory containing `main.go` and `go.mod`; `-p` maps your computer's port 8080 to the server's container port 8080.

Leave the terminal open. After compilation, the server prints `Starting API on port 8080`.

From a second terminal, send a request:

```text
curl.exe -i http://127.0.0.1:8080/health/live
```

Expect HTTP `200 OK`, a `Content-Type: application/json` header, and this body:

```json
{"status":"ok"}
```

You can also send a GET request to the same URL in Postman. The status, headers, and body appear in the response area. The Go terminal does not log individual requests yet. This endpoint checks that the HTTP server is alive; it does not check PostgreSQL.

Press Ctrl+C in the server terminal to stop the run. `--rm` removes that temporary container after it exits. Source edits are visible through the bind mount, but `go run .` does not automatically reload them; restart the command after saving changes.

## Why plain `up -d` does not start the whole app yet

Python, dbt, and Go are assigned to the `tools` profile. Without that profile, this starts PostgreSQL only:

```text
docker compose --env-file .env -f docker/compose.yaml up -d
```

You can build and start the configured services together:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools up -d --build
```

However, the tool services currently run commands that finish:

| Service | Default command | Expected behavior |
| --- | --- | --- |
| PostgreSQL | PostgreSQL server | Keeps running. |
| Python | `python --version` | Prints the version, then exits. |
| dbt | `dbt --version` | Prints the version, then exits. |
| Go | `go version` | Prints the version, then exits. |

`-d` runs a process in the background; it does not keep a finished process alive. A successful version command normally appears as `Exited (0)`. The earlier `run ... go go run .` override does not modify the Compose file. A future API service will have a persistent server command and its own port mapping.

## What `/workspace` means

The Python and Go services mount the repository root at `/workspace` inside their Linux containers. You do not need to create a Windows folder named `workspace`.

For example, `docker/check.py` on your computer becomes `/workspace/docker/check.py` in the container. The mount is read-only: edit source files in your code editor on Windows, then run them in Docker. Go's temporary build files and caches are stored separately inside the container.

The dbt image uses `/opt/dbt` and contains the configuration copied during its build. If you edit that copied configuration, rebuild the dbt image.

## Rebuild after changing dependencies

After changing Python requirements or its Dockerfile:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools build python
```

After changing dbt requirements, its Dockerfile, or its copied configuration:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools build dbt
```

Run the relevant checks again after rebuilding. Editing the Go source does not require rebuilding the Go tool image; rerun the server command.

## Inspect and stop services

Show running and stopped Compose containers:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools ps -a
```

Show recent database logs:

```text
docker compose --env-file .env -f docker/compose.yaml logs --tail 60 postgres
```

Stop PostgreSQL when you are finished:

```text
docker compose --env-file .env -f docker/compose.yaml stop postgres
```

Stopping the container preserves the database volume.

## Troubleshooting encountered during setup

| Symptom | What to check |
| --- | --- |
| Compose cannot find its configuration. | Run from the repository root and include `-f docker/compose.yaml`. |
| The build cannot find `dbt_project.yml`. | Ensure the file is in this Docker folder with that exact name. The build context is this folder. |
| PostgreSQL cannot bind host port 5432. | Choose an available host port, such as `DATABASE_PORT=5433`, in `.env`, then rerun the PostgreSQL startup command. Keep internal `PGPORT=5432`. |
| Python cannot resolve the `postgres` hostname. | Check `ps -a`, ensure PostgreSQL is running, and check that both containers are attached to the Compose project network. |
| `dbt debug` cannot find Git. | Rebuild the current dbt Dockerfile, which installs Git, then rerun `dbt debug`. |
| Tool containers exit immediately with code 0. | Their default version commands have completed successfully. Use `run` with the command you want to execute. |
| The health URL cannot be reached. | Keep the Go run terminal open and use its `-p 127.0.0.1:8080:8080` mapping. Check that host port 8080 is available. |

Docker's [Compose command reference](https://docs.docker.com/reference/cli/docker/compose/) and [profiles guide](https://docs.docker.com/compose/how-tos/profiles/) explain the command options used here.
