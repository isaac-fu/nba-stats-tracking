package main

import (
	"context"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	db            *pgxpool.Pool
	queryTimeout  time.Duration
}

func NewServer(db *pgxpool.Pool) *Server {
	return &Server{
		db:           db,
		queryTimeout: time.Duration(envInt("API_QUERY_TIMEOUT_SECONDS", 5)) * time.Second,
	}
}

func (s *Server) RegisterRoutes(app *fiber.App) {
	app.Get("/", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"service": "nba-stat-tracker-api",
			"version": "v1",
		})
	})
	app.Get("/healthz", s.health)

	v1 := app.Group("/api/v1")

	// Synced NBA facts are intentionally read-only here. The importer owns
	// writes so a user request cannot corrupt or partially overwrite canonical
	// data.
	v1.Get("/teams", s.listTeams)
	v1.Get("/teams/:external_id", s.getTeam)

	v1.Get("/players", s.listPlayers)
	v1.Get("/players/:external_id/stats", s.listPlayerStats)
	v1.Get("/players/:external_id", s.getPlayer)

	v1.Get("/games", s.listGames)
	v1.Get("/games/:external_id/stats", s.listGameStats)
	v1.Get("/games/:external_id", s.getGame)

	v1.Get("/stats", s.listStats)
	v1.Get("/stats/:id", s.getStat)
}

func (s *Server) requestContext(c *fiber.Ctx) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.UserContext(), s.queryTimeout)
}

func (s *Server) health(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(errorResponse{
			Error: "database unavailable",
		})
	}

	return c.JSON(fiber.Map{"status": "ok"})
}

func databaseError(c *fiber.Ctx, err error) error {
	log.Printf("database request failed: %v", err)
	return c.Status(fiber.StatusInternalServerError).JSON(errorResponse{
		Error: "internal server error",
	})
}

func notFound(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusNotFound).JSON(errorResponse{Error: message})
}

func badRequest(c *fiber.Ctx, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(errorResponse{Error: message})
}
