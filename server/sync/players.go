package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PlayerTeam struct {
	ID int `json:"id"`
}

type APIPlayer struct {
	ID        int        `json:"id"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	Team      PlayerTeam `json:"team"`
}

type PlayersResponse struct {
	Data []APIPlayer `json:"data"`
	Meta struct {
		NextCursor *int `json:"next_cursor"`
	} `json:"meta"`
}

func importPlayers(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
) error {
	var existingPlayers int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM players`).Scan(&existingPlayers); err != nil {
		return fmt.Errorf("checking existing players (verify players.external_id exists): %w", err)
	}

	progress, progressFound, err := loadSyncProgress(ctx, db, "players", "all")
	if err != nil {
		return fmt.Errorf("checking player sync progress: %w", err)
	}

	if existingPlayers > 0 &&
		!shouldReimportExistingData() &&
		(!progressFound || progress.Status == "complete") {
		if !progressFound {
			if err := markSyncComplete(ctx, db, "players", "all", int64(existingPlayers)); err != nil {
				return err
			}
		}
		fmt.Printf(
			"Found %d existing players; skipping players API request\n",
			existingPlayers,
		)
		return nil
	}

	cursor := 0
	totalPlayers := int64(0)
	if progressFound && progress.Status == "running" {
		cursor = progress.NextCursor
		totalPlayers = progress.RowsProcessed
	}

	newlyInserted := int64(0)
	skippedPlayers := 0
	page := 0
	for {
		page++
		params := url.Values{}
		params.Set("per_page", strconv.Itoa(defaultAPIPageSize))
		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		var result PlayersResponse
		if err := getAPIJSON(ctx, cooler, apiKey, "/players", params, &result); err != nil {
			return err
		}

		pagePlayers := make([]APIPlayer, 0, len(result.Data))
		for _, player := range result.Data {
			if _, allowed := allowedTeamIDs[player.Team.ID]; !allowed {
				skippedPlayers++
				continue
			}
			pagePlayers = append(pagePlayers, player)
		}

		inserted, err := mergePlayers(ctx, db, pagePlayers)
		if err != nil {
			return fmt.Errorf("merging players page %d: %w", page, err)
		}
		totalPlayers += int64(len(pagePlayers))
		newlyInserted += inserted

		if result.Meta.NextCursor == nil {
			if err := markSyncComplete(ctx, db, "players", "all", totalPlayers); err != nil {
				return err
			}
			fmt.Printf(
				"Processed %d eligible players (%d newly inserted; %d skipped for ineligible teams)\n",
				totalPlayers,
				newlyInserted,
				skippedPlayers,
			)
			return nil
		}

		nextCursor := *result.Meta.NextCursor
		if nextCursor == cursor {
			return fmt.Errorf("players API pagination cursor did not advance: %d", cursor)
		}
		if err := saveSyncProgress(
			ctx,
			db,
			"players",
			"all",
			"running",
			&nextCursor,
			totalPlayers,
		); err != nil {
			return err
		}
		cursor = nextCursor
	}
}

func mergePlayers(
	ctx context.Context,
	db *pgxpool.Pool,
	players []APIPlayer,
) (int64, error) {
	if len(players) == 0 {
		return 0, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("starting player merge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE nba_stage_players (
			external_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			team_external_id INTEGER NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return 0, fmt.Errorf("creating player staging table: %w", err)
	}

	rows := make([][]any, 0, len(players))
	for _, player := range players {
		rows = append(rows, []any{
			int32(player.ID),
			player.FirstName + " " + player.LastName,
			int32(player.Team.ID),
		})
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"nba_stage_players"},
		[]string{"external_id", "name", "team_external_id"},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, fmt.Errorf("copying players into staging: %w", err)
	}

	var missingTeams int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM nba_stage_players AS s
		LEFT JOIN teams AS t ON t.external_id = s.team_external_id
		WHERE t.id IS NULL
	`).Scan(&missingTeams); err != nil {
		return 0, fmt.Errorf("validating staged player teams: %w", err)
	}
	if missingTeams > 0 {
		return 0, fmt.Errorf("%d staged players reference teams not present in teams", missingTeams)
	}

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO players (external_id, name, team_id)
		SELECT s.external_id, s.name, t.id
		FROM nba_stage_players AS s
		JOIN teams AS t ON t.external_id = s.team_external_id
		ON CONFLICT (external_id) DO NOTHING
	`)
	if err != nil {
		return 0, fmt.Errorf("merging staged players: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing player merge: %w", err)
	}
	return commandTag.RowsAffected(), nil
}
