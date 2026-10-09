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
	cooler *PGCooler,
	allowedTeamIDs map[int]struct{},
	apiKey string,
	season int,
) error {
	// BALLDONTLIE labels a season by the year it begins. For example,
	// season 2025 represents the 2025-26 NBA season.

	existingGames, err := hasGamesForSeason(ctx, db, season)
	if err != nil {
		return fmt.Errorf("checking existing games for season %d: %w", season, err)
	}
	if existingGames {
		fmt.Printf(
			"Found existing games for season %d; skipping games API request\n",
			season,
		)
		return nil
	}

	var cursor int
	page := 0
	importedGames := 0
	skippedGames := 0

	for {
		page++

		params := url.Values{}
		params.Set("per_page", "100")
		addTeamFilters(params, allowedTeamIDs)
		params.Add("seasons[]", strconv.Itoa(season))
		params.Set("season_type", "regular")

		if cursor > 0 {
			params.Set("cursor", strconv.Itoa(cursor))
		}

		apiURL := "https://api.balldontlie.io/v1/games?" + params.Encode()
		fmt.Printf("Fetching games page %d (cursor=%d)...\n", page, cursor)

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
			return fmt.Errorf("games API request failed: %s", resp.Status)
		}

		var result GamesResponse

		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil {
			return err
		}

		fmt.Printf("Received %d games on page %d\n", len(result.Data), page)

		for _, game := range result.Data {
			_, homeAllowed := allowedTeamIDs[game.HomeTeam.ID]
			_, visitorAllowed := allowedTeamIDs[game.VisitorTeam.ID]
			if !homeAllowed || !visitorAllowed {
				skippedGames++
				continue
			}

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
			DO NOTHING
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
			importedGames++
		}

		if result.Meta.NextCursor == nil {
			break
		}

		nextCursor := *result.Meta.NextCursor
		if nextCursor == cursor {
			return fmt.Errorf(
				"games API pagination cursor did not advance: %d",
				cursor,
			)
		}

		cursor = nextCursor
	}

	fmt.Printf(
		"Imported %d games (skipped %d games involving historical or defunct teams)\n",
		importedGames,
		skippedGames,
	)
	return nil
}

func hasGamesForSeason(
	ctx context.Context,
	db *pgxpool.Pool,
	season int,
) (bool, error) {
	var gameCount int

	err := db.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM games
		WHERE game_date >= make_date($1, 10, 1)
		  AND game_date < make_date($1 + 1, 10, 1)
	`, season).Scan(&gameCount)
	if err != nil {
		return false, err
	}

	return gameCount > 0, nil
}
