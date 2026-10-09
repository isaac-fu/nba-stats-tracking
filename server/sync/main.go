package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load("../.env"); err != nil {
		log.Fatal("could not load .env")
	}

	ctx := context.Background()

	db, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := db.Ping(ctx); err != nil {
		log.Fatal(err)
	}

	apiKey := os.Getenv("BALLDONTLIE_API_KEY")

	if err := importTeams(ctx, db, apiKey); err != nil {
		log.Fatal(err)
	}

	if err := importPlayers(ctx, db, apiKey); err != nil {
		log.Fatal(err)
	}

	if err := importGames(ctx, db, apiKey); err != nil {
		log.Fatal(err)
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
		apiKey,
		gameExternalID,
	); err != nil {
		log.Fatal(err)
	}
}