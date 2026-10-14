with errors as (
    select 'player_parent' as issue, m.player_id as entity
    from {{ ref('mart_player_season') }} m
    left join {{ ref('dim_player') }} d using (release_id, player_id)
    left join {{ ref('dim_season') }} s using (release_id, season_end_year)
    where d.player_id is null or s.season_end_year is null
    union all
    select 'player_team_parent', m.player_id || ':' || m.team_season_id
    from {{ ref('mart_player_team_season') }} m
    left join {{ ref('dim_player') }} p using (release_id, player_id)
    left join {{ ref('dim_team_season') }} t using (release_id, team_season_id, season_end_year)
    where p.player_id is null or t.team_season_id is null
    union all
    select 'team_parent', m.team_season_id
    from {{ ref('mart_team_season') }} m
    left join {{ ref('dim_team_season') }} t using (release_id, team_season_id, season_end_year)
    where t.team_season_id is null
    union all
    select 'franchise_or_season_parent', t.team_season_id
    from {{ ref('dim_team_season') }} t
    left join {{ ref('dim_franchise') }} f using (release_id, franchise_id)
    left join {{ ref('dim_season') }} s using (release_id, season_end_year)
    where f.franchise_id is null or s.season_end_year is null
    union all
    select 'capability_parent', c.season_end_year::text || ':' || c.phase
    from {{ ref('release_capability') }} c
    left join {{ ref('dim_season') }} s using (release_id, season_end_year)
    where s.season_end_year is null
)
select * from errors
