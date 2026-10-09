package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load("../.env"); err != nil {
		log.Fatal("could not load .env")
	}

	ctx := context.Background()

	dbConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	// The importers run sequentially, so a small pool avoids opening more
	// PostgreSQL connections than the sync process needs. Override it with
	// DB_MAX_CONNS when this process is expected to do concurrent DB work.
	dbConfig.MaxConns = int32(envInt("DB_MAX_CONNS", 2))
	dbConfig.MinConns = 0
	dbConfig.MaxConnLifetime = 30 * time.Minute
	dbConfig.MaxConnIdleTime = 5 * time.Minute
	dbConfig.HealthCheckPeriod = time.Minute

	db, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := db.Ping(pingCtx); err != nil {
		cancel()
		log.Fatal(err)
	}
	cancel()

	apiKey := os.Getenv("BALLDONTLIE_API_KEY")
	if apiKey == "" {
		log.Fatal("BALLDONTLIE_API_KEY is not set")
	}

	// Share one cooler across every importer so the request budget is global
	// across teams, players, games, and stats.
	cooler := newPGCoolerFromEnv()

	allowedTeamIDs, err := importTeams(ctx, db, cooler, apiKey)
	if err != nil {
		log.Fatal(err)
	}

	if err := importPlayers(ctx, db, cooler, allowedTeamIDs, apiKey); err != nil {
		log.Fatal(err)
	}

	startSeason := envInt("NBA_START_SEASON", 1980)
	endSeason := envInt("NBA_END_SEASON", 2025)

	if startSeason > endSeason {
		log.Fatal("NBA_START_SEASON cannot be greater than NBA_END_SEASON")
	}

	for season := startSeason; season <= endSeason; season++ {
		fmt.Printf("Starting import for season %d-%d\n", season, season+1)

		if err := importGames(
			ctx,
			db,
			cooler,
			allowedTeamIDs,
			apiKey,
			season,
		); err != nil {
			log.Fatalf("season %d import failed: %v", season, err)
		}

		fmt.Printf("Finished import for season %d-%d\n", season, season+1)
	}

	if len(os.Args) < 2 {
		log.Println("Skipping stats import. Provide a game ID to import stats.")
		return
	}

	gameExternalID, err := strconv.Atoi(os.Args[1])
	if err != nil {
		log.Fatal("invalid game ID:", err)
	}

	if err := importStatsForGame(
		ctx,
		db,
		cooler,
		allowedTeamIDs,
		apiKey,
		gameExternalID,
	); err != nil {
		log.Fatal(err)
	}
}
