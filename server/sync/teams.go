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
	cooler *PGCooler,
	apiKey string,
) (map[int]struct{}, error) {
	existingTeamIDs, complete, err := loadExistingAllowedTeamIDs(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("checking existing teams: %w", err)
	}
	if complete {
		fmt.Printf(
			"Found %d existing NBA teams; skipping teams API request\n",
			len(existingTeamIDs),
		)
		return existingTeamIDs, nil
	}

	req, err := http.NewRequestWithContext(
		ctx,
		"GET",
		"https://api.balldontlie.io/v1/teams",
		nil,
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", apiKey)

	resp, err := cooler.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("teams API request failed: %s", resp.Status)
	}

	var result TeamsResponse

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	allowedTeamIDs := make(map[int]struct{})
	importedTeams := 0
	for _, team := range result.Data {
		if !isEligibleNBATeam(team.ID, team.FullName) {
			fmt.Printf(
				"Skipping historical or defunct team %s (external_id=%d)\n",
				team.FullName,
				team.ID,
			)
			continue
		}

		_, err := db.Exec(ctx, `
        INSERT INTO teams (
            external_id,
            name,
            abbreviation
        )
        VALUES ($1, $2, $3)
        ON CONFLICT (external_id)
		DO NOTHING
    `,
        team.ID,
        team.FullName,
        team.Abbreviation,
        )

		if err != nil {
			return nil, fmt.Errorf(
				"inserting team %s (external_id=%d, abbreviation=%s): %w",
				team.FullName,
				team.ID,
				team.Abbreviation,
				err,
			)
		}
		allowedTeamIDs[team.ID] = struct{}{}
		importedTeams++
	}

	fmt.Printf("Imported %d teams\n", importedTeams)
	return allowedTeamIDs, nil
}

const expectedCurrentNBATeams = 30

func loadExistingAllowedTeamIDs(
	ctx context.Context,
	db *pgxpool.Pool,
) (map[int]struct{}, bool, error) {
	rows, err := db.Query(ctx, `
		SELECT external_id, name
		FROM teams
	`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	allowedTeamIDs := make(map[int]struct{})
	for rows.Next() {
		var externalID int
		var fullName string

		if err := rows.Scan(&externalID, &fullName); err != nil {
			return nil, false, err
		}

		if isEligibleNBATeam(externalID, fullName) {
			allowedTeamIDs[externalID] = struct{}{}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	return allowedTeamIDs, len(allowedTeamIDs) >= expectedCurrentNBATeams, nil
}
