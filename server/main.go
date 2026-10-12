package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

func main() {
	// A .env file is useful locally, but deployed environments normally inject
	// variables directly. Do not make the API fail simply because .env is absent.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Printf("could not load .env; using process environment: %v", err)
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		log.Fatalf("invalid DATABASE_URL: %v", err)
	}
	dbConfig.MaxConns = int32(envInt("DB_MAX_CONNS", 10))
	dbConfig.MinConns = 0
	dbConfig.MaxConnLifetime = 30 * time.Minute
	dbConfig.MaxConnIdleTime = 5 * time.Minute
	dbConfig.HealthCheckPeriod = time.Minute

	db, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		log.Fatalf("opening PostgreSQL pool: %v", err)
	}
	defer db.Close()

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := db.Ping(pingCtx); err != nil {
		cancel()
		log.Fatalf("connecting to PostgreSQL: %v", err)
	}
	cancel()

	app := fiber.New(fiber.Config{
		ErrorHandler: apiErrorHandler,
	})
	app.Use(recover.New())
	app.Use(logger.New())

	server := NewServer(db)
	server.RegisterRoutes(app)

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- app.Listen(":" + port)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("starting HTTP server: %v", err)
		}
	case <-ctx.Done():
		log.Println("shutting down HTTP server")
		if err := app.Shutdown(); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		log.Printf("invalid %s=%q; using %d", name, value, fallback)
		return fallback
	}
	return parsed
}

func apiErrorHandler(c *fiber.Ctx, err error) error {
	status := fiber.StatusInternalServerError
	message := "internal server error"

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		status = fiberErr.Code
		if status < 500 {
			message = fiberErr.Message
		}
	}

	return c.Status(status).JSON(errorResponse{Error: message})
}
