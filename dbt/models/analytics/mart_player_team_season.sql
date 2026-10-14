{{ release_marker() }}
with totals as (
    select {{ release_id() }} as release_id, season_end_year, phase, player_id,
    source_team_token || ':' || season_end_year::text as team_season_id,
    'source_totals'::text as metrics_basis,
    case when count(*) = 1 then 'source_single_team' else 'summed_source_stints' end as source_scope,
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
    from {{ ref('stg_player_season') }}
    where source_team_token not in ('TOT', '2TM', '3TM')
    group by season_end_year, phase, player_id, source_team_token
)
select *, {{ rates() }} from totals
