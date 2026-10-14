{{ release_marker() }}
with totals as (
    select {{ release_id() }} as release_id, season_end_year, phase,
    source_team_code || ':' || season_end_year::text as team_season_id,
    'source_totals'::text as metrics_basis, games_played::integer, wins::integer, losses::integer,
    minutes_seconds,
    fg::integer, fga::integer, fg3::integer, fg3a::integer, ft::integer, fta::integer, orb::integer, drb::integer, reb::integer, ast::integer, stl::integer, blk::integer, tov::integer, pf::integer, pts::integer, opponent_pts::integer
    from {{ ref('stg_team_season') }}
)
select *, {{ rates(team=true) }} from totals
