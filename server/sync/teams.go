package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Team struct {
	ID           int    `json:"id"`
	FullName     string `json:"full_name"`
	Abbreviation string `json:"abbreviation"`
	City         string `json:"city"`
	Conference   string `json:"conference"`
	Division     string `json:"division"`
}

type TeamsResponse struct {
	Data []Team `json:"data"`
}

func importTeams(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	apiKey string,
) (map[int]struct{}, error) {
	existingTeamIDs, currentComplete, err := loadExistingAllowedTeamIDs(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("checking existing teams (verify teams.external_id exists): %w", err)
	}

	progress, progressFound, err := loadSyncProgress(ctx, db, "teams", "all")
	if err != nil {
		return nil, fmt.Errorf("checking team sync progress: %w", err)
	}

	if currentComplete &&
		!shouldReimportExistingData() &&
		(!progressFound || progress.Status == "complete") {
		if !progressFound {
			if err := markSyncComplete(ctx, db, "teams", "all", int64(len(existingTeamIDs))); err != nil {
				return nil, err
			}
		}
		fmt.Printf(
			"Found %d existing eligible NBA teams; skipping teams API request\n",
			len(existingTeamIDs),
		)
		return existingTeamIDs, nil
	}

	var result TeamsResponse
	if err := getAPIJSON(ctx, cooler, apiKey, "/teams", url.Values{}, &result); err != nil {
		return nil, err
	}

	eligibleTeams := make([]Team, 0, len(result.Data))
	allowedTeamIDs := make(map[int]struct{}, len(existingTeamIDs)+len(result.Data))
	for teamID := range existingTeamIDs {
		allowedTeamIDs[teamID] = struct{}{}
	}

	for _, team := range result.Data {
		if !isEligibleNBATeam(team.ID, team.FullName, team.Abbreviation) {
			continue
		}

		eligibleTeams = append(eligibleTeams, team)
		allowedTeamIDs[team.ID] = struct{}{}
	}
	for currentTeamID := range currentNBAAbbreviations {
		if _, ok := allowedTeamIDs[currentTeamID]; !ok {
			return nil, fmt.Errorf(
				"teams API/database data is missing current NBA team external_id=%d",
				currentTeamID,
			)
		}
	}

	inserted, err := mergeTeams(ctx, db, eligibleTeams)
	if err != nil {
		return nil, err
	}
	if err := markSyncComplete(ctx, db, "teams", "all", int64(len(eligibleTeams))); err != nil {
		return nil, err
	}

	fmt.Printf(
		"Processed %d eligible NBA teams (%d newly inserted; historical/defunct teams excluded)\n",
		len(eligibleTeams),
		inserted,
	)
	return allowedTeamIDs, nil
}

const expectedCurrentNBATeams = 30

func loadExistingAllowedTeamIDs(
	ctx context.Context,
	db *pgxpool.Pool,
) (map[int]struct{}, bool, error) {
	rows, err := db.Query(ctx, `
		SELECT external_id, name, abbreviation
		FROM teams
	`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	allowedTeamIDs := make(map[int]struct{})
	currentTeamCount := 0
	for rows.Next() {
		var externalID int
		var fullName string
		var abbreviation string

		if err := rows.Scan(&externalID, &fullName, &abbreviation); err != nil {
			return nil, false, err
		}

		if !isEligibleNBATeam(externalID, fullName, abbreviation) {
			continue
		}
		allowedTeamIDs[externalID] = struct{}{}
		if isCurrentNBATeamID(externalID) {
			currentTeamCount++
		}
	}

	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	return allowedTeamIDs, currentTeamCount == expectedCurrentNBATeams, nil
}

func mergeTeams(
	ctx context.Context,
	db *pgxpool.Pool,
	teams []Team,
) (int64, error) {
	if len(teams) == 0 {
		return 0, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("starting team merge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE nba_stage_teams (
			external_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			abbreviation TEXT NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return 0, fmt.Errorf("creating team staging table: %w", err)
	}

	rows := make([][]any, 0, len(teams))
	for _, team := range teams {
		rows = append(rows, []any{int32(team.ID), team.FullName, team.Abbreviation})
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"nba_stage_teams"},
		[]string{"external_id", "name", "abbreviation"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, fmt.Errorf("copying teams into staging: %w", err)
	}

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO teams (external_id, name, abbreviation)
		SELECT external_id, name, abbreviation
		FROM nba_stage_teams
		ON CONFLICT (external_id) DO NOTHING
	`)
	if err != nil {
		return 0, fmt.Errorf("merging staged teams: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing team merge: %w", err)
	}
	return commandTag.RowsAffected(), nil
}
