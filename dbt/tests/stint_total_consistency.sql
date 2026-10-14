-- Aggregate rows and stint rows cannot both be summed into a player total.
-- When every stint field is known, source all-team totals must match the stints.
with totals as (
    select season_end_year, phase, player_id,
      max(games_played) filter (where source_team_token in ('TOT','2TM','3TM')) as total_games,
      max(pts) filter (where source_team_token in ('TOT','2TM','3TM')) as total_pts,
      sum(games_played) filter (where source_team_token not in ('TOT','2TM','3TM')) as stint_games,
      sum(pts) filter (where source_team_token not in ('TOT','2TM','3TM')) as stint_pts,
      count(*) filter (where source_team_token not in ('TOT','2TM','3TM')) as stint_count,
      count(pts) filter (where source_team_token not in ('TOT','2TM','3TM')) as known_stint_points
    from {{ ref('stg_player_season') }} group by season_end_year, phase, player_id
)
select * from totals where stint_count > 0 and
    (total_games <> stint_games or
     (known_stint_points = stint_count and total_pts <> stint_pts))
