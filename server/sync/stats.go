package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
			return 0, err
		}

		seconds, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, err
		}

		return minutes*60 + seconds, nil
	}

	minutes, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
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
	cursor := 0
	totalStats := 0

	for {
		params := url.Values{}
		params.Set("game_ids[]", strconv.Itoa(gameExternalID))
		params.Set("per_page", "100")

		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		apiURL := "https://api.balldontlie.io/v1/stats?" + params.Encode()

		req, err := http.NewRequestWithContext(
			ctx,
			"GET",
			apiURL,
			nil,
		)
		if err != nil {
			return err
		}

		req.Header.Set("Authorization", apiKey)

		resp, err := cooler.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("stats API request failed: %s", resp.Status)
		}

		var result StatsResponse

		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil {
			return err
		}

		for _, stat := range result.Data {
			if _, allowed := allowedTeamIDs[stat.Team.ID]; !allowed {
				fmt.Printf(
					"Skipping stats for player %d on a historical or defunct team external_id=%d\n",
					stat.Player.ID,
					stat.Team.ID,
				)
				continue
			}
			if stat.Game.HomeTeam.ID != 0 && stat.Game.VisitorTeam.ID != 0 {
				_, homeAllowed := allowedTeamIDs[stat.Game.HomeTeam.ID]
				_, visitorAllowed := allowedTeamIDs[stat.Game.VisitorTeam.ID]
				if !homeAllowed || !visitorAllowed {
					fmt.Printf(
						"Skipping stats for player %d because game %d involves a historical or defunct team\n",
						stat.Player.ID,
						stat.Game.ID,
					)
					continue
				}
			}

			minutesSeconds, err := parseMinutes(stat.Minutes)
			if err != nil {
				return fmt.Errorf(
					"parsing minutes for player %d: %w",
					stat.Player.ID,
					err,
				)
			}

			commandTag, err := db.Exec(ctx, `
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
					$4,
					$5,
					$6,
					$7,
					$8,
					$9,
					$10,
					$11,
					$12,
					$13,
					$14,
					$15,
					$16
				FROM games AS g
				JOIN players AS p
					ON p.external_id = $2
				JOIN teams AS t
					ON t.external_id = $3
				WHERE g.external_id = $1
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
					three_point_field_goals_made =
						EXCLUDED.three_point_field_goals_made,
					three_point_field_goals_attempted =
						EXCLUDED.three_point_field_goals_attempted,
					free_throws_made = EXCLUDED.free_throws_made,
					free_throws_attempted =
						EXCLUDED.free_throws_attempted
			`,
				stat.Game.ID,
				stat.Player.ID,
				stat.Team.ID,
				stat.Points,
				stat.Rebounds,
				stat.Assists,
				minutesSeconds,
				stat.Steals,
				stat.Blocks,
				stat.Turnovers,
				stat.FieldGoalsMade,
				stat.FieldGoalsAttempted,
				stat.ThreePointersMade,
				stat.ThreePointersAttempted,
				stat.FreeThrowsMade,
				stat.FreeThrowsAttempted,
			)

			if err != nil {
				return fmt.Errorf(
					"inserting stats for player %d: %w",
					stat.Player.ID,
					err,
				)
			}

			if commandTag.RowsAffected() == 0 {
				return fmt.Errorf(
					"could not map game %d, player %d, or team %d",
					stat.Game.ID,
					stat.Player.ID,
					stat.Team.ID,
				)
			}

			totalStats++
		}

		if result.Meta.NextCursor == nil {
			break
		}

		cursor = *result.Meta.NextCursor
	}

	fmt.Printf(
		"Imported %d player statistics for game %d\n",
		totalStats,
		gameExternalID,
	)

	return nil
}
