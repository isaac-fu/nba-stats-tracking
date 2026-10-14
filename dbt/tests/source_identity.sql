with errors as (
    select 'player_identity' as issue, coalesce(player_id, '<NULL>') as entity,
        season_end_year from {{ ref('stg_player_season') }}
    where nullif(btrim(player_id),'') is null or display_name is null
       or nullif(btrim(source_team_token),'') is null
       or season_end_year < 1947 or phase not in ('regular','playoffs')
       or games_played is null
    union all
    select 'team_identity', coalesce(source_team_code,'<NULL>'), season_end_year
    from {{ ref('stg_team_season') }}
    where nullif(btrim(source_team_code),'') is null or display_name is null
       or games_played is null or wins is null or losses is null
       or season_end_year < 1947 or phase not in ('regular','playoffs')
    union all
    select 'unmapped_team_or_seed_overlap', source_team_code, season_end_year
    from {{ ref('stg_team_identity') }}
    group by source_team_code, season_end_year
    having count(*) <> 1 or count(franchise_id) <> 1
    union all
    select 'multiple_all_team_rows', player_id, season_end_year
    from {{ ref('stg_player_season') }}
    where source_team_token in ('TOT','2TM','3TM')
    group by player_id, season_end_year, phase having count(*) > 1
    union all
    select 'duplicate_team_summary', source_team_code, season_end_year
    from {{ ref('stg_team_season') }}
    group by source_team_code, season_end_year, phase having count(*) > 1
)
select * from errors
