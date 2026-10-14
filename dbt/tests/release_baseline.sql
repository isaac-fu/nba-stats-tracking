-- Every selected season/phase must have both player and team summaries.
-- This gates accidental publication after only half of an import succeeded.
select season_end_year, phase
from {{ ref('release_capability') }}
where not player_season_totals_available or not team_season_totals_available
union all
select null::integer, null::text
where not exists (select 1 from {{ ref('release_capability') }})
   or not exists (select 1 from {{ ref('mart_player_season') }})
   or not exists (select 1 from {{ ref('mart_team_season') }})
