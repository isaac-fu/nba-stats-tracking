{{ release_marker() }}
with names as (
    select *, row_number() over (partition by player_id
        order by season_end_year desc, phase, row_ordinal, parse_batch_id) as preference
    from {{ ref('stg_player_season') }}
)
select {{ release_id() }} as release_id, player_id, display_name,
    lower(display_name) as search_name, null::date as born_on,
    null::numeric as height_cm, null::numeric as weight_kg, position_source
from names where preference = 1
