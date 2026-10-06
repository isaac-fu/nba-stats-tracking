package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
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

func main() {
	// Your .env is one folder above server.
	if err := godotenv.Load("../.env"); err != nil {
		panic("could not load .env")
	}

	apiKey := os.Getenv("BALLDONTLIE_API_KEY")
	databaseURL := os.Getenv("DATABASE_URL")

	req, err := http.NewRequest(
		"GET",
		"https://api.balldontlie.io/v1/teams",
		nil,
	)
	if err != nil {
		panic(err)
	}

	req.Header.Set("Authorization", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		panic(fmt.Sprintf("API request failed: %s", resp.Status))
	}

	var result TeamsResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		panic(err)
	}

	ctx := context.Background()

	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	for _, team := range result.Data {
		_, err := db.Exec(ctx, `
			INSERT INTO teams (
				external_id,
				name,
				abbreviation
			)
			VALUES ($1, $2, $3)
			ON CONFLICT (abbreviation)
			DO UPDATE SET
				external_id = EXCLUDED.external_id,
				name = EXCLUDED.name
		`,
			team.ID,
			team.FullName,
			team.Abbreviation,
		)

		if err != nil {
			panic(err)
		}
	}

	fmt.Printf("Imported %d teams\n", len(result.Data))
}