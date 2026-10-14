{{ release_marker() }}
select {{ release_id() }} as release_id,
    source_team_code || ':' || season_end_year::text as team_season_id,
    season_end_year, franchise_id, source_team_code,
    coalesce(display_name, franchise_name) as display_name,
    source_team_code as abbreviation, null::text as conference, null::text as division
from {{ ref('stg_team_identity') }}
