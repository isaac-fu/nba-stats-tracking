package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIPlayerGameStat struct {
	ID                  int     `json:"id"`
	Minutes             string  `json:"min"`
	FieldGoalsMade      int     `json:"fgm"`
	FieldGoalsAttempted int     `json:"fga"`
	FieldGoalPercentage  float64 `json:"fg_pct"`

	ThreePointersMade      int     `json:"fg3m"`
	ThreePointersAttempted int     `json:"fg3a"`
	ThreePointPercentage   float64 `json:"fg3_pct"`

	FreeThrowsMade      int     `json:"ftm"`
	FreeThrowsAttempted int     `json:"fta"`
	FreeThrowPercentage float64 `json:"ft_pct"`

	OffensiveRebounds int `json:"oreb"`
	DefensiveRebounds int `json:"dreb"`
	Rebounds          int `json:"reb"`
	Assists           int `json:"ast"`
	Steals            int `json:"stl"`
	Blocks            int `json:"blk"`
	Turnovers         int `json:"turnover"`
	PersonalFouls     int `json:"pf"`
	Points            int `json:"pts"`

	PlusMinus *int `json:"plus_minus"`

	Player APIPlayer `json:"player"`
	Team   APITeam   `json:"team"`
	Game   APIGame   `json:"game"`
}

type StatsResponse struct {
	Data []APIPlayerGameStat `json:"data"`
	Meta struct {
		NextCursor *int `json:"next_cursor"`
	} `json:"meta"`
}

type statRow struct {
	GameExternalID              int
	PlayerExternalID            int
	TeamExternalID              int
	Points                      int
	Rebounds                    int
	Assists                     int
	MinutesPlayedSeconds        int
	Steals                      int
	Blocks                      int
	Turnovers                   int
	FieldGoalsMade              int
	FieldGoalsAttempted         int
	ThreePointFieldGoalsMade    int
	ThreePointFieldGoalsAttempted int
	FreeThrowsMade              int
	FreeThrowsAttempted         int
}

func parseMinutes(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}

	if strings.Contains(value, ":") {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			return 0, fmt.Errorf("invalid minutes format: %s", value)
		}

		minutes, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, fmt.Errorf("invalid minutes value %q: %w", value, err)
		}
		seconds, err := strconv.Atoi(parts[1])
		if err != nil || seconds < 0 || seconds > 59 {
			return 0, fmt.Errorf("invalid seconds value in minutes %q", value)
		}
		return minutes*60 + seconds, nil
	}

	minutes, err := strconv.Atoi(value)
	if err != nil || minutes < 0 {
		return 0, fmt.Errorf("invalid minutes value %q", value)
	}
	return minutes * 60, nil
}

func importStatsForGame(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
	gameExternalID int,
) error {
	game, err := loadGameByExternalID(ctx, db, gameExternalID)
	if err != nil {
		return err
	}
	return importStatsForGames(
		ctx,
		db,
		cooler,
		allowedTeamIDs,
		apiKey,
		[]gameRef{game},
		1,
	)
}

func importStatsForSeason(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
	season int,
	batchSize int,
) error {
	finalSeason := isFinalNBASeason(season)
	games, err := loadGamesForStats(ctx, db, season, finalSeason)
	if err != nil {
		return err
	}
	if len(games) == 0 {
		fmt.Printf("No games need stats for season %d\n", season)
		if finalSeason {
			return markSyncComplete(ctx, db, "stats", strconv.Itoa(season), 0)
		}
		return nil
	}

	if err := importStatsForGames(
		ctx,
		db,
		cooler,
		allowedTeamIDs,
		apiKey,
		games,
		batchSize,
	); err != nil {
		return fmt.Errorf("importing stats for season %d: %w", season, err)
	}

	if finalSeason {
		return markSyncComplete(ctx, db, "stats", strconv.Itoa(season), int64(len(games)))
	}
	return nil
}

func importStatsForGames(
	ctx context.Context,
	db *pgxpool.Pool,
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
	games []gameRef,
	batchSize int,
) error {
	if batchSize <= 0 {
		batchSize = 50
	}

	for start := 0; start < len(games); start += batchSize {
		end := start + batchSize
		if end > len(games) {
			end = len(games)
		}
		batch := games[start:end]

		externalIDs := make([]int, 0, len(batch))
		internalIDs := make([]int, 0, len(batch))
		for _, game := range batch {
			externalIDs = append(externalIDs, game.ExternalID)
			internalIDs = append(internalIDs, game.DatabaseID)
		}

		stats, err := fetchStatsBatch(ctx, cooler, apiKey, externalIDs)
		if err != nil {
			return fmt.Errorf("fetching stats for games %d-%d: %w", start+1, end, err)
		}

		rows, skipped, err := prepareStatRows(stats, allowedTeamIDs, externalIDs)
		if err != nil {
			return fmt.Errorf("preparing stats for games %d-%d: %w", start+1, end, err)
		}
		if _, err := mergeStats(ctx, db, rows); err != nil {
			return fmt.Errorf("merging stats for games %d-%d: %w", start+1, end, err)
		}
		if err := markStatsGamesComplete(ctx, db, internalIDs); err != nil {
			return err
		}

		fmt.Printf(
			"Processed stats for games %d-%d/%d (%d stat rows; %d skipped)\n",
			start+1,
			end,
			len(games),
			len(rows),
			skipped,
		)
	}
	return nil
}

func fetchStatsBatch(
	ctx context.Context,
	cooler *PGCooler,
	apiKey string,
	gameExternalIDs []int,
) ([]APIPlayerGameStat, error) {
	var allStats []APIPlayerGameStat
	cursor := 0

	for {
		params := url.Values{}
		addGameIDFilters(params, gameExternalIDs)
		params.Set("per_page", strconv.Itoa(defaultAPIPageSize))
		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		var result StatsResponse
		if err := getAPIJSON(ctx, cooler, apiKey, "/stats", params, &result); err != nil {
			return nil, err
		}
		allStats = append(allStats, result.Data...)

		if result.Meta.NextCursor == nil {
			return allStats, nil
		}
		nextCursor := *result.Meta.NextCursor
		if nextCursor == cursor {
			return nil, fmt.Errorf("stats API pagination cursor did not advance: %d", cursor)
		}
		cursor = nextCursor
	}
}

func prepareStatRows(
	stats []APIPlayerGameStat,
	allowedTeamIDs map[int]struct{},
	requestedGameIDs []int,
) ([]statRow, int, error) {
	requested := make(map[int]struct{}, len(requestedGameIDs))
	for _, gameID := range requestedGameIDs {
		requested[gameID] = struct{}{}
	}

	rows := make([]statRow, 0, len(stats))
	skipped := 0
	for _, stat := range stats {
		if _, requestedGame := requested[stat.Game.ID]; !requestedGame {
			skipped++
			continue
		}
		if _, allowed := allowedTeamIDs[stat.Team.ID]; !allowed {
			skipped++
			continue
		}
		if stat.Game.HomeTeam.ID != 0 && stat.Game.VisitorTeam.ID != 0 {
			_, homeAllowed := allowedTeamIDs[stat.Game.HomeTeam.ID]
			_, visitorAllowed := allowedTeamIDs[stat.Game.VisitorTeam.ID]
			if !homeAllowed || !visitorAllowed {
				skipped++
				continue
			}
		}

		minutesSeconds, err := parseMinutes(stat.Minutes)
		if err != nil {
			return nil, skipped, fmt.Errorf(
				"parsing minutes for player %d: %w",
				stat.Player.ID,
				err,
			)
		}

		rows = append(rows, statRow{
			GameExternalID:                stat.Game.ID,
			PlayerExternalID:              stat.Player.ID,
			TeamExternalID:                stat.Team.ID,
			Points:                        stat.Points,
			Rebounds:                      stat.Rebounds,
			Assists:                       stat.Assists,
			MinutesPlayedSeconds:          minutesSeconds,
			Steals:                        stat.Steals,
			Blocks:                        stat.Blocks,
			Turnovers:                     stat.Turnovers,
			FieldGoalsMade:                stat.FieldGoalsMade,
			FieldGoalsAttempted:           stat.FieldGoalsAttempted,
			ThreePointFieldGoalsMade:      stat.ThreePointersMade,
			ThreePointFieldGoalsAttempted: stat.ThreePointersAttempted,
			FreeThrowsMade:                stat.FreeThrowsMade,
			FreeThrowsAttempted:           stat.FreeThrowsAttempted,
		})
	}
	return rows, skipped, nil
}

func mergeStats(
	ctx context.Context,
	db *pgxpool.Pool,
	stats []statRow,
) (int64, error) {
	if len(stats) == 0 {
		return 0, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("starting stats merge transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE nba_stage_stats (
			game_external_id INTEGER NOT NULL,
			player_external_id INTEGER NOT NULL,
			team_external_id INTEGER NOT NULL,
			points INTEGER NOT NULL,
			rebounds INTEGER NOT NULL,
			assists INTEGER NOT NULL,
			minutes_played_seconds INTEGER NOT NULL,
			steals INTEGER NOT NULL,
			blocks INTEGER NOT NULL,
			turnovers INTEGER NOT NULL,
			field_goals_made INTEGER NOT NULL,
			field_goals_attempted INTEGER NOT NULL,
			three_point_field_goals_made INTEGER NOT NULL,
			three_point_field_goals_attempted INTEGER NOT NULL,
			free_throws_made INTEGER NOT NULL,
			free_throws_attempted INTEGER NOT NULL
		) ON COMMIT DROP
	`)
	if err != nil {
		return 0, fmt.Errorf("creating stats staging table: %w", err)
	}

	rows := make([][]any, 0, len(stats))
	for _, stat := range stats {
		rows = append(rows, []any{
			int32(stat.GameExternalID),
			int32(stat.PlayerExternalID),
			int32(stat.TeamExternalID),
			int32(stat.Points),
			int32(stat.Rebounds),
			int32(stat.Assists),
			int32(stat.MinutesPlayedSeconds),
			int32(stat.Steals),
			int32(stat.Blocks),
			int32(stat.Turnovers),
			int32(stat.FieldGoalsMade),
			int32(stat.FieldGoalsAttempted),
			int32(stat.ThreePointFieldGoalsMade),
			int32(stat.ThreePointFieldGoalsAttempted),
			int32(stat.FreeThrowsMade),
			int32(stat.FreeThrowsAttempted),
		})
	}

	if _, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"nba_stage_stats"},
		[]string{
			"game_external_id",
			"player_external_id",
			"team_external_id",
			"points",
			"rebounds",
			"assists",
			"minutes_played_seconds",
			"steals",
			"blocks",
			"turnovers",
			"field_goals_made",
			"field_goals_attempted",
			"three_point_field_goals_made",
			"three_point_field_goals_attempted",
			"free_throws_made",
			"free_throws_attempted",
		},
		pgx.CopyFromRows(rows),
	); err != nil {
		return 0, fmt.Errorf("copying stats into staging: %w", err)
	}

	var missingReferences int64
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM nba_stage_stats AS s
		LEFT JOIN games AS g ON g.external_id = s.game_external_id
		LEFT JOIN players AS p ON p.external_id = s.player_external_id
		LEFT JOIN teams AS t ON t.external_id = s.team_external_id
		WHERE g.id IS NULL OR p.id IS NULL OR t.id IS NULL
	`).Scan(&missingReferences); err != nil {
		return 0, fmt.Errorf("validating staged stat references: %w", err)
	}
	if missingReferences > 0 {
		return 0, fmt.Errorf(
			"%d staged stats reference a missing game, player, or team",
			missingReferences,
		)
	}

	commandTag, err := tx.Exec(ctx, `
		INSERT INTO player_game_stats (
			game_id,
			player_id,
			team_id,
			points,
			rebounds,
			assists,
			minutes_played_seconds,
			steals,
			blocks,
			turnovers,
			field_goals_made,
			field_goals_attempted,
			three_point_field_goals_made,
			three_point_field_goals_attempted,
			free_throws_made,
			free_throws_attempted
		)
		SELECT
			g.id,
			p.id,
			t.id,
			s.points,
			s.rebounds,
			s.assists,
			s.minutes_played_seconds,
			s.steals,
			s.blocks,
			s.turnovers,
			s.field_goals_made,
			s.field_goals_attempted,
			s.three_point_field_goals_made,
			s.three_point_field_goals_attempted,
			s.free_throws_made,
			s.free_throws_attempted
		FROM nba_stage_stats AS s
		JOIN games AS g ON g.external_id = s.game_external_id
		JOIN players AS p ON p.external_id = s.player_external_id
		JOIN teams AS t ON t.external_id = s.team_external_id
		ON CONFLICT (game_id, player_id)
		DO UPDATE SET
			team_id = EXCLUDED.team_id,
			points = EXCLUDED.points,
			rebounds = EXCLUDED.rebounds,
			assists = EXCLUDED.assists,
			minutes_played_seconds = EXCLUDED.minutes_played_seconds,
			steals = EXCLUDED.steals,
			blocks = EXCLUDED.blocks,
			turnovers = EXCLUDED.turnovers,
			field_goals_made = EXCLUDED.field_goals_made,
			field_goals_attempted = EXCLUDED.field_goals_attempted,
			three_point_field_goals_made = EXCLUDED.three_point_field_goals_made,
			three_point_field_goals_attempted = EXCLUDED.three_point_field_goals_attempted,
			free_throws_made = EXCLUDED.free_throws_made,
			free_throws_attempted = EXCLUDED.free_throws_attempted
		WHERE (
			player_game_stats.team_id,
			player_game_stats.points,
			player_game_stats.rebounds,
			player_game_stats.assists,
			player_game_stats.minutes_played_seconds,
			player_game_stats.steals,
			player_game_stats.blocks,
			player_game_stats.turnovers,
			player_game_stats.field_goals_made,
			player_game_stats.field_goals_attempted,
			player_game_stats.three_point_field_goals_made,
			player_game_stats.three_point_field_goals_attempted,
			player_game_stats.free_throws_made,
			player_game_stats.free_throws_attempted
		) IS DISTINCT FROM (
			EXCLUDED.team_id,
			EXCLUDED.points,
			EXCLUDED.rebounds,
			EXCLUDED.assists,
			EXCLUDED.minutes_played_seconds,
			EXCLUDED.steals,
			EXCLUDED.blocks,
			EXCLUDED.turnovers,
			EXCLUDED.field_goals_made,
			EXCLUDED.field_goals_attempted,
			EXCLUDED.three_point_field_goals_made,
			EXCLUDED.three_point_field_goals_attempted,
			EXCLUDED.free_throws_made,
			EXCLUDED.free_throws_attempted
		)
	`)
	if err != nil {
		return 0, fmt.Errorf("merging staged stats: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("committing stats merge: %w", err)
	}
	return commandTag.RowsAffected(), nil
}
