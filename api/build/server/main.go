package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	// An empty connection string uses PGHOST, PGPORT, PGDATABASE, PGUSER,
	// and PGPASSWORD supplied by Compose. Never log a connection string.
	config, err := pgxpool.ParseConfig("")
	if err != nil {
		log.Fatal("Invalid database configuration")
	}
	config.MaxConns = 10
	config.ConnConfig.ConnectTimeout = 5 * time.Second
	config.ConnConfig.RuntimeParams["application_name"] = "nba-stats-api"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		log.Fatal("Unable to create database connection pool")
	}
	defer pool.Close()

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if pool.Ping(checkCtx) != nil {
		log.Print("Database is not ready; the API will retry when handling requests")
	}
	checkCancel()

	address := os.Getenv("API_ADDR")
	if address == "" {
		address = ":8080"
	}
	server := &http.Server{
		Addr:              address,
		Handler:           NewHandler(&PostgresStore{pool: pool}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	serverErrors := make(chan error, 1)
	log.Printf("Starting NBA API on %s", address)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-stop.Done():
		// Wait for in-flight requests before closing the PostgreSQL pool.
		ctx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Print("Server shutdown timed out")
			_ = server.Close()
		}
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal("HTTP server failed to start")
		}
	}
}
