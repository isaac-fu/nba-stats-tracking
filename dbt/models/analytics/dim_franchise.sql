{{ release_marker() }}
select distinct {{ release_id() }} as release_id,
    franchise_id, franchise_name as display_name
from {{ ref('stg_team_identity') }} where franchise_id is not null
