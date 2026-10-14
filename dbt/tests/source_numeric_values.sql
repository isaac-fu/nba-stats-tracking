-- Reject nonblank malformed values instead of silently turning invalid data into NULL.
with cells as (
    select 'player' as kind, p.player_id as entity, p.season_end_year,
        j.key, j.value
    from {{ ref('stg_player_season') }} p
    cross join lateral jsonb_each_text(p.source_payload) j
    where j.key in ('g','gs','mp','fg','fga','fg3','fg3a','ft','fta','orb','drb','reb','ast','stl','blk','tov','pf','pts')
    union all
    select 'team', t.source_team_code, t.season_end_year, j.key, j.value
    from {{ ref('stg_team_season') }} t
    cross join lateral jsonb_each_text(t.source_payload) j
    where j.key in ('g','wins','losses','mp','fg','fga','fg3','fg3a','ft','fta','orb','drb','reb','ast','stl','blk','tov','pf','pts','opponent_pts')
)
select * from cells
where nullif(btrim(value), '') is not null
and not (replace(btrim(value), ',', '') ~
    case when key = 'mp' then '^[0-9]+(\.[0-9]+)?$' else '^[0-9]+$' end)
