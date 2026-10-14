{{ release_marker() }}
select {{ release_id() }} as release_id,
    r.parse_batch_id, r.source_table_id, r.row_ordinal,
    r.season_end_year, r.source_phase as phase,
    source_player_id as player_id, source_team_token,
    nullif(btrim(source_payload->>'player_name'), '') as display_name,
    nullif(btrim(source_payload->>'pos'), '') as position_source,
    {{ seconds_value("source_payload->>'mp'") }} as minutes_seconds,
    {{ integer_value("source_payload->>'g'") }} as games_played,
    {{ integer_value("source_payload->>'gs'") }} as games_started,
    {{ integer_value("source_payload->>'fg'") }} as fg,
    {{ integer_value("source_payload->>'fga'") }} as fga,
    {{ integer_value("source_payload->>'fg3'") }} as fg3,
    {{ integer_value("source_payload->>'fg3a'") }} as fg3a,
    {{ integer_value("source_payload->>'ft'") }} as ft,
    {{ integer_value("source_payload->>'fta'") }} as fta,
    {{ integer_value("source_payload->>'orb'") }} as orb,
    {{ integer_value("source_payload->>'drb'") }} as drb,
    {{ integer_value("source_payload->>'reb'") }} as reb,
    {{ integer_value("source_payload->>'ast'") }} as ast,
    {{ integer_value("source_payload->>'stl'") }} as stl,
    {{ integer_value("source_payload->>'blk'") }} as blk,
    {{ integer_value("source_payload->>'tov'") }} as tov,
    {{ integer_value("source_payload->>'pf'") }} as pf,
    {{ integer_value("source_payload->>'pts'") }} as pts,
    r.source_payload
from {{ source('raw', 'player_season_row') }} r
join {{ source('meta', 'release_input') }} i using (parse_batch_id)
where i.release_id = {{ release_id() }}
