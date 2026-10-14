with totals as (
    select 'player'::text as kind, player_id as entity, season_end_year, phase,
        games_played, games_started, minutes_seconds, fg, fga, fg3, fg3a, ft, fta, orb, drb, reb, ast, stl, blk, tov, pf, pts,
        null::integer as wins, null::integer as losses, null::integer as opponent_pts
    from {{ ref('mart_player_season') }}
    union all
    select 'player_team', player_id || ':' || team_season_id, season_end_year, phase,
        games_played, games_started, minutes_seconds, fg, fga, fg3, fg3a, ft, fta, orb, drb, reb, ast, stl, blk, tov, pf, pts, null, null, null
    from {{ ref('mart_player_team_season') }}
    union all
    select 'team', team_season_id, season_end_year, phase,
        games_played, null, minutes_seconds, fg, fga, fg3, fg3a, ft, fta, orb, drb, reb, ast, stl, blk, tov, pf, pts, wins, losses, opponent_pts
    from {{ ref('mart_team_season') }}
)
select * from totals where
    games_played < 0 or
    minutes_seconds < 0 or
    fg < 0 or
    fga < 0 or
    fg3 < 0 or
    fg3a < 0 or
    ft < 0 or
    fta < 0 or
    orb < 0 or
    drb < 0 or
    reb < 0 or
    ast < 0 or
    stl < 0 or
    blk < 0 or
    tov < 0 or
    pf < 0 or
    pts < 0
    or games_started < 0 or games_started > games_played
    or fg > fga or fg3 > fg or fg3a > fga or fg3 > fg3a or ft > fta
    or orb + drb <> reb
    or pts <> 2 * fg + fg3 + ft
    or wins < 0 or losses < 0 or wins + losses <> games_played or opponent_pts < 0
