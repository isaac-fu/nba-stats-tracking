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
	apiKey string,
) error {
	cursor := 0
	totalPlayers := 0

	for {
		params := url.Values{}
		params.Set("per_page", "100")

		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		apiURL := "https://api.balldontlie.io/v1/players?" + params.Encode()

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
			return fmt.Errorf("players API request failed: %s", resp.Status)
		}

		var result PlayersResponse

		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil {
			return err
		}

		for _, player := range result.Data {
			playerName := player.FirstName + " " + player.LastName

			commandTag, err := db.Exec(ctx, `
				INSERT INTO players (
					external_id,
					name,
					team_id
				)
				SELECT
					$1,
					$2,
					id
				FROM teams
				WHERE external_id = $3
				ON CONFLICT (external_id)
				DO UPDATE SET
					name = EXCLUDED.name,
					team_id = EXCLUDED.team_id
			`,
				player.ID,
				playerName,
				player.Team.ID,
			)

			if err != nil {
				return fmt.Errorf(
					"inserting player %s: %w",
					playerName,
					err,
				)
			}

			if commandTag.RowsAffected() == 0 {
				return fmt.Errorf(
					"could not find team with external ID %d for player %s",
					player.Team.ID,
					playerName,
				)
			}

			totalPlayers++
		}

		if result.Meta.NextCursor == nil {
			break
		}

		cursor = *result.Meta.NextCursor
	}

	fmt.Printf("Imported %d players\n", totalPlayers)
	return nil
}