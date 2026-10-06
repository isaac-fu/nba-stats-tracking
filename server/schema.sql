CREATE TABLE IF NOT EXISTS teams (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    abbreviation VARCHAR(3) NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS players (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL,
    team_id INTEGER REFERENCES teams(id)
);

CREATE TABLE IF NOT EXISTS games (
    id SERIAL PRIMARY KEY,
    home_team_id INTEGER NOT NULL REFERENCES teams(id),
    away_team_id INTEGER NOT NULL REFERENCES teams(id),
    game_date DATE NOT NULL,
    home_score INTEGER,
    away_score INTEGER
);

CREATE TABLE IF NOT EXISTS player_game_stats (
    id SERIAL PRIMARY KEY,
    game_id INTEGER NOT NULL REFERENCES games(id),
    player_id INTEGER NOT NULL REFERENCES players(id),
    team_id INTEGER NOT NULL REFERENCES teams(id),
    points INTEGER NOT NULL DEFAULT 0,
    rebounds INTEGER NOT NULL DEFAULT 0,
    assists INTEGER NOT NULL DEFAULT 0,
    minutes_played_seconds INTEGER NOT NULL DEFAULT 0,
    steals INTEGER NOT NULL DEFAULT 0,
    blocks INTEGER NOT NULL DEFAULT 0,
    turnovers INTEGER NOT NULL DEFAULT 0,
    field_goals_made INTEGER NOT NULL DEFAULT 0,
    field_goals_attempted INTEGER NOT NULL DEFAULT 0,
    three_point_field_goals_made INTEGER NOT NULL DEFAULT 0,
    three_point_field_goals_attempted INTEGER NOT NULL DEFAULT 0,
    free_throws_made INTEGER NOT NULL DEFAULT 0,
    free_throws_attempted INTEGER NOT NULL DEFAULT 0,

    UNIQUE (game_id, player_id)
);

ALTER TABLE teams ADD COLUMN external_id INTEGER UNIQUE;
ALTER TABLE players ADD COLUMN external_id INTEGER UNIQUE;
ALTER TABLE games ADD COLUMN external_id INTEGER UNIQUE;