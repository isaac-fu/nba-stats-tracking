package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load("../.env"); err != nil && !os.IsNotExist(err) {
		log.Printf("could not load ../.env; using process environment: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	if err := validateBaseSchema(ctx, db); err != nil {
		log.Fatal(err)
	}
	if err := ensureSyncTables(ctx, db); err != nil {
		log.Fatal(err)
	}

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
	currentSeason := envInt("NBA_CURRENT_SEASON", defaultNBASeason(time.Now()))
	endSeason := envInt("NBA_END_SEASON", currentSeason)
	if startSeason > endSeason {
		log.Fatal("NBA_START_SEASON cannot be greater than NBA_END_SEASON")
	}

	for season := startSeason; season <= endSeason; season++ {
		fmt.Printf("Starting game import for season %d-%d\n", season, season+1)
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
		fmt.Printf("Finished game import for season %d-%d\n", season, season+1)
	}

	// A positional game ID keeps the original targeted-import workflow. For a
	// complete stats backfill, set NBA_IMPORT_STATS=true; stats are then fetched
	// in batches and only games without completed stats are requested for final
	// seasons.
	if len(os.Args) > 1 {
		for _, argument := range os.Args[1:] {
			gameExternalID, err := strconv.Atoi(argument)
			if err != nil {
				log.Fatalf("invalid game ID %q: %v", argument, err)
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
		return
	}

	if !envBool("NBA_IMPORT_STATS", false) {
		log.Println("Skipping stats import. Set NBA_IMPORT_STATS=true or pass a game ID.")
		return
	}

	statsStartSeason := envInt("NBA_STATS_START_SEASON", startSeason)
	statsEndSeason := envInt("NBA_STATS_END_SEASON", endSeason)
	if statsStartSeason > statsEndSeason {
		log.Fatal("NBA_STATS_START_SEASON cannot be greater than NBA_STATS_END_SEASON")
	}
	statsBatchSize := envInt("NBA_STATS_BATCH_SIZE", 50)

	for season := statsStartSeason; season <= statsEndSeason; season++ {
		if err := importStatsForSeason(
			ctx,
			db,
			cooler,
			allowedTeamIDs,
			apiKey,
			season,
			statsBatchSize,
		); err != nil {
			log.Fatalf("stats import for season %d failed: %v", season, err)
		}
	}
}
