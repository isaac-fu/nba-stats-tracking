# Run and understand the five checkpoints

Use PowerShell or Command Prompt from the repository root. Keep Docker Desktop running. Your existing `.env` supplies the PostgreSQL settings.

For a first sample run, `scripts/checkpoints.cmd` (or the equivalent PowerShell script) performs the commands below in order. Run them separately when you want to inspect each stage.

## 1. Parse HTML and load raw tables

Prepare all buildable images and start the database:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools build
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
```

Load the included synthetic sample:

For a preview before any database writes, use `ingestion.cli inspect` as shown in [the sample walkthrough](SAMPLE_WALKTHROUGH.md). It reports table IDs, row counts, distinct entities, and missing values from the local file.

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli sample
```

Expect four raw player rows and two raw team rows. The player rows include one all-team total, two team stints for that player, and one single-team player. They will become two player season records after dbt's selection rules.

Inspect the raw stage from the Python container:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -c "import psycopg; c=psycopg.connect(); print(c.execute('SELECT source_player_id,source_team_token,source_payload FROM raw.player_season_row ORDER BY row_ordinal LIMIT 10').fetchall()); c.close()"
```

The loader creates a pipeline run, source-page identity, compressed body observation, and complete parse batch. Raw statistics remain strings. Repeated imports create observations; they do not change old snapshots.

Read `ingestion/parser.py`, `ingestion/store.py`, and [ingestion/README.md](../ingestion/README.md) to follow the path from HTML to raw rows.

## 2. Clean, test, and publish analytics

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source demo_fixture
```

This command freezes the exact input revisions, creates a separate build schema, runs dbt, validates the output, and publishes all eight serving models in one transaction. It prints the published release UUID after success.

The sample build contains 11 models, one identity seed, and 53 data tests. The serving tables contain two players, two teams, three player/team stint records, and one season capability record. Alex Example has 50 total points across four games, so the result is 12.5 points per game. Adding the all-team row and the stints would produce the wrong result; the selection model prevents that.

Missing statistics remain NULL. Shooting percentages use total makes divided by total attempts. Season detail flags clearly state that game-level data and complete game coverage are unavailable in this slice.

Publication requires the exact build's artifacts and unchanged output tables. A failed test or changed candidate leaves the old published release available.

See [dbt/README.md](../dbt/README.md) and [orchestration/README.md](../orchestration/README.md) for the models and release lifecycle. Artifacts persist in the Docker `pipeline_artifacts` volume, under `/artifacts/<release UUID>` inside the pipeline container.

## 3. Request statistics from Go

Stop an earlier one-off Go health server with Ctrl+C if it is using host port 8080. Start the new persistent API service:

```text
docker compose --env-file .env -f docker/compose.yaml up -d api
curl.exe -i http://localhost:8080/health/ready
curl.exe "http://localhost:8080/api/v1/players?season=2024"
curl.exe "http://localhost:8080/api/v1/comparisons/players?season=2024&ids=example01,sample01"
```

Use GET for the same URLs in Postman. The list/comparison body has `release_id` and `data`. The sample's two players have points per game of 12.5 and 4.0. Unknown values, such as Bailey Sample's steals, are JSON null. An invalid filter returns 400; a missing requested comparison entity returns 404.

Go reads published analytics; it does not scrape or run dbt when a request arrives. Each request pins one release so all returned entities share the same data version.

See [the API README](../api/README.md) and [OpenAPI contract](../contracts/openapi.yaml).

## 4. Add an approved source and repeat updates

For authorized saved HTML pages placed in your repository, import a matching player totals page and a team totals page:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli import --kind players --season 2024 --phase regular --file /workspace/ingestion/local/players_2024.html --source-name basketball_reference --url https://www.basketball-reference.com/leagues/NBA_2024_totals.html
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli import --kind teams --season 2024 --phase regular --file /workspace/ingestion/local/teams_2024.html --source-name basketball_reference --url https://www.basketball-reference.com/leagues/NBA_2024.html
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source basketball_reference
```

The `ingestion/local` folder and those files are your own inputs; they are not bundled. Preview each file with `inspect` before importing it. Keep original canonical source URLs so a later correction refreshes the same logical page. Source URLs must agree with the requested kind, season, and phase; a conflicting HTML canonical link is rejected. Supplemental team records must also link to the requested year. Team imports must provide real wins/losses; the parser rejects unavailable required records rather than inventing them. If the HTML supplies no canonical link, matching the operator-provided URL does not prove the page's phase; inspect the saved page itself.

Only with approved live access, use PowerShell:

```powershell
.\scripts\refresh.ps1 -Mode historical -StartSeason 2023 -EndSeason 2024 -AllowLive
.\scripts\refresh.ps1 -Mode daily -Season 2027 -AllowLive
```

Season means the **ending year**: 2027 refers to 2026–27. The daily command refreshes season summaries for completed games; it does not deliver live scores or individual game logs. Run it again for another refresh. There is no automatic schedule yet.

The client checks robots rules, paces requests, uses conditional headers, and stops on blocks or challenges. A 304 response reuses the existing observation; an accepted identical body records a new observation. Live fetching returned 403 in this implementation environment, so actual source-page compatibility remains unverified. See [the ingestion guide](../ingestion/README.md) for aliases, failure handling, and access limitations.

## 5. Explore the data in React

```text
docker compose --env-file .env -f docker/compose.yaml up -d web
```

Open [http://localhost:5173](http://localhost:5173). Select Players or Teams, choose a season/phase, search or sort, check two entities, and choose **Compare selected**. The sample has two players and two teams. Missing metrics display a dash and the synthetic-data banner stays visible.

React sends same-origin `/api/v1` requests through Vite to Go; it never connects directly to PostgreSQL. If you publish another release, choose **Refresh data** to reload the source metadata and available statistics. API and web host ports can be changed with `API_PORT` and `WEB_PORT` in `.env`.

See [the web guide](../web/README.md). Optional Tableau comparisons are described in [TABLEAU.md](TABLEAU.md).

## After source changes

Rebuild the API image after Go changes and the web image after React changes:

```text
docker compose --env-file .env -f docker/compose.yaml up -d --build api web
```

Python and dbt source is mounted read-only from the repository; rerun their commands after saving changes. Rebuild the corresponding image after changing dependency requirements or its Dockerfile.
