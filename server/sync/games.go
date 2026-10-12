package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APITeam struct {
	ID           int    `json:"id"`
	FullName     string `json:"full_name"`
	Abbreviation string `json:"abbreviation"`
	City         string `json:"city"`
	Conference   string `json:"conference"`
	Division     string `json:"division"`
}

type APIGame struct {
	ID               int      `json:"id"`
	Date             string   `json:"date"`
	Season           int      `json:"season"`
	Status           string   `json:"status"`
	Postseason       bool     `json:"postseason"`
	HomeTeam         APITeam  `json:"home_team"`
	VisitorTeam      APITeam  `json:"visitor_team"`
	HomeTeamScore    int      `json:"home_team_score"`
	VisitorTeamScore int      `json:"visitor_team_score"`
}

type GamesResponse struct {
	Data []APIGame `json:"data"`
	Meta struct {
		NextCursor *int `json:"next_cursor"`
	} `json:"meta"`
}

type gameRow struct {
	ExternalID     int
	HomeTeamID     int
	AwayTeamID     int
	GameDate       string
	HomeTeamScore  int
	AwayTeamScore  int
}

func importGames(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
	season int,
) error {
	// BALLDONTLIE labels a season by the year it begins. For example,
	// season 2025 represents the 2025-26 NBA season.
	scope := strconv.Itoa(season)
	finalSeason := isFinalNBASeason(season)

	existingGames, err := hasGamesForSeason(ctx, db, season)
	if err != nil {
		return fmt.Errorf("checking existing games for season %d: %w", season, err)
	}

	progress, progressFound, err := loadSyncProgress(ctx, db, "games", scope)
	if err != nil {
		return fmt.Errorf("checking game sync progress for season %d: %w", season, err)
	}

	if finalSeason && !shouldReimportExistingData() && existingGames {
		if !progressFound || progress.Status == "complete" {
			if !progressFound {
				if err := markSyncComplete(ctx, db, "games", scope, 0); err != nil {
					return err
				}
			}
			fmt.Printf(
				"Found existing games for final season %d; skipping games API request\n",
				season,
			)
			return nil
		}
	}

	cursor := 0
	totalProcessed := int64(0)
	if progressFound && progress.Status == "running" {
		cursor = progress.NextCursor
		totalProcessed = progress.RowsProcessed
	}

	page := 0
	skippedGames := 0
	newlyAffectedRows := int64(0)
	for {
		page++
		params := url.Values{}
		params.Set("per_page", strconv.Itoa(defaultAPIPageSize))
		addTeamFilters(params, allowedTeamIDs)
		params.Add("seasons[]", strconv.Itoa(season))
		params.Set("season_type", "regular")
		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		var result GamesResponse
		if err := getAPIJSON(ctx, cooler, apiKey, "/games", params, &result); err != nil {
			return err
		}

		pageGames := make([]gameRow, 0, len(result.Data))
		for _, game := range result.Data {
			_, homeAllowed := allowedTeamIDs[game.HomeTeam.ID]
			_, visitorAllowed := allowedTeamIDs[game.VisitorTeam.ID]
			if !homeAllowed || !visitorAllowed {
				skippedGames++
				continue
			}

			gameDate, err := normalizeGameDate(game.Date)
			if err != nil {
				return fmt.Errorf("normalizing date for game %d: %w", game.ID, err)
			}
			pageGames = append(pageGames, gameRow{
				ExternalID:    game.ID,
				HomeTeamID:    game.HomeTeam.ID,
				AwayTeamID:    game.VisitorTeam.ID,
				GameDate:      gameDate,
				HomeTeamScore: game.HomeTeamScore,
				AwayTeamScore: game.VisitorTeamScore,
			})
		}

		affectedRows, err := mergeGames(ctx, db, pageGames, !finalSeason)
		if err != nil {
			return fmt.Errorf("merging games page %d for season %d: %w", page, season, err)
		}
		newlyAffectedRows += affectedRows
		totalProcessed += int64(len(pageGames))

		if result.Meta.NextCursor == nil {
			if finalSeason {
				if err := markSyncComplete(ctx, db, "games", scope, totalProcessed); err != nil {
					return err
				}
			} else {
				// The active season can gain games and corrected scores later, so
				// leave it running instead of permanently marking it complete.
				if err := saveSyncProgress(ctx, db, "games", scope, "running", nil, totalProcessed); err != nil {
					return err
				}
			}
			fmt.Printf(
				"Processed %d games for season %d (%d affected rows; %d skipped for ineligible teams)\n",
				totalProcessed,
				season,
				newlyAffectedRows,
				skippedGames,
			)
			return nil
		}

		nextCursor := *result.Meta.NextCursor
		if nextCursor == cursor {
			return fmt.Errorf(
				"games API pagination cursor did not advance for season %d: %d",
				season,
				cursor,
			)
		}
		if err := saveSyncProgress(ctx, db, "games", scope, "running", &nextCursor, totalProcessed); err != nil {
			return err
		}
		cursor = nextCursor
	}
}

func isFinalNBASeason(season int) bool {
	currentSeason := envInt("NBA_CURRENT_SEASON", defaultNBASeason(time.Now()))
	return season < currentSeason
}

func normalizeGameDate(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("empty game date")
	}

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("unsupported API date %q", value)
}

func mergeGames(
	ctx context.Context,
	db *pgxpool.Pool,
	games []gameRow,
	updateExisting bool,
) (int64, error) {
	if len(games) == 0 {
		return 0, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("starting game merge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE nba_stage_games (
			external_id INTEGER NOT NULL,
			home_team_external_id INTEGER NOT NULL,
			away_team_external_id INTEGER NOT NULL,
			game_date TEXT NOT NULL,
			home_score INTEGER NOT NULL,
			away_score INTEGER NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return 0, fmt.Errorf("creating game staging table: %w", err)
	}

	rows := make([][]any, 0, len(games))
	for _, game := range games {
		rows = append(rows, []any{
			int32(game.ExternalID),
			int32(game.HomeTeamID),
			int32(game.AwayTeamID),
			game.GameDate,
			int32(game.HomeTeamScore),
			int32(game.AwayTeamScore),
		})
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"nba_stage_games"},
		[]string{
			"external_id",
			"home_team_external_id",
			"away_team_external_id",
			"game_date",
			"home_score",
			"away_score",
		},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, fmt.Errorf("copying games into staging: %w", err)
	}

	var missingTeams int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM nba_stage_games AS s
		LEFT JOIN teams AS home ON home.external_id = s.home_team_external_id
		LEFT JOIN teams AS away ON away.external_id = s.away_team_external_id
		WHERE home.id IS NULL OR away.id IS NULL
	`).Scan(&missingTeams); err != nil {
		return 0, fmt.Errorf("validating staged game teams: %w", err)
	}
	if missingTeams > 0 {
		return 0, fmt.Errorf("%d staged games reference teams not present in teams", missingTeams)
	}

	conflictClause := "ON CONFLICT (external_id) DO NOTHING"
	if updateExisting {
		conflictClause = `
			ON CONFLICT (external_id)
			DO UPDATE SET
				home_team_id = EXCLUDED.home_team_id,
				away_team_id = EXCLUDED.away_team_id,
				game_date = EXCLUDED.game_date,
				home_score = EXCLUDED.home_score,
				away_score = EXCLUDED.away_score
			WHERE (
				games.home_team_id,
				games.away_team_id,
				games.game_date,
				games.home_score,
				games.away_score
			) IS DISTINCT FROM (
				EXCLUDED.home_team_id,
				EXCLUDED.away_team_id,
				EXCLUDED.game_date,
				EXCLUDED.home_score,
				EXCLUDED.away_score
			)`
	}

	query := `
		INSERT INTO games (
			external_id,
			home_team_id,
			away_team_id,
			game_date,
			home_score,
			away_score
		)
		SELECT
			s.external_id,
			home.id,
			away.id,
			s.game_date::date,
			s.home_score,
			s.away_score
		FROM nba_stage_games AS s
		JOIN teams AS home ON home.external_id = s.home_team_external_id
		JOIN teams AS away ON away.external_id = s.away_team_external_id
		` + conflictClause

	commandTag, err := tx.Exec(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("merging staged games: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing game merge: %w", err)
	}
	return commandTag.RowsAffected(), nil
}

func hasGamesForSeason(
	ctx context.Context,
	db *pgxpool.Pool,
	season int,
) (bool, error) {
	var exists bool
	err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM games
			WHERE game_date >= make_date($1, 10, 1)
			  AND game_date < make_date($1 + 1, 10, 1)
		)
	`, season).Scan(&exists)
	return exists, err
}
