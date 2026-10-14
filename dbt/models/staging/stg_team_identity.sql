{{ release_marker() }}
with used as (
    select season_end_year, source_team_code, display_name
    from {{ ref('stg_team_season') }}
    union all
    select season_end_year, source_team_token as source_team_code, null::text as display_name
    from {{ ref('stg_player_season') }} where source_team_token not in ('TOT', '2TM', '3TM')
), teams as (
    select season_end_year, source_team_code, max(display_name) as display_name
    from used group by season_end_year, source_team_code
)
select {{ release_id() }} as release_id, t.*,
    s.franchise_id, s.franchise_name
from teams t
left join {{ ref('team_identity') }} s
on s.source_team_code = t.source_team_code
and t.season_end_year between s.first_season and s.last_season
