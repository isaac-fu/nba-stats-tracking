package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
)

type pageOptions struct {
	limit  int
	offset int
}

func parsePagination(c *fiber.Ctx) (pageOptions, error) {
	page := pageOptions{limit: 25}

	if raw := c.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			return pageOptions{}, fmt.Errorf("limit must be an integer between 1 and 100")
		}
		page.limit = value
	}

	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return pageOptions{}, fmt.Errorf("offset must be a non-negative integer")
		}
		page.offset = value
	}

	return page, nil
}

func addPagination(query string, args *[]any, page pageOptions) string {
	limitPosition := len(*args) + 1
	offsetPosition := len(*args) + 2
	*args = append(*args, page.limit+1, page.offset)
	return fmt.Sprintf("%s LIMIT $%d OFFSET $%d", query, limitPosition, offsetPosition)
}

func parsePathID(c *fiber.Ctx, name string) (int, error) {
	value, err := strconv.Atoi(c.Params(name))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func parseQueryID(c *fiber.Ctx, name string) (*int, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return nil, fmt.Errorf("%s must be a positive integer", name)
	}
	return &value, nil
}

func parseSeason(c *fiber.Ctx) (*int, error) {
	raw := strings.TrimSpace(c.Query("season"))
	if raw == "" {
		return nil, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 1900 || value > 2100 {
		return nil, fmt.Errorf("season must be a year between 1900 and 2100")
	}
	return &value, nil
}

func parseDate(c *fiber.Ctx, name string) (*time.Time, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}

	value, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return nil, fmt.Errorf("%s must use YYYY-MM-DD format", name)
	}
	return &value, nil
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

func trimPage[T any](data []T, page pageOptions) ([]T, pageMeta) {
	hasMore := len(data) > page.limit
	if hasMore {
		data = data[:page.limit]
	}
	return data, pageMeta{
		Limit:   page.limit,
		Offset:  page.offset,
		Count:   len(data),
		HasMore: hasMore,
	}
}

func scanTeam(scanner interface{ Scan(...any) error }) (Team, error) {
	var team Team
	err := scanner.Scan(&team.ID, &team.ExternalID, &team.Name, &team.Abbreviation)
	return team, err
}

func scanGame(scanner interface{ Scan(...any) error }) (Game, error) {
	var game Game
	var gameDate time.Time
	err := scanner.Scan(
		&game.ID,
		&game.ExternalID,
		&gameDate,
		&game.HomeScore,
		&game.AwayScore,
		&game.HomeTeam.ID,
		&game.HomeTeam.ExternalID,
		&game.HomeTeam.Name,
		&game.HomeTeam.Abbreviation,
		&game.AwayTeam.ID,
		&game.AwayTeam.ExternalID,
		&game.AwayTeam.Name,
		&game.AwayTeam.Abbreviation,
	)
	if err != nil {
		return Game{}, err
	}
	game.GameDate = gameDate.Format("2006-01-02")
	return game, nil
}

func scanPlayer(scanner interface{ Scan(...any) error }) (Player, error) {
	var player Player
	var team Team
	err := scanner.Scan(
		&player.ID,
		&player.ExternalID,
		&player.Name,
		&team.ID,
		&team.ExternalID,
		&team.Name,
		&team.Abbreviation,
	)
	if err != nil {
		return Player{}, err
	}
	player.Team = &team
	return player, nil
}

func scanStat(scanner interface{ Scan(...any) error }) (PlayerGameStat, error) {
	var stat PlayerGameStat
	var gameDate time.Time
	err := scanner.Scan(
		&stat.ID,
		&stat.GameID,
		&gameDate,
		&stat.PlayerID,
		&stat.PlayerName,
		&stat.TeamID,
		&stat.TeamName,
		&stat.TeamAbbreviation,
		&stat.Points,
		&stat.Rebounds,
		&stat.Assists,
		&stat.MinutesPlayedSeconds,
		&stat.Steals,
		&stat.Blocks,
		&stat.Turnovers,
		&stat.FieldGoalsMade,
		&stat.FieldGoalsAttempted,
		&stat.ThreePointFieldGoalsMade,
		&stat.ThreePointFieldGoalsAttempted,
		&stat.FreeThrowsMade,
		&stat.FreeThrowsAttempted,
	)
	if err != nil {
		return PlayerGameStat{}, err
	}
	stat.GameDate = gameDate.Format("2006-01-02")
	return stat, nil
}

func (s *Server) listTeams(c *fiber.Ctx) error {
	page, err := parsePagination(c)
	if err != nil {
		return badRequest(c, err.Error())
	}

	query := `
		SELECT id, external_id, name, abbreviation
		FROM teams`
	args := make([]any, 0, 3)
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		query += `
			WHERE name ILIKE '%' || $1 || '%' ESCAPE E'\\'
			   OR abbreviation ILIKE '%' || $1 || '%' ESCAPE E'\\'`
		args = append(args, escapeLike(search))
	}
	query += ` ORDER BY name ASC, external_id ASC`
	query = addPagination(query, &args, page)

	ctx, cancel := s.requestContext(c)
	defer cancel()
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return databaseError(c, err)
	}
	defer rows.Close()

	data := make([]Team, 0, page.limit)
	for rows.Next() {
		team, err := scanTeam(rows)
		if err != nil {
			return databaseError(c, err)
		}
		data = append(data, team)
	}
	if err := rows.Err(); err != nil {
		return databaseError(c, err)
	}

	data, meta := trimPage(data, page)
	return c.JSON(paginatedResponse[Team]{Data: data, Meta: meta})
}

func (s *Server) getTeam(c *fiber.Ctx) error {
	externalID, err := parsePathID(c, "external_id")
	if err != nil {
		return badRequest(c, err.Error())
	}

	ctx, cancel := s.requestContext(c)
	defer cancel()
	team, err := scanTeam(s.db.QueryRow(ctx, `
		SELECT id, external_id, name, abbreviation
		FROM teams
		WHERE external_id = $1`, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(c, "team not found")
	}
	if err != nil {
		return databaseError(c, err)
	}
	return c.JSON(team)
}

const playerSelect = `
	SELECT
		p.id, p.external_id, p.name,
		t.id, t.external_id, t.name, t.abbreviation
	FROM players AS p
	JOIN teams AS t ON t.id = p.team_id`

func (s *Server) listPlayers(c *fiber.Ctx) error {
	page, err := parsePagination(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	teamID, err := parseQueryID(c, "team_id")
	if err != nil {
		return badRequest(c, err.Error())
	}

	query := playerSelect
	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if search := strings.TrimSpace(c.Query("search")); search != "" {
		where = append(where, "p.name ILIKE '%' || $1 || '%' ESCAPE '\\'")
		args = append(args, escapeLike(search))
	}
	if teamID != nil {
		where = append(where, fmt.Sprintf("t.external_id = $%d", len(args)+1))
		args = append(args, *teamID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY p.name ASC, p.external_id ASC`
	query = addPagination(query, &args, page)

	ctx, cancel := s.requestContext(c)
	defer cancel()
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return databaseError(c, err)
	}
	defer rows.Close()

	data := make([]Player, 0, page.limit)
	for rows.Next() {
		player, err := scanPlayer(rows)
		if err != nil {
			return databaseError(c, err)
		}
		data = append(data, player)
	}
	if err := rows.Err(); err != nil {
		return databaseError(c, err)
	}

	data, meta := trimPage(data, page)
	return c.JSON(paginatedResponse[Player]{Data: data, Meta: meta})
}

func (s *Server) getPlayer(c *fiber.Ctx) error {
	externalID, err := parsePathID(c, "external_id")
	if err != nil {
		return badRequest(c, err.Error())
	}

	ctx, cancel := s.requestContext(c)
	defer cancel()
	player, err := scanPlayer(s.db.QueryRow(ctx, playerSelect+`
		WHERE p.external_id = $1`, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(c, "player not found")
	}
	if err != nil {
		return databaseError(c, err)
	}
	return c.JSON(player)
}

const gameSelect = `
	SELECT
		g.id, g.external_id, g.game_date, g.home_score, g.away_score,
		h.id, h.external_id, h.name, h.abbreviation,
		a.id, a.external_id, a.name, a.abbreviation
	FROM games AS g
	JOIN teams AS h ON h.id = g.home_team_id
	JOIN teams AS a ON a.id = g.away_team_id`

func (s *Server) listGames(c *fiber.Ctx) error {
	page, err := parsePagination(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	season, err := parseSeason(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	teamID, err := parseQueryID(c, "team_id")
	if err != nil {
		return badRequest(c, err.Error())
	}
	fromDate, err := parseDate(c, "from")
	if err != nil {
		return badRequest(c, err.Error())
	}
	toDate, err := parseDate(c, "to")
	if err != nil {
		return badRequest(c, err.Error())
	}

	query := gameSelect
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if season != nil {
		where = append(where, fmt.Sprintf(
			"g.game_date >= make_date($%d, 10, 1) AND g.game_date < make_date($%d + 1, 10, 1)",
			len(args)+1, len(args)+1,
		))
		args = append(args, *season)
	}
	if fromDate != nil {
		where = append(where, fmt.Sprintf("g.game_date >= $%d", len(args)+1))
		args = append(args, *fromDate)
	}
	if toDate != nil {
		where = append(where, fmt.Sprintf("g.game_date <= $%d", len(args)+1))
		args = append(args, *toDate)
	}
	if teamID != nil {
		where = append(where, fmt.Sprintf("(h.external_id = $%d OR a.external_id = $%d)", len(args)+1, len(args)+1))
		args = append(args, *teamID)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY g.game_date DESC, g.id DESC`
	query = addPagination(query, &args, page)

	ctx, cancel := s.requestContext(c)
	defer cancel()
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return databaseError(c, err)
	}
	defer rows.Close()

	data := make([]Game, 0, page.limit)
	for rows.Next() {
		game, err := scanGame(rows)
		if err != nil {
			return databaseError(c, err)
		}
		data = append(data, game)
	}
	if err := rows.Err(); err != nil {
		return databaseError(c, err)
	}

	data, meta := trimPage(data, page)
	return c.JSON(paginatedResponse[Game]{Data: data, Meta: meta})
}

func (s *Server) getGame(c *fiber.Ctx) error {
	externalID, err := parsePathID(c, "external_id")
	if err != nil {
		return badRequest(c, err.Error())
	}

	ctx, cancel := s.requestContext(c)
	defer cancel()
	game, err := scanGame(s.db.QueryRow(ctx, gameSelect+`
		WHERE g.external_id = $1`, externalID))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(c, "game not found")
	}
	if err != nil {
		return databaseError(c, err)
	}
	return c.JSON(game)
}

const statSelect = `
	SELECT
		pgs.id,
		g.external_id,
		g.game_date,
		p.external_id,
		p.name,
		t.external_id,
		t.name,
		t.abbreviation,
		pgs.points,
		pgs.rebounds,
		pgs.assists,
		pgs.minutes_played_seconds,
		pgs.steals,
		pgs.blocks,
		pgs.turnovers,
		pgs.field_goals_made,
		pgs.field_goals_attempted,
		pgs.three_point_field_goals_made,
		pgs.three_point_field_goals_attempted,
		pgs.free_throws_made,
		pgs.free_throws_attempted
	FROM player_game_stats AS pgs
	JOIN games AS g ON g.id = pgs.game_id
	JOIN players AS p ON p.id = pgs.player_id
	JOIN teams AS t ON t.id = pgs.team_id`

type statFilters struct {
	gameID   *int
	playerID *int
	teamID   *int
	season   *int
}

func (s *Server) listStats(c *fiber.Ctx) error {
	filters, err := parseStatFilters(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	return s.listStatsWithFilters(c, filters)
}

func (s *Server) listGameStats(c *fiber.Ctx) error {
	gameID, err := parsePathID(c, "external_id")
	if err != nil {
		return badRequest(c, err.Error())
	}
	filters, err := parseStatFilters(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	filters.gameID = &gameID
	return s.listStatsWithFilters(c, filters)
}

func (s *Server) listPlayerStats(c *fiber.Ctx) error {
	playerID, err := parsePathID(c, "external_id")
	if err != nil {
		return badRequest(c, err.Error())
	}
	filters, err := parseStatFilters(c)
	if err != nil {
		return badRequest(c, err.Error())
	}
	filters.playerID = &playerID
	return s.listStatsWithFilters(c, filters)
}

func parseStatFilters(c *fiber.Ctx) (statFilters, error) {
	gameID, err := parseQueryID(c, "game_id")
	if err != nil {
		return statFilters{}, err
	}
	playerID, err := parseQueryID(c, "player_id")
	if err != nil {
		return statFilters{}, err
	}
	teamID, err := parseQueryID(c, "team_id")
	if err != nil {
		return statFilters{}, err
	}
	season, err := parseSeason(c)
	if err != nil {
		return statFilters{}, err
	}
	return statFilters{
		gameID:   gameID,
		playerID: playerID,
		teamID:   teamID,
		season:   season,
	}, nil
}

func (s *Server) listStatsWithFilters(c *fiber.Ctx, filters statFilters) error {
	page, err := parsePagination(c)
	if err != nil {
		return badRequest(c, err.Error())
	}

	query := statSelect
	where := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if filters.gameID != nil {
		where = append(where, fmt.Sprintf("g.external_id = $%d", len(args)+1))
		args = append(args, *filters.gameID)
	}
	if filters.playerID != nil {
		where = append(where, fmt.Sprintf("p.external_id = $%d", len(args)+1))
		args = append(args, *filters.playerID)
	}
	if filters.teamID != nil {
		where = append(where, fmt.Sprintf("t.external_id = $%d", len(args)+1))
		args = append(args, *filters.teamID)
	}
	if filters.season != nil {
		where = append(where, fmt.Sprintf(
			"g.game_date >= make_date($%d, 10, 1) AND g.game_date < make_date($%d + 1, 10, 1)",
			len(args)+1, len(args)+1,
		))
		args = append(args, *filters.season)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ` ORDER BY g.game_date DESC, pgs.points DESC, pgs.id DESC`
	query = addPagination(query, &args, page)

	ctx, cancel := s.requestContext(c)
	defer cancel()
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return databaseError(c, err)
	}
	defer rows.Close()

	data := make([]PlayerGameStat, 0, page.limit)
	for rows.Next() {
		stat, err := scanStat(rows)
		if err != nil {
			return databaseError(c, err)
		}
		data = append(data, stat)
	}
	if err := rows.Err(); err != nil {
		return databaseError(c, err)
	}

	data, meta := trimPage(data, page)
	return c.JSON(paginatedResponse[PlayerGameStat]{Data: data, Meta: meta})
}

func (s *Server) getStat(c *fiber.Ctx) error {
	id, err := parsePathID(c, "id")
	if err != nil {
		return badRequest(c, err.Error())
	}

	ctx, cancel := s.requestContext(c)
	defer cancel()
	stat, err := scanStat(s.db.QueryRow(ctx, statSelect+`
		WHERE pgs.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound(c, "stat row not found")
	}
	if err != nil {
		return databaseError(c, err)
	}
	return c.JSON(stat)
}
