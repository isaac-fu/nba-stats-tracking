package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type syncProgress struct {
	Status        string
	NextCursor    int
	RowsProcessed int64
}

type gameRef struct {
	DatabaseID  int
	ExternalID int
}

// validateBaseSchema fails early with all missing columns instead of waiting
// for the first importer to produce a cryptic PostgreSQL error. The tables
// themselves remain application-owned schema and should normally be created by
// a migration before the sync process starts.
func validateBaseSchema(ctx context.Context, db *pgxpool.Pool) error {
	rows, err := db.Query(ctx, `
		WITH required(table_name, column_name) AS (
			VALUES
				('teams', 'external_id'),
				('teams', 'name'),
				('teams', 'abbreviation'),
				('players', 'external_id'),
				('players', 'name'),
				('players', 'team_id'),
				('games', 'external_id'),
				('games', 'home_team_id'),
				('games', 'away_team_id'),
				('games', 'game_date'),
				('games', 'home_score'),
				('games', 'away_score'),
				('player_game_stats', 'game_id'),
				('player_game_stats', 'player_id'),
				('player_game_stats', 'team_id'),
				('player_game_stats', 'points'),
				('player_game_stats', 'rebounds'),
				('player_game_stats', 'assists'),
				('player_game_stats', 'minutes_played_seconds'),
				('player_game_stats', 'steals'),
				('player_game_stats', 'blocks'),
				('player_game_stats', 'turnovers'),
				('player_game_stats', 'field_goals_made'),
				('player_game_stats', 'field_goals_attempted'),
				('player_game_stats', 'three_point_field_goals_made'),
				('player_game_stats', 'three_point_field_goals_attempted'),
				('player_game_stats', 'free_throws_made'),
				('player_game_stats', 'free_throws_attempted')
		)
		SELECT required.table_name, required.column_name
		FROM required
		WHERE NOT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = required.table_name
			  AND column_name = required.column_name
		)
		ORDER BY required.table_name, required.column_name
	`)
	if err != nil {
		return fmt.Errorf("checking sync schema: %w", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var tableName string
		var columnName string
		if err := rows.Scan(&tableName, &columnName); err != nil {
			return fmt.Errorf("reading schema check: %w", err)
		}
		missing = append(missing, tableName+"."+columnName)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("finishing schema check: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"database schema is missing required columns: %s; run the schema migration before syncing",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

// These are operational tables for the ETL process, not application facts.
// They make a sync restartable without putting API cursors or transient state
// into the user-facing NBA tables.
func ensureSyncTables(ctx context.Context, db *pgxpool.Pool) error {
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS nba_sync_progress (
			dataset TEXT NOT NULL,
			scope TEXT NOT NULL,
			status TEXT NOT NULL CHECK (status IN ('running', 'complete')),
			next_cursor INTEGER,
			rows_processed BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (dataset, scope)
		);

		CREATE TABLE IF NOT EXISTS nba_sync_game_stats_progress (
			game_id INTEGER PRIMARY KEY REFERENCES games(id) ON DELETE CASCADE,
			status TEXT NOT NULL CHECK (status IN ('running', 'complete')),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);

		CREATE INDEX IF NOT EXISTS nba_sync_game_stats_progress_status_idx
			ON nba_sync_game_stats_progress (status);
	`)
	if err != nil {
		return fmt.Errorf("creating sync metadata tables: %w", err)
	}
	return nil
}

func loadSyncProgress(
	ctx context.Context,
	db *pgxpool.Pool,
	dataset string,
	scope string,
) (syncProgress, bool, error) {
	var progress syncProgress
	err := db.QueryRow(ctx, `
		SELECT
			status,
			COALESCE(next_cursor, 0),
			rows_processed
		FROM nba_sync_progress
		WHERE dataset = $1 AND scope = $2
	`, dataset, scope).Scan(
		&progress.Status,
		&progress.NextCursor,
		&progress.RowsProcessed,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return syncProgress{}, false, nil
	}
	if err != nil {
		return syncProgress{}, false, err
	}
	return progress, true, nil
}

func saveSyncProgress(
	ctx context.Context,
	db *pgxpool.Pool,
	dataset string,
	scope string,
	status string,
	nextCursor *int,
	rowsProcessed int64,
) error {
	var cursorValue any
	if nextCursor != nil {
		cursorValue = *nextCursor
	}

	_, err := db.Exec(ctx, `
		INSERT INTO nba_sync_progress (
			dataset,
			scope,
			status,
			next_cursor,
			rows_processed,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (dataset, scope)
		DO UPDATE SET
			status = EXCLUDED.status,
			next_cursor = EXCLUDED.next_cursor,
			rows_processed = EXCLUDED.rows_processed,
			updated_at = EXCLUDED.updated_at
	`, dataset, scope, status, cursorValue, rowsProcessed)
	if err != nil {
		return fmt.Errorf("saving %s/%s sync progress: %w", dataset, scope, err)
	}
	return nil
}

func markSyncComplete(
	ctx context.Context,
	db *pgxpool.Pool,
	dataset string,
	scope string,
	rowsProcessed int64,
) error {
	return saveSyncProgress(ctx, db, dataset, scope, "complete", nil, rowsProcessed)
}

func shouldReimportExistingData() bool {
	return envBool("NBA_REIMPORT_EXISTING", false)
}

func loadGamesForStats(
	ctx context.Context,
	db *pgxpool.Pool,
	season int,
	onlyPending bool,
) ([]gameRef, error) {
	pendingClause := ""
	if onlyPending {
		pendingClause = `
		  AND (p.status IS NULL OR p.status <> 'complete')`
	}

	query := `
		SELECT g.id, g.external_id
		FROM games AS g
		LEFT JOIN nba_sync_game_stats_progress AS p
			ON p.game_id = g.id
		WHERE g.game_date >= make_date($1, 10, 1)
		  AND g.game_date < make_date($1 + 1, 10, 1)
		` + pendingClause + `
		ORDER BY g.game_date, g.id
	`
	rows, err := db.Query(ctx, query, season)
	if err != nil {
		return nil, fmt.Errorf("loading games for stats for season %d: %w", season, err)
	}
	defer rows.Close()

	var games []gameRef
	for rows.Next() {
		var game gameRef
		if err := rows.Scan(&game.DatabaseID, &game.ExternalID); err != nil {
			return nil, err
		}
		games = append(games, game)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return games, nil
}

func loadGameByExternalID(
	ctx context.Context,
	db *pgxpool.Pool,
	externalID int,
) (gameRef, error) {
	var game gameRef
	err := db.QueryRow(ctx, `
		SELECT id, external_id
		FROM games
		WHERE external_id = $1
	`, externalID).Scan(&game.DatabaseID, &game.ExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return gameRef{}, fmt.Errorf("game external_id=%d does not exist", externalID)
	}
	if err != nil {
		return gameRef{}, err
	}
	return game, nil
}

func markStatsGamesComplete(
	ctx context.Context,
	db *pgxpool.Pool,
	gameIDs []int,
) error {
	if len(gameIDs) == 0 {
		return nil
	}

	values := make([]string, 0, len(gameIDs))
	args := make([]any, 0, len(gameIDs))
	for index, gameID := range gameIDs {
		values = append(values, fmt.Sprintf("($%d, 'complete', now())", index+1))
		args = append(args, gameID)
	}

	query := `
		INSERT INTO nba_sync_game_stats_progress (game_id, status, updated_at)
		VALUES ` + strings.Join(values, ", ") + `
		ON CONFLICT (game_id)
		DO UPDATE SET
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at
	`
	if _, err := db.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("marking stats batches complete: %w", err)
	}
	return nil
}
