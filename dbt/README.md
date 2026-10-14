# Transforming frozen inputs with dbt

This project turns source season-summary strings into typed dimensions, player totals, team-stint totals, team totals, and capability metadata. It builds in a new `build_<release UUID without hyphens>` schema. It never writes directly into the published `analytics` tables.

Use the pipeline service from the repository root after loading the local sample:

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline python -m orchestration.pipeline run --source demo_fixture
```

The pipeline freezes raw input revisions, runs `dbt build` including seeds and data tests, then publishes the validated candidate. A successful run prints its release UUID. This command does not fetch data from a website.

## Models

- `stg_player_season` and `stg_team_season`: select only `meta.release_input` batches for the requested release; normalize numbers and minutes. Blank values become SQL NULL. Nonblank malformed numeric values fail a test.
- `stg_team_identity`: join source codes and season years to the explicit identity seed.
- `dim_season`, `dim_player`, `dim_franchise`, `dim_team_season`: dimensions matching the existing database DDL.
- `mart_player_season`: prefer one TOT/2TM/3TM row. Otherwise use a single-team row or sum stints once. If a component is missing from any summed row, the result remains NULL.
- `mart_player_team_season`: group real-team stints, excluding all-team rows.
- `mart_team_season`: source team summary totals and results.
- `release_capability`: explicitly marks game detail unavailable and completeness unverified. Synthetic sample releases remain labeled as samples.

All three marts calculate rates as ratios of totals. For example, field goal percentage is `SUM(fg) / SUM(fga)`, rather than an average of individual percentages. The publisher verifies equality with PostgreSQL generated serving columns.

## Identity seed coverage

`seeds/team_identity.csv` has an explicit supported range: NBA season-ending years 2005 onward. It covers 30 current franchise identities and the source-code transitions NJN/BRK, SEA/OKC, CHA/CHO, and NOH/NOK/NOP in that range. Franchise display names use their current names; team-season display names use source names when present. Future names/codes require a reviewed seed change. Earlier seasons require reviewed expansion before loading and publishing them.

Unmapped codes, ambiguous overlapping seed ranges, or unsupported seasons fail tests. There is no abbreviation-based franchise guess. The source team directory is the reference for reviewing changes: <https://www.basketball-reference.com/teams/>. Access to that page was blocked during development, so wider historical coverage is deliberately not included.

## Data tests

Tests cover composite keys, required identities, dimension relationships, phase values, numeric syntax, nonnegative values, shooting bounds, points/rebound arithmetic, starts/games bounds, wins plus losses equals games, one all-team row per player/season/phase, unique team summaries, agreement of known all-team and stint totals, and nonempty player/team data for each season/phase. Any failing or warning test prevents publication.

The model directory and macros require both `release_id` and `input_manifest_sha256` dbt variables. Use the orchestration CLI instead of running an untracked `dbt build` and trying to publish its tables afterward.
