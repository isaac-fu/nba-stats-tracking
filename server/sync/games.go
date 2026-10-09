package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

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
	ID              int      `json:"id"`
	Date            string   `json:"date"`
	Season          int      `json:"season"`
	Status          string   `json:"status"`
	Postseason      bool     `json:"postseason"`
	HomeTeam        APITeam  `json:"home_team"`
	VisitorTeam     APITeam  `json:"visitor_team"`
	HomeTeamScore   int      `json:"home_team_score"`
	VisitorTeamScore int     `json:"visitor_team_score"`
}

type GamesResponse struct {
	Data []APIGame `json:"data"`
	Meta struct {
		NextCursor *int `json:"next_cursor"`
	} `json:"meta"`
}

func importGames(
	ctx context.Context,
	db *pgxpool.Pool,
	apiKey string,
) error {
	var cursor int

	for {
		params := url.Values{}
		params.Set("per_page", "100")

		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		apiURL := "https://api.balldontlie.io/v1/games?" + params.Encode()

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

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return fmt.Errorf("games API request failed: %s", resp.Status)
		}

		var result GamesResponse

		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil {
			return err
		}

		for _, game := range result.Data {
			commandTag, err := db.Exec(ctx, `
				INSERT INTO games (
					external_id,
					home_team_id,
					away_team_id,
					game_date,
					home_score,
					away_score
				)
				SELECT
					$1,
					home.id,
					away.id,
					$4::date,
					$5,
					$6
				FROM teams AS home
				JOIN teams AS away
					ON away.external_id = $3
				WHERE home.external_id = $2
				ON CONFLICT (external_id)
				DO UPDATE SET
					home_team_id = EXCLUDED.home_team_id,
					away_team_id = EXCLUDED.away_team_id,
					game_date = EXCLUDED.game_date,
					home_score = EXCLUDED.home_score,
					away_score = EXCLUDED.away_score
			`,
				game.ID,
				game.HomeTeam.ID,
				game.VisitorTeam.ID,
				game.Date,
				game.HomeTeamScore,
				game.VisitorTeamScore,
			)

			if err != nil {
				return fmt.Errorf("inserting game %d: %w", game.ID, err)
			}

			if commandTag.RowsAffected() == 0 {
				return fmt.Errorf(
					"could not find teams for game %d: home API ID %d, away API ID %d",
					game.ID,
					game.HomeTeam.ID,
					game.VisitorTeam.ID,
				)
			}
		}

		if result.Meta.NextCursor == nil {
			break
		}

		cursor = *result.Meta.NextCursor
	}

	fmt.Println("Imported games")
	return nil
}