{{ release_marker() }}
with seasons as (
    select season_end_year from {{ ref('stg_player_season') }}
    union select season_end_year from {{ ref('stg_team_season') }}
)
select {{ release_id() }} as release_id, season_end_year,
    (season_end_year - 1)::text || '-' || right(season_end_year::text, 2) as season_label,
    null::date as starts_on, null::date as ends_on
from seasons
