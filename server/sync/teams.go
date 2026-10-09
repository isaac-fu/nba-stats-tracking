package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

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
	apiKey string,
) error {
	req, err := http.NewRequestWithContext(
		ctx,
		"GET",
		"https://api.balldontlie.io/v1/teams",
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("teams API request failed: %s", resp.Status)
	}

	var result TeamsResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	for _, team := range result.Data {
		_, err := db.Exec(ctx, `
			INSERT INTO teams (
				external_id,
				name,
				abbreviation
			)
			VALUES ($1, $2, $3)
			ON CONFLICT (external_id)
			DO UPDATE SET
				name = EXCLUDED.name,
				abbreviation = EXCLUDED.abbreviation
		`,
			team.ID,
			team.FullName,
			team.Abbreviation,
		)

		if err != nil {
			return fmt.Errorf("inserting team %s: %w", team.FullName, err)
		}
	}

	fmt.Printf("Imported %d teams\n", len(result.Data))
	return nil
}