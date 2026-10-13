-- NBA analytics backend: reference DDL, PostgreSQL 16+.
-- This file is schema scaffolding; it does not scrape, run dbt, or deploy an API.
-- Apply to a NEW development database with a migration owner; no credentials here.
-- UTC timestamptz stores instants; game_date is the source's basketball calendar date.
-- UUIDs are supplied by callers. No extension or superuser is required.
-- Re-running this initial migration is deliberately unsupported; use versioned migrations.
BEGIN;
CREATE SCHEMA meta;
CREATE SCHEMA raw;
CREATE SCHEMA analytics;
CREATE SCHEMA api;

CREATE TABLE meta.pipeline_run (
    run_id uuid PRIMARY KEY,
    job_kind text NOT NULL CHECK (job_kind IN ('ingest','parse','build','publish')),
    state text NOT NULL CHECK (state IN ('running','succeeded','failed','cancelled')),
    started_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    code_sha text NOT NULL,
    config_sha256 text NOT NULL CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE TABLE raw.source_page (
    page_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_name text NOT NULL DEFAULT 'basketball_reference',
    page_kind text NOT NULL CHECK (page_kind IN (
        'player_profile','team_season','schedule','box_score',
        'player_season_totals','team_season_totals')),
    canonical_url text NOT NULL UNIQUE,
    logical_key text NOT NULL,
    season_end_year integer,
    source_phase text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_name, page_kind, logical_key)
);

-- Every accepted fetch has a new immutable observation, even if its body repeats.
-- Do not deduplicate revisions by hash: an A -> B -> A correction must retain order.
CREATE TABLE raw.page_snapshot (
    page_snapshot_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    page_id bigint NOT NULL REFERENCES raw.source_page(page_id),
    ingest_run_id uuid NOT NULL REFERENCES meta.pipeline_run(run_id),
    fetched_at timestamptz NOT NULL,
    content_sha256 text NOT NULL CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    content_bytes bigint NOT NULL CHECK (content_bytes > 0),
    body_gzip bytea,
    immutable_archive_uri text,
    response_headers jsonb NOT NULL DEFAULT '{}'::jsonb,
    CHECK ((body_gzip IS NOT NULL) <> (immutable_archive_uri IS NOT NULL)),
    UNIQUE (page_id, page_snapshot_id)
);
CREATE INDEX snapshot_page_latest_idx
    ON raw.page_snapshot(page_id, fetched_at DESC, page_snapshot_id DESC);
CREATE INDEX snapshot_fetched_at_idx ON raw.page_snapshot(fetched_at);

-- Failed/blocked/rate-limited requests are observations, never usable snapshots.
CREATE TABLE meta.fetch_attempt (
    fetch_attempt_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ingest_run_id uuid NOT NULL REFERENCES meta.pipeline_run(run_id),
    page_id bigint NOT NULL REFERENCES raw.source_page(page_id),
    attempted_at timestamptz NOT NULL,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    http_status integer CHECK (http_status BETWEEN 100 AND 599),
    outcome text NOT NULL CHECK (outcome IN (
        'accepted','not_modified','retryable_error','blocked','parse_rejected','permanent_error')),
    page_snapshot_id bigint REFERENCES raw.page_snapshot(page_snapshot_id),
    error_code text,
    elapsed_ms integer CHECK (elapsed_ms >= 0),
    CHECK (outcome <> 'accepted' OR page_snapshot_id IS NOT NULL)
);
CREATE INDEX fetch_attempt_run_idx ON meta.fetch_attempt(ingest_run_id, attempted_at);
CREATE INDEX fetch_attempt_retention_idx ON meta.fetch_attempt(attempted_at);

-- Only successful complete parse batches enter raw; failures live in pipeline_run.
-- Write a batch and all of its rows in ONE transaction.
CREATE TABLE raw.parse_batch (
    parse_batch_id uuid PRIMARY KEY,
    page_snapshot_id bigint NOT NULL REFERENCES raw.page_snapshot(page_snapshot_id),
    parse_run_id uuid NOT NULL REFERENCES meta.pipeline_run(run_id),
    parser_version text NOT NULL,
    parser_code_sha text NOT NULL,
    raw_contract_version integer NOT NULL CHECK (raw_contract_version > 0),
    completed_at timestamptz NOT NULL,
    table_row_counts jsonb NOT NULL,
    UNIQUE (page_snapshot_id, parse_batch_id)
);
CREATE INDEX parse_batch_snapshot_idx
    ON raw.parse_batch(page_snapshot_id, completed_at DESC, parse_batch_id);

-- Raw identifying fields are extracted for routing; values in payload retain source
-- strings and unknown fields. dbt owns numeric coercion and canonical row semantics.
-- source_team_token can be TOT/2TM/3TM: these are NOT franchise/team IDs.

CREATE TABLE raw.player_profile_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    source_player_id text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.team_season_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    source_team_code text NOT NULL,
    season_end_year integer NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.game_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    source_game_id text NOT NULL,
    season_end_year integer NOT NULL,
    source_phase text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.player_game_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    source_game_id text NOT NULL,
    source_player_id text NOT NULL,
    source_team_code text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.team_game_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    source_game_id text NOT NULL,
    source_team_code text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.player_season_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    season_end_year integer NOT NULL,
    source_phase text NOT NULL,
    source_player_id text NOT NULL,
    source_team_token text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE raw.team_season_totals_row (
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    row_ordinal integer NOT NULL CHECK (row_ordinal > 0),
    season_end_year integer NOT NULL,
    source_phase text NOT NULL,
    source_team_code text NOT NULL,
    source_payload jsonb NOT NULL CHECK (jsonb_typeof(source_payload) = 'object'),
    PRIMARY KEY (parse_batch_id, source_table_id, row_ordinal)
);


CREATE TABLE meta.release (
    release_id uuid PRIMARY KEY,
    build_run_id uuid NOT NULL REFERENCES meta.pipeline_run(run_id),
    status text NOT NULL CHECK (status IN ('candidate','validated','published','retired','failed')),
    source_cutoff timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    sealed_at timestamptz,
    first_published_at timestamptz,
    code_sha text NOT NULL,
    dbt_version text NOT NULL,
    dbt_adapter_version text NOT NULL,
    metric_contract_version text NOT NULL,
    seed_manifest_sha256 text NOT NULL CHECK (seed_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    input_manifest_sha256 text CHECK (input_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    dbt_manifest_uri text,
    dbt_run_results_uri text,
    quality_report_uri text,
    previous_release_id uuid REFERENCES meta.release(release_id),
    CHECK (status NOT IN ('validated','published','retired') OR sealed_at IS NOT NULL),
    CHECK (status NOT IN ('published','retired') OR first_published_at IS NOT NULL)
);
CREATE INDEX release_retention_idx ON meta.release(status, first_published_at, created_at);

-- Freeze exactly ONE selected parse revision for each logical source page.
-- Selection happens at build start, using accepted observations fetched <= cutoff.
-- dbt reads this table by release_id; it must not query unconstrained "latest raw".
CREATE TABLE meta.release_input (
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    page_id bigint NOT NULL REFERENCES raw.source_page(page_id),
    page_snapshot_id bigint NOT NULL,
    parse_batch_id uuid NOT NULL,
    PRIMARY KEY (release_id, page_id),
    FOREIGN KEY (page_id, page_snapshot_id)
        REFERENCES raw.page_snapshot(page_id, page_snapshot_id),
    FOREIGN KEY (page_snapshot_id, parse_batch_id)
        REFERENCES raw.parse_batch(page_snapshot_id, parse_batch_id)
);
CREATE INDEX release_input_snapshot_idx ON meta.release_input(page_snapshot_id);
CREATE INDEX release_input_parse_idx ON meta.release_input(parse_batch_id);

CREATE TABLE meta.quality_issue (
    quality_issue_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    model_name text NOT NULL,
    test_name text NOT NULL,
    severity text NOT NULL CHECK (severity IN ('error','warn')),
    affected_key jsonb NOT NULL,
    details jsonb NOT NULL,
    detected_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX quality_issue_release_idx ON meta.quality_issue(release_id, severity);

CREATE TABLE analytics.dim_season (
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    season_end_year integer NOT NULL CHECK (season_end_year >= 1947),
    season_label text NOT NULL,
    starts_on date,
    ends_on date,
    PRIMARY KEY (release_id, season_end_year),
    CHECK (ends_on IS NULL OR starts_on IS NULL OR ends_on >= starts_on)
);

CREATE TABLE analytics.dim_player (
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    player_id text NOT NULL,
    display_name text NOT NULL,
    search_name text NOT NULL,
    born_on date,
    height_cm numeric CHECK (height_cm > 0),
    weight_kg numeric CHECK (weight_kg > 0),
    position_source text,
    PRIMARY KEY (release_id, player_id)
);
CREATE INDEX player_search_idx ON analytics.dim_player(release_id, search_name, player_id);

-- Franchise lineage is a reviewed versioned seed, not a guess from abbreviations.
CREATE TABLE analytics.dim_franchise (
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    franchise_id text NOT NULL,
    display_name text NOT NULL,
    PRIMARY KEY (release_id, franchise_id)
);

CREATE TABLE analytics.dim_team_season (
    release_id uuid NOT NULL,
    team_season_id text NOT NULL, -- e.g. LAL:2024
    season_end_year integer NOT NULL,
    franchise_id text NOT NULL,
    source_team_code text NOT NULL,
    display_name text NOT NULL,
    abbreviation text NOT NULL,
    conference text,
    division text,
    PRIMARY KEY (release_id, team_season_id),
    UNIQUE (release_id, team_season_id, season_end_year),
    UNIQUE (release_id, season_end_year, source_team_code),
    FOREIGN KEY (release_id, season_end_year)
        REFERENCES analytics.dim_season(release_id, season_end_year),
    FOREIGN KEY (release_id, franchise_id)
        REFERENCES analytics.dim_franchise(release_id, franchise_id),
    CHECK (team_season_id = source_team_code || ':' || season_end_year::text)
);
CREATE INDEX team_season_franchise_idx
    ON analytics.dim_team_season(release_id, franchise_id, season_end_year);

CREATE TABLE analytics.fact_game (
    release_id uuid NOT NULL,
    game_id text NOT NULL, -- Basketball Reference box-score filename without .html
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    game_date date NOT NULL,
    home_team_season_id text NOT NULL,
    away_team_season_id text NOT NULL,
    home_pts integer NOT NULL CHECK (home_pts >= 0),
    away_pts integer NOT NULL CHECK (away_pts >= 0),
    overtime_periods integer CHECK (overtime_periods >= 0),
    status text NOT NULL DEFAULT 'final' CHECK (status = 'final'),
    PRIMARY KEY (release_id, game_id),
    UNIQUE (release_id, game_id, season_end_year, phase),
    FOREIGN KEY (release_id, home_team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    FOREIGN KEY (release_id, away_team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    CHECK (home_team_season_id <> away_team_season_id),
    CHECK (home_pts <> away_pts)
);
CREATE INDEX game_season_date_idx
    ON analytics.fact_game(release_id, season_end_year, phase, game_date, game_id);

CREATE TABLE analytics.fact_team_game (
    release_id uuid NOT NULL,
    game_id text NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    team_season_id text NOT NULL,
    opponent_team_season_id text NOT NULL,
    is_home boolean NOT NULL,
    won boolean NOT NULL,
    minutes_seconds integer CHECK (minutes_seconds >= 0),
    fg integer CHECK (fg >= 0),
    fga integer CHECK (fga >= 0),
    fg3 integer CHECK (fg3 >= 0),
    fg3a integer CHECK (fg3a >= 0),
    ft integer CHECK (ft >= 0),
    fta integer CHECK (fta >= 0),
    orb integer CHECK (orb >= 0),
    drb integer CHECK (drb >= 0),
    reb integer CHECK (reb >= 0),
    ast integer CHECK (ast >= 0),
    stl integer CHECK (stl >= 0),
    blk integer CHECK (blk >= 0),
    tov integer CHECK (tov >= 0),
    pf integer CHECK (pf >= 0),
    pts integer CHECK (pts >= 0),
    opponent_pts integer NOT NULL CHECK (opponent_pts >= 0),
    PRIMARY KEY (release_id, game_id, team_season_id),
    FOREIGN KEY (release_id, game_id, season_end_year, phase)
        REFERENCES analytics.fact_game(release_id, game_id, season_end_year, phase),
    FOREIGN KEY (release_id, team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    FOREIGN KEY (release_id, opponent_team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    CHECK (team_season_id <> opponent_team_season_id),
    CHECK (pts IS NOT NULL),
    CHECK (fg <= fga),
    CHECK (fg3 <= fg),
    CHECK (fg3a <= fga),
    CHECK (fg3 <= fg3a),
    CHECK (ft <= fta),
    CHECK (orb + drb = reb)
);
CREATE INDEX team_game_history_idx
    ON analytics.fact_team_game(release_id, team_season_id, phase, game_id);

CREATE TABLE analytics.fact_player_game (
    release_id uuid NOT NULL,
    game_id text NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    player_id text NOT NULL,
    team_season_id text NOT NULL,
    participation_status text NOT NULL CHECK (participation_status IN (
        'played','did_not_play','inactive','did_not_dress','not_with_team','unknown')),
    started boolean,
    minutes_seconds integer CHECK (minutes_seconds >= 0),
    fg integer CHECK (fg >= 0),
    fga integer CHECK (fga >= 0),
    fg3 integer CHECK (fg3 >= 0),
    fg3a integer CHECK (fg3a >= 0),
    ft integer CHECK (ft >= 0),
    fta integer CHECK (fta >= 0),
    orb integer CHECK (orb >= 0),
    drb integer CHECK (drb >= 0),
    reb integer CHECK (reb >= 0),
    ast integer CHECK (ast >= 0),
    stl integer CHECK (stl >= 0),
    blk integer CHECK (blk >= 0),
    tov integer CHECK (tov >= 0),
    pf integer CHECK (pf >= 0),
    pts integer CHECK (pts >= 0),
    PRIMARY KEY (release_id, game_id, player_id),
    FOREIGN KEY (release_id, game_id, season_end_year, phase)
        REFERENCES analytics.fact_game(release_id, game_id, season_end_year, phase),
    FOREIGN KEY (release_id, game_id, team_season_id)
        REFERENCES analytics.fact_team_game(release_id, game_id, team_season_id),
    FOREIGN KEY (release_id, player_id)
        REFERENCES analytics.dim_player(release_id, player_id),
    CHECK (participation_status = 'played' OR (
        minutes_seconds IS NULL AND started IS NULL
        AND fg IS NULL AND fga IS NULL AND fg3 IS NULL AND fg3a IS NULL
        AND ft IS NULL AND fta IS NULL AND orb IS NULL AND drb IS NULL
        AND reb IS NULL AND ast IS NULL AND stl IS NULL AND blk IS NULL
        AND tov IS NULL AND pf IS NULL AND pts IS NULL)),
    CHECK (fg <= fga),
    CHECK (fg3 <= fg),
    CHECK (fg3a <= fga),
    CHECK (fg3 <= fg3a),
    CHECK (ft <= fta),
    CHECK (orb + drb = reb)
);
CREATE INDEX player_game_history_idx
    ON analytics.fact_player_game(release_id, player_id, season_end_year, phase, game_id);
CREATE INDEX player_game_team_idx
    ON analytics.fact_player_game(release_id, team_season_id, phase, game_id);

-- Preserve the source's distinct all-teams rows and individual team stints.
-- Multiple stints remain distinct by exact selected source table and row ordinal.
CREATE TABLE analytics.source_player_season_stint (
    release_id uuid NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    player_id text NOT NULL,
    source_row_ordinal integer NOT NULL CHECK (source_row_ordinal > 0),
    row_scope text NOT NULL CHECK (row_scope IN ('all_teams','team_stint')),
    source_team_token text NOT NULL,
    team_season_id text,
    games_played integer NOT NULL CHECK (games_played >= 0),
    games_started integer CHECK (games_started >= 0 AND games_started <= games_played),
    minutes_seconds bigint CHECK (minutes_seconds >= 0),
    fg integer CHECK (fg >= 0),
    fga integer CHECK (fga >= 0),
    fg3 integer CHECK (fg3 >= 0),
    fg3a integer CHECK (fg3a >= 0),
    ft integer CHECK (ft >= 0),
    fta integer CHECK (fta >= 0),
    orb integer CHECK (orb >= 0),
    drb integer CHECK (drb >= 0),
    reb integer CHECK (reb >= 0),
    ast integer CHECK (ast >= 0),
    stl integer CHECK (stl >= 0),
    blk integer CHECK (blk >= 0),
    tov integer CHECK (tov >= 0),
    pf integer CHECK (pf >= 0),
    pts integer CHECK (pts >= 0),
    parse_batch_id uuid NOT NULL REFERENCES raw.parse_batch(parse_batch_id),
    source_table_id text NOT NULL,
    PRIMARY KEY (release_id, season_end_year, phase, player_id,
        parse_batch_id, source_table_id, source_row_ordinal),
    FOREIGN KEY (release_id, season_end_year)
        REFERENCES analytics.dim_season(release_id, season_end_year),
    FOREIGN KEY (release_id, player_id)
        REFERENCES analytics.dim_player(release_id, player_id),
    FOREIGN KEY (release_id, team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    CHECK ((row_scope = 'all_teams' AND team_season_id IS NULL)
        OR (row_scope = 'team_stint' AND team_season_id IS NOT NULL)),
    CHECK (fg <= fga),
    CHECK (fg3 <= fg),
    CHECK (fg3a <= fga),
    CHECK (fg3 <= fg3a),
    CHECK (ft <= fta),
    CHECK (orb + drb = reb)
);
CREATE UNIQUE INDEX source_one_all_teams_idx
    ON analytics.source_player_season_stint(release_id, season_end_year, phase, player_id)
    WHERE row_scope = 'all_teams';
CREATE INDEX source_stint_parse_idx
    ON analytics.source_player_season_stint(parse_batch_id);

-- Choose exactly ONE basis per player/season/phase. Never union an all-teams source
-- aggregate with its team stints. Complete game coverage is required for game_totals.
CREATE TABLE analytics.mart_player_season (
    release_id uuid NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    player_id text NOT NULL,
    metrics_basis text NOT NULL CHECK (metrics_basis IN ('game_totals','source_totals')),
    source_scope text NOT NULL CHECK (source_scope IN (
        'completed_games','source_all_teams','source_single_team','summed_source_stints')),
    games_played integer NOT NULL CHECK (games_played >= 0),
    games_started integer CHECK (games_started >= 0 AND games_started <= games_played),
    minutes_seconds bigint CHECK (minutes_seconds >= 0),
    fg integer CHECK (fg >= 0),
    fga integer CHECK (fga >= 0),
    fg3 integer CHECK (fg3 >= 0),
    fg3a integer CHECK (fg3a >= 0),
    ft integer CHECK (ft >= 0),
    fta integer CHECK (fta >= 0),
    orb integer CHECK (orb >= 0),
    drb integer CHECK (drb >= 0),
    reb integer CHECK (reb >= 0),
    ast integer CHECK (ast >= 0),
    stl integer CHECK (stl >= 0),
    blk integer CHECK (blk >= 0),
    tov integer CHECK (tov >= 0),
    pf integer CHECK (pf >= 0),
    pts integer CHECK (pts >= 0),
    pts_per_game numeric GENERATED ALWAYS AS (pts::numeric / NULLIF(games_played, 0)) STORED,
    reb_per_game numeric GENERATED ALWAYS AS (reb::numeric / NULLIF(games_played, 0)) STORED,
    ast_per_game numeric GENERATED ALWAYS AS (ast::numeric / NULLIF(games_played, 0)) STORED,
    pts_per36 numeric GENERATED ALWAYS AS (pts::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    reb_per36 numeric GENERATED ALWAYS AS (reb::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    ast_per36 numeric GENERATED ALWAYS AS (ast::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    fg_pct numeric GENERATED ALWAYS AS (fg::numeric / NULLIF(fga, 0)) STORED,
    fg3_pct numeric GENERATED ALWAYS AS (fg3::numeric / NULLIF(fg3a, 0)) STORED,
    ft_pct numeric GENERATED ALWAYS AS (ft::numeric / NULLIF(fta, 0)) STORED,
    efg_pct numeric GENERATED ALWAYS AS ((fg::numeric + 0.5 * fg3) / NULLIF(fga, 0)) STORED,
    ts_pct numeric GENERATED ALWAYS AS (pts::numeric / NULLIF(2 * (fga::numeric + 0.44 * fta), 0)) STORED,
    PRIMARY KEY (release_id, season_end_year, phase, player_id),
    FOREIGN KEY (release_id, season_end_year)
        REFERENCES analytics.dim_season(release_id, season_end_year),
    FOREIGN KEY (release_id, player_id)
        REFERENCES analytics.dim_player(release_id, player_id),
    CHECK ((metrics_basis = 'game_totals' AND source_scope = 'completed_games')
        OR (metrics_basis = 'source_totals' AND source_scope <> 'completed_games')),
    CHECK (fg <= fga),
    CHECK (fg3 <= fg),
    CHECK (fg3a <= fga),
    CHECK (fg3 <= fg3a),
    CHECK (ft <= fta),
    CHECK (orb + drb = reb)
);
CREATE INDEX player_season_lookup_idx
    ON analytics.mart_player_season(release_id, player_id, season_end_year, phase);
CREATE INDEX player_season_pts_leaderboard_idx
    ON analytics.mart_player_season(release_id, season_end_year, phase, pts DESC, player_id)
    WHERE pts IS NOT NULL;

-- Team-scoped player totals do not get joined to league totals during aggregation.
CREATE TABLE analytics.mart_player_team_season (
    LIKE analytics.mart_player_season INCLUDING GENERATED INCLUDING CONSTRAINTS,
    team_season_id text NOT NULL,
    PRIMARY KEY (release_id, season_end_year, phase, player_id, team_season_id),
    CHECK (source_scope IN ('completed_games','source_single_team','summed_source_stints')),
    FOREIGN KEY (release_id, player_id)
        REFERENCES analytics.dim_player(release_id, player_id),
    FOREIGN KEY (release_id, team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year)
);
CREATE INDEX player_team_season_lookup_idx
    ON analytics.mart_player_team_season(release_id, team_season_id, phase, player_id);

CREATE TABLE analytics.mart_team_season (
    release_id uuid NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    team_season_id text NOT NULL,
    metrics_basis text NOT NULL CHECK (metrics_basis IN ('game_totals','source_totals')),
    games_played integer NOT NULL CHECK (games_played >= 0),
    wins integer NOT NULL CHECK (wins >= 0),
    losses integer NOT NULL CHECK (losses >= 0),
    minutes_seconds bigint CHECK (minutes_seconds >= 0),
    fg integer CHECK (fg >= 0),
    fga integer CHECK (fga >= 0),
    fg3 integer CHECK (fg3 >= 0),
    fg3a integer CHECK (fg3a >= 0),
    ft integer CHECK (ft >= 0),
    fta integer CHECK (fta >= 0),
    orb integer CHECK (orb >= 0),
    drb integer CHECK (drb >= 0),
    reb integer CHECK (reb >= 0),
    ast integer CHECK (ast >= 0),
    stl integer CHECK (stl >= 0),
    blk integer CHECK (blk >= 0),
    tov integer CHECK (tov >= 0),
    pf integer CHECK (pf >= 0),
    pts integer CHECK (pts >= 0),
    opponent_pts integer CHECK (opponent_pts >= 0),
    pts_per_game numeric GENERATED ALWAYS AS (pts::numeric / NULLIF(games_played, 0)) STORED,
    reb_per_game numeric GENERATED ALWAYS AS (reb::numeric / NULLIF(games_played, 0)) STORED,
    ast_per_game numeric GENERATED ALWAYS AS (ast::numeric / NULLIF(games_played, 0)) STORED,
    pts_per36 numeric GENERATED ALWAYS AS (pts::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    reb_per36 numeric GENERATED ALWAYS AS (reb::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    ast_per36 numeric GENERATED ALWAYS AS (ast::numeric * 2160 / NULLIF(minutes_seconds, 0)) STORED,
    fg_pct numeric GENERATED ALWAYS AS (fg::numeric / NULLIF(fga, 0)) STORED,
    fg3_pct numeric GENERATED ALWAYS AS (fg3::numeric / NULLIF(fg3a, 0)) STORED,
    ft_pct numeric GENERATED ALWAYS AS (ft::numeric / NULLIF(fta, 0)) STORED,
    efg_pct numeric GENERATED ALWAYS AS ((fg::numeric + 0.5 * fg3) / NULLIF(fga, 0)) STORED,
    ts_pct numeric GENERATED ALWAYS AS (pts::numeric / NULLIF(2 * (fga::numeric + 0.44 * fta), 0)) STORED,
    win_pct numeric GENERATED ALWAYS AS (wins::numeric / NULLIF(games_played, 0)) STORED,
    points_for_per_game numeric GENERATED ALWAYS AS (pts::numeric / NULLIF(games_played, 0)) STORED,
    points_against_per_game numeric GENERATED ALWAYS AS (opponent_pts::numeric / NULLIF(games_played, 0)) STORED,
    point_differential_per_game numeric GENERATED ALWAYS AS (
        (pts::numeric - opponent_pts) / NULLIF(games_played, 0)) STORED,
    PRIMARY KEY (release_id, season_end_year, phase, team_season_id),
    FOREIGN KEY (release_id, team_season_id, season_end_year)
        REFERENCES analytics.dim_team_season(release_id, team_season_id, season_end_year),
    CHECK (wins + losses = games_played),
    CHECK (fg <= fga),
    CHECK (fg3 <= fg),
    CHECK (fg3a <= fga),
    CHECK (fg3 <= fg3a),
    CHECK (ft <= fta),
    CHECK (orb + drb = reb)
);

-- Availability and completeness are distinct. Aggregate-only historical seasons
-- still support comparisons without pretending to have box scores/game logs.
CREATE TABLE analytics.release_capability (
    release_id uuid NOT NULL,
    season_end_year integer NOT NULL,
    phase text NOT NULL CHECK (phase IN ('regular','playoffs')),
    player_season_totals_available boolean NOT NULL,
    team_season_totals_available boolean NOT NULL,
    game_detail_available boolean NOT NULL,
    game_detail_complete boolean NOT NULL,
    observed_games integer NOT NULL CHECK (observed_games >= 0),
    expected_games integer CHECK (expected_games >= 0),
    coverage_starts_on date,
    coverage_through_date date,
    missing_fields text[] NOT NULL DEFAULT ARRAY[]::text[],
    coverage_notes text,
    PRIMARY KEY (release_id, season_end_year, phase),
    FOREIGN KEY (release_id, season_end_year)
        REFERENCES analytics.dim_season(release_id, season_end_year),
    CHECK (NOT game_detail_complete OR game_detail_available),
    CHECK (NOT game_detail_complete OR expected_games IS NOT NULL),
    CHECK (NOT game_detail_complete OR observed_games = expected_games),
    CHECK (expected_games IS NULL OR observed_games <= expected_games),
    CHECK (game_detail_available OR observed_games = 0),
    CHECK (coverage_starts_on IS NULL OR coverage_through_date IS NULL
        OR coverage_starts_on <= coverage_through_date)
);

-- The boolean CHECK permits at most ONE published pointer; no row means bootstrap.
CREATE TABLE api.published_release (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    published_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE meta.publication_event (
    publication_event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    release_id uuid NOT NULL REFERENCES meta.release(release_id),
    previous_release_id uuid REFERENCES meta.release(release_id),
    published_at timestamptz NOT NULL DEFAULT now(),
    actor text NOT NULL,
    reason text NOT NULL
);
CREATE INDEX publication_event_release_idx
    ON meta.publication_event(release_id, published_at DESC);

-- View ownership permits narrow lineage metadata reads without granting the API
-- direct raw/meta access. Unpublished candidate releases are not externally visible.
-- source_observed_at is the newest selected input observation, not a coverage date.
CREATE VIEW api.release_metadata AS
    SELECT r.release_id, r.status, r.first_published_at, r.source_cutoff,
           r.metric_contract_version,
           r.first_published_at AS published_at,
           r.metric_contract_version AS metrics_definition_version,
           observations.source_observed_at
    FROM meta.release r
    LEFT JOIN LATERAL (
        SELECT max(s.fetched_at) AS source_observed_at
        FROM meta.release_input i
        JOIN raw.page_snapshot s ON s.page_snapshot_id = i.page_snapshot_id
        WHERE i.release_id = r.release_id
    ) observations ON true
    WHERE r.first_published_at IS NOT NULL;

-- Current-release views are convenient for one-statement Tableau reads.
-- Go API handlers use analytics with a pinned release_id; do not read several
-- current_* views in separate READ COMMITTED statements and assume one release.
CREATE VIEW api.current_player_season AS
    SELECT m.* FROM analytics.mart_player_season m
    JOIN api.published_release p ON p.release_id = m.release_id AND p.singleton;
CREATE VIEW api.current_player_team_season AS
    SELECT m.* FROM analytics.mart_player_team_season m
    JOIN api.published_release p ON p.release_id = m.release_id AND p.singleton;
CREATE VIEW api.current_team_season AS
    SELECT m.* FROM analytics.mart_team_season m
    JOIN api.published_release p ON p.release_id = m.release_id AND p.singleton;
CREATE VIEW api.current_capabilities AS
    SELECT m.* FROM analytics.release_capability m
    JOIN api.published_release p ON p.release_id = m.release_id AND p.singleton;
COMMIT;

-- Publication contract (orchestrator, separate explicit transaction):
-- 1. Freeze meta.release_input before dbt; set target.schema=build_<uuid_hex>.
--    MVP models omit custom schema configs and all use that isolated schema.
--    If custom schemas are later added, map dbt's default suffixes explicitly.
-- 2. dbt build + full contract/coverage checks. Record artifacts; seal manifest.
-- 3. BEGIN; SELECT pg_advisory_xact_lock(81421, 1); (global publisher/GC lock)
--    SELECT release_id FROM api.published_release WHERE singleton FOR UPDATE;
--    Verify release remains candidate/validated and expected previous pointer.
--    INSERT analytics dimensions, capability, facts and marts in FK order,
--    using explicit column lists; omit GENERATED ALWAYS columns.
--    Every inserted row MUST carry the same new release_id.
--    Run publisher row-count/referential/coverage checks; set release published.
--    INSERT/UPDATE api.published_release and append meta.publication_event;
--    COMMIT. Any error rolls back copies AND pointer; old release stays usable.
-- 4. ANALYZE after commit, and destroy isolated build schemas after retention.
--    Rebuild FULL coherent candidate; never patch a published release in place.
--    Rollback is a pointer swap to an intact previous release plus an audit event.
--
-- Roles provisioned through reviewed infrastructure migrations, not this file:
-- migration_owner owns schemas; ingestor INSERT/SELECT raw + run metadata only;
-- dbt_builder reads frozen inputs and owns build_* schemas, no analytics DML;
-- publisher INSERT analytics, update release/pointer, append publication_event;
-- api_reader SELECT analytics + pointer + api.release_metadata only;
-- tableau_reader SELECT approved api.current_* views, selected analytics marts/
-- dimensions and api.release_metadata for retained-release extracts; no raw access;
-- janitor DELETE retired analytics releases only, under publisher/GC lock.
-- Raw revision tables and published analytics get NO normal UPDATE/DELETE grants.
-- No GRANT ALL, shared superuser, credentials, or public-schema write access.
--
-- Retention: keep current + previous good release + configurable request/cursor
-- grace window (e.g. 30 days). API returns 400 for malformed/expired cursors,
-- and 410 for a known retired release whose serving rows were collected.
-- Janitor locks global publication/GC lock, verifies release is not current or
-- rollback target and older than grace; removes dependent analytics rows in FK
-- order, then marks release retired. Preserve release/input/audit metadata.
-- Archive compressed HTML durably; never delete referenced snapshots or parses.
-- DELETE WHERE release_id uses every analytic table's leading PK column.
-- snapshot/parse FK indexes support safe raw archive/reference audits.
-- Start unpartitioned for MVP. If retention volume warrants partitioning, use
-- release_id LIST or time RANGE with partition keys included in all PK/FK keys.

