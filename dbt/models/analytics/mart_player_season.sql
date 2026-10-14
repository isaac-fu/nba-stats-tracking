{{ release_marker() }}
with tagged as (
    select *, count(*) filter (where source_team_token in ('TOT', '2TM', '3TM'))
        over (partition by season_end_year, phase, player_id) as all_team_rows
    from {{ ref('stg_player_season') }}
), selected as (
    select * from tagged
    where (all_team_rows > 0 and source_team_token in ('TOT', '2TM', '3TM'))
       or (all_team_rows = 0 and source_team_token not in ('TOT', '2TM', '3TM'))
), totals as (
    select {{ release_id() }} as release_id, season_end_year, phase, player_id,
    'source_totals'::text as metrics_basis,
    case when bool_or(source_team_token in ('TOT', '2TM', '3TM')) then 'source_all_teams'
         when count(*) = 1 then 'source_single_team' else 'summed_source_stints' end as source_scope,
    {{ complete_sum('games_played') }}::integer as games_played,
    {{ complete_sum('games_started') }}::integer as games_started,
    {{ complete_sum('minutes_seconds') }}::bigint as minutes_seconds,
    {{ complete_sum('fg') }}::integer as fg,
    {{ complete_sum('fga') }}::integer as fga,
    {{ complete_sum('fg3') }}::integer as fg3,
    {{ complete_sum('fg3a') }}::integer as fg3a,
    {{ complete_sum('ft') }}::integer as ft,
    {{ complete_sum('fta') }}::integer as fta,
    {{ complete_sum('orb') }}::integer as orb,
    {{ complete_sum('drb') }}::integer as drb,
    {{ complete_sum('reb') }}::integer as reb,
    {{ complete_sum('ast') }}::integer as ast,
    {{ complete_sum('stl') }}::integer as stl,
    {{ complete_sum('blk') }}::integer as blk,
    {{ complete_sum('tov') }}::integer as tov,
    {{ complete_sum('pf') }}::integer as pf,
    {{ complete_sum('pts') }}::integer as pts
    from selected group by season_end_year, phase, player_id
)
select *, {{ rates() }} from totals
