package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNoRelease = errors.New("no published release")
var ErrMissingEntity = errors.New("entity absent from this season and phase")

type Release struct {
	ReleaseID        string     `json:"release_id"`
	SourceName       string     `json:"source_name"`
	SampleData       bool       `json:"sample_data"`
	PublishedAt      time.Time  `json:"published_at"`
	SourceObservedAt *time.Time `json:"source_observed_at"`
}

type Season struct {
	SeasonEndYear int    `json:"season_end_year"`
	SeasonLabel   string `json:"season_label"`
	Phase         string `json:"phase"`
}

// Pointers preserve unavailable statistics as JSON null, including rates with
// a zero denominator. A measured zero remains a JSON number.
type Totals struct {
	MinutesSeconds *int64 `json:"minutes_seconds"`
	FG             *int64 `json:"fg"`
	FGA            *int64 `json:"fga"`
	FG3            *int64 `json:"fg3"`
	FG3A           *int64 `json:"fg3a"`
	FT             *int64 `json:"ft"`
	FTA            *int64 `json:"fta"`
	ORB            *int64 `json:"orb"`
	DRB            *int64 `json:"drb"`
	REB            *int64 `json:"reb"`
	AST            *int64 `json:"ast"`
	STL            *int64 `json:"stl"`
	BLK            *int64 `json:"blk"`
	TOV            *int64 `json:"tov"`
	PF             *int64 `json:"pf"`
	PTS            *int64 `json:"pts"`
}

type Rates struct {
	PTSPerGame *float64 `json:"pts_per_game"`
	REBPerGame *float64 `json:"reb_per_game"`
	ASTPerGame *float64 `json:"ast_per_game"`
	FGPct      *float64 `json:"fg_pct"`
	FG3Pct     *float64 `json:"fg3_pct"`
	FTPct      *float64 `json:"ft_pct"`
	EFGPct     *float64 `json:"efg_pct"`
	TSPct      *float64 `json:"ts_pct"`
}

type Player struct {
	PlayerID      string `json:"player_id"`
	DisplayName   string `json:"display_name"`
	SeasonEndYear int    `json:"season_end_year"`
	Phase         string `json:"phase"`
	GamesPlayed   int    `json:"games_played"`
	GamesStarted  *int64 `json:"games_started"`
	Totals
	Rates
	MetricsBasis string `json:"metrics_basis"`
	SourceScope  string `json:"source_scope"`
}

type Team struct {
	TeamSeasonID  string `json:"team_season_id"`
	DisplayName   string `json:"display_name"`
	Abbreviation  string `json:"abbreviation"`
	SeasonEndYear int    `json:"season_end_year"`
	Phase         string `json:"phase"`
	GamesPlayed   int    `json:"games_played"`
	Wins          int    `json:"wins"`
	Losses        int    `json:"losses"`
	Totals
	Rates
	OpponentPTS              *int64   `json:"opponent_pts"`
	WinPct                   *float64 `json:"win_pct"`
	PointsAgainstPerGame     *float64 `json:"points_against_per_game"`
	PointDifferentialPerGame *float64 `json:"point_differential_per_game"`
	MetricsBasis             string   `json:"metrics_basis"`
}

type Envelope[T any] struct {
	ReleaseID string `json:"release_id"`
	Data      []T    `json:"data"`
}

type Filters struct {
	Season int
	Phase  string
	Query  string
	Sort   string
	Limit  int
	IDs    []string
}

type Store interface {
	Status(context.Context) (Release, error)
	Seasons(context.Context) (Envelope[Season], error)
	Players(context.Context, Filters) (Envelope[Player], error)
	Teams(context.Context, Filters) (Envelope[Team], error)
}

type PostgresStore struct{ pool *pgxpool.Pool }

// Pin once inside a read-only repeatable-read transaction. A publication that
// occurs during this request cannot mix dimensions or statistics from releases.
func (s *PostgresStore) begin(ctx context.Context) (pgx.Tx, Release, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, Release{}, err
	}
	var release Release
	err = tx.QueryRow(ctx, `SELECT p.release_id::text, m.source_name, m.sample_data,
		p.published_at, m.source_observed_at
		FROM api.published_release p
		JOIN api.release_metadata m ON m.release_id = p.release_id
		WHERE p.singleton`).Scan(&release.ReleaseID, &release.SourceName, &release.SampleData,
		&release.PublishedAt, &release.SourceObservedAt)
	if err != nil {
		_ = tx.Rollback(context.Background())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, Release{}, ErrNoRelease
		}
		return nil, Release{}, err
	}
	return tx, release, nil
}

func (s *PostgresStore) Status(ctx context.Context) (Release, error) {
	tx, release, err := s.begin(ctx)
	if err != nil {
		return Release{}, err
	}
	defer tx.Rollback(context.Background())
	return release, nil
}

func (s *PostgresStore) Seasons(ctx context.Context) (Envelope[Season], error) {
	tx, release, err := s.begin(ctx)
	if err != nil {
		return Envelope[Season]{}, err
	}
	defer tx.Rollback(context.Background())
	result := Envelope[Season]{ReleaseID: release.ReleaseID, Data: []Season{}}
	rows, err := tx.Query(ctx, `SELECT s.season_end_year, s.season_label, c.phase
		FROM analytics.dim_season s
		JOIN analytics.release_capability c USING (release_id, season_end_year)
		WHERE s.release_id = $1::uuid
		ORDER BY s.season_end_year DESC, c.phase`, release.ReleaseID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var season Season
		if err := rows.Scan(&season.SeasonEndYear, &season.SeasonLabel, &season.Phase); err != nil {
			return result, err
		}
		result.Data = append(result.Data, season)
	}
	return result, rows.Err()
}

const totalColumns = `m.minutes_seconds, m.fg, m.fga, m.fg3, m.fg3a, m.ft, m.fta,
	m.orb, m.drb, m.reb, m.ast, m.stl, m.blk, m.tov, m.pf, m.pts`
const rateColumns = `m.pts_per_game::float8, m.reb_per_game::float8, m.ast_per_game::float8,
	m.fg_pct::float8, m.fg3_pct::float8, m.ft_pct::float8, m.efg_pct::float8, m.ts_pct::float8`

func totalDestinations(t *Totals) []any {
	return []any{&t.MinutesSeconds, &t.FG, &t.FGA, &t.FG3, &t.FG3A, &t.FT, &t.FTA,
		&t.ORB, &t.DRB, &t.REB, &t.AST, &t.STL, &t.BLK, &t.TOV, &t.PF, &t.PTS}
}
func rateDestinations(r *Rates) []any {
	return []any{&r.PTSPerGame, &r.REBPerGame, &r.ASTPerGame, &r.FGPct, &r.FG3Pct, &r.FTPct, &r.EFGPct, &r.TSPct}
}

var playerSorts = map[string]string{
	"pts_per_game": "m.pts_per_game DESC NULLS LAST", "pts": "m.pts DESC NULLS LAST",
	"reb_per_game": "m.reb_per_game DESC NULLS LAST", "ast_per_game": "m.ast_per_game DESC NULLS LAST",
	"ts_pct": "m.ts_pct DESC NULLS LAST", "games_played": "m.games_played DESC", "name": "d.display_name ASC",
}
var teamSorts = map[string]string{
	"pts_per_game": "m.pts_per_game DESC NULLS LAST", "pts": "m.pts DESC NULLS LAST",
	"reb_per_game": "m.reb_per_game DESC NULLS LAST", "ast_per_game": "m.ast_per_game DESC NULLS LAST",
	"ts_pct": "m.ts_pct DESC NULLS LAST", "games_played": "m.games_played DESC", "name": "d.display_name ASC",
	"wins": "m.wins DESC", "win_pct": "m.win_pct DESC NULLS LAST",
}

func (s *PostgresStore) Players(ctx context.Context, f Filters) (Envelope[Player], error) {
	tx, release, err := s.begin(ctx)
	if err != nil {
		return Envelope[Player]{}, err
	}
	defer tx.Rollback(context.Background())
	result := Envelope[Player]{ReleaseID: release.ReleaseID, Data: []Player{}}
	order, ok := playerSorts[f.Sort]
	if !ok {
		return result, fmt.Errorf("invalid sort")
	}
	rows, err := tx.Query(ctx, `SELECT d.player_id, d.display_name, m.season_end_year, m.phase,
		m.games_played, m.games_started, `+totalColumns+`, `+rateColumns+`, m.metrics_basis, m.source_scope
		FROM analytics.mart_player_season m
		JOIN analytics.dim_player d USING (release_id, player_id)
		WHERE m.release_id = $1::uuid AND m.season_end_year = $2 AND m.phase = $3
		AND position(lower($4) in lower(d.display_name)) > 0
		AND ($5::text[] IS NULL OR d.player_id = ANY($5::text[]))
		ORDER BY `+order+`, d.player_id LIMIT $6`, release.ReleaseID, f.Season, f.Phase, f.Query, f.IDs, f.Limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var p Player
		dest := []any{&p.PlayerID, &p.DisplayName, &p.SeasonEndYear, &p.Phase, &p.GamesPlayed, &p.GamesStarted}
		dest = append(dest, totalDestinations(&p.Totals)...)
		dest = append(dest, rateDestinations(&p.Rates)...)
		dest = append(dest, &p.MetricsBasis, &p.SourceScope)
		if err := rows.Scan(dest...); err != nil {
			return result, err
		}
		result.Data = append(result.Data, p)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(f.IDs) > 0 {
		indexed := make(map[string]Player, len(result.Data))
		for _, row := range result.Data {
			indexed[row.PlayerID] = row
		}
		result.Data = []Player{}
		for _, id := range f.IDs {
			row, found := indexed[id]
			if !found {
				return result, ErrMissingEntity
			}
			result.Data = append(result.Data, row)
		}
	}
	return result, nil
}

func (s *PostgresStore) Teams(ctx context.Context, f Filters) (Envelope[Team], error) {
	tx, release, err := s.begin(ctx)
	if err != nil {
		return Envelope[Team]{}, err
	}
	defer tx.Rollback(context.Background())
	result := Envelope[Team]{ReleaseID: release.ReleaseID, Data: []Team{}}
	order, ok := teamSorts[f.Sort]
	if !ok {
		return result, fmt.Errorf("invalid sort")
	}
	rows, err := tx.Query(ctx, `SELECT d.team_season_id, d.display_name, d.abbreviation,
		m.season_end_year, m.phase, m.games_played, m.wins, m.losses,
		`+totalColumns+`, `+rateColumns+`, m.opponent_pts, m.win_pct::float8,
		m.points_against_per_game::float8, m.point_differential_per_game::float8, m.metrics_basis
		FROM analytics.mart_team_season m
		JOIN analytics.dim_team_season d USING (release_id, team_season_id)
		WHERE m.release_id = $1::uuid AND m.season_end_year = $2 AND m.phase = $3
		AND position(lower($4) in lower(d.display_name || ' ' || d.abbreviation)) > 0
		AND ($5::text[] IS NULL OR d.team_season_id = ANY($5::text[]))
		ORDER BY `+order+`, d.team_season_id LIMIT $6`, release.ReleaseID, f.Season, f.Phase, f.Query, f.IDs, f.Limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var t Team
		dest := []any{&t.TeamSeasonID, &t.DisplayName, &t.Abbreviation, &t.SeasonEndYear, &t.Phase, &t.GamesPlayed, &t.Wins, &t.Losses}
		dest = append(dest, totalDestinations(&t.Totals)...)
		dest = append(dest, rateDestinations(&t.Rates)...)
		dest = append(dest, &t.OpponentPTS, &t.WinPct, &t.PointsAgainstPerGame, &t.PointDifferentialPerGame, &t.MetricsBasis)
		if err := rows.Scan(dest...); err != nil {
			return result, err
		}
		result.Data = append(result.Data, t)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(f.IDs) > 0 {
		indexed := make(map[string]Team, len(result.Data))
		for _, row := range result.Data {
			indexed[row.TeamSeasonID] = row
		}
		result.Data = []Team{}
		for _, id := range f.IDs {
			row, found := indexed[id]
			if !found {
				return result, ErrMissingEntity
			}
			result.Data = append(result.Data, row)
		}
	}
	return result, nil
}
