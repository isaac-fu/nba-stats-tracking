# Follow one statistic through the sample app

The Docker application now serves the included synthetic sample. This exercise follows Alex Example's points from HTML to the comparison screen. It uses invented data and makes no source requests.

## 1. Preview the file before loading it

From the repository root, run:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm --no-deps python python -m ingestion.cli inspect --kind players --season 2024 --phase regular --file /workspace/ingestion/fixtures/players_2024.html --source-name demo_fixture --url demo://season/2024/regular/players
```

Then preview the teams:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm --no-deps python python -m ingestion.cli inspect --kind teams --season 2024 --phase regular --file /workspace/ingestion/fixtures/teams_2024.html --source-name demo_fixture --url demo://season/2024/regular/teams
```

`inspect` reads the file and parses its tables without contacting a website or PostgreSQL. Expect four player rows representing two distinct players, and two team rows. The player report should identify blank steals and blocks for one row; these are intentional unavailable values.

Start with [the player fixture](../ingestion/fixtures/players_2024.html) and [parser.py](../ingestion/parser.py). The HTML table is inside a comment, so the parser also processes comment-hidden tables. Player identity comes from the player link; statistic names come from `data-stat` attributes.

## 2. Understand the three rows for Alex

| Source team token | Games | Points | Meaning |
| --- | ---: | ---: | --- |
| `2TM` | 4 | 50 | Alex's total across two teams. |
| `BOS` | 2 | 25 | Alex's Boston stint. |
| `LAL` | 2 | 25 | Alex's Los Angeles stint. |

The loader in [store.py](../ingestion/store.py) preserves all three rows in `raw.player_season_row`. Values such as points stay as strings in `source_payload`. Keeping the source rows lets you investigate a calculation later.

The fixture's all-team total already includes both stints. Adding all three rows would incorrectly give 100 points. The player season model selects the `2TM` total; the player/team model keeps the stints separately.

## 3. Follow dbt's conversion and calculation

Read these two files in order:

1. [stg_player_season.sql](../dbt/models/staging/stg_player_season.sql) converts raw strings to typed columns. Empty strings become SQL NULL; 120 minutes becomes 7,200 seconds.
2. [mart_player_season.sql](../dbt/models/analytics/mart_player_season.sql) selects the appropriate totals and calculates rates through its macros.

Alex's points per game is `50 / 4 = 12.5`. His field-goal percentage is `20 / 40 = 0.5`. Bailey's zero three-point attempts produce an unavailable percentage, rather than a zero-percent shooting claim.

The pipeline in [pipeline.py](../orchestration/pipeline.py) freezes the input rows for a release, builds separate candidate tables, runs dbt tests, and publishes only after validation succeeds. The `release_id` in your API response identifies that published data version.

## 4. Follow Go and React

Use GET in Postman:

```text
http://localhost:8080/api/v1/players?season=2024
http://localhost:8080/api/v1/comparisons/players?season=2024&ids=example01,sample01
```

Read [store.go](../api/build/server/store.go) for the database queries and [handlers.go](../api/build/server/handlers.go) for request validation and JSON responses. Go reads the published analytics; an HTTP request does not rerun ingestion or dbt.

Open [the React app](http://localhost:5173), select both players, and choose **Compare selected**. [App.jsx](../web/build/client/src/App.jsx) requests the Go endpoints and formats their values for display.

| Value | Raw/source meaning | API JSON | React display |
| --- | --- | --- | --- |
| Alex points | All-team total is 50. | `"pts": 50` | Total points: 50 |
| Alex points per game | 50 points / 4 games. | `"pts_per_game": 12.5` | 12.5 |
| Alex field-goal percentage | 20 makes / 40 attempts. | `"fg_pct": 0.5` | 50.0% |
| Bailey three-point percentage | No attempts; division is undefined. | `"fg3_pct": null` | — |

## 5. Repeat a run and compare the result

When you want to practice updating data, rerun these two commands:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli sample
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source demo_fixture
```

Then choose **Refresh data** in React and request the players again in Postman. A successful publication has a new `release_id`, but still two player season records with the same totals. Raw observations accumulate for traceability; season totals are not added together across runs.

If parsing or a dbt test fails, correct the input or model and rerun that stage. The previous published release remains available while the new candidate is checked.

Once you can trace these values through all five stages, the next data checkpoint is a reviewed real-source import. The local preview command will help check those files before loading them. Daily scheduling and game-level statistics can follow after that source is validated.
