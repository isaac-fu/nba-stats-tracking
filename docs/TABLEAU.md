# Optional Tableau comparisons

React already provides comparisons. Tableau is an optional client for exploring the same published analytics; it does not need to run the ingestion or dbt steps.

After publishing a release, connect Tableau Desktop to PostgreSQL on your computer using the database name, user, and password from your local `.env`. Use the host port specified by `DATABASE_PORT` (the example uses 5433). The Docker-only hostname `postgres` is for containers; desktop clients use `localhost`.

Use a dedicated reader role when provisioning a deployment. The current Compose credentials are the existing local development credentials; a separate Tableau login is not provisioned by this checkpoint.

For player comparisons, use one custom SQL query:

```sql
SELECT p.display_name, m.season_end_year, m.phase,
       m.games_played, m.pts_per_game, m.reb_per_game, m.ast_per_game,
       m.fg_pct, m.efg_pct, m.ts_pct,
       r.source_name, r.sample_data, m.release_id
FROM api.current_player_season m
JOIN analytics.dim_player p
  ON p.release_id = m.release_id AND p.player_id = m.player_id
JOIN api.release_metadata r ON r.release_id = m.release_id
```

For team comparisons:

```sql
SELECT t.display_name, t.abbreviation, m.season_end_year, m.phase,
       m.games_played, m.wins, m.losses, m.win_pct,
       m.pts_per_game, m.points_against_per_game,
       m.point_differential_per_game, r.source_name, r.sample_data, m.release_id
FROM api.current_team_season m
JOIN analytics.dim_team_season t
  ON t.release_id = m.release_id AND t.team_season_id = m.team_season_id
JOIN api.release_metadata r ON r.release_id = m.release_id
```

Keep each extract's release ID so separate queries do not accidentally mix publications. Filter by season and phase, put player/team name on Rows, and select comparison measures. Rates such as `fg_pct` and `win_pct` are fractions; format them as percentages. The `sample_data` field identifies invented demonstration results.

No Tableau workbook or external Tableau connection was created during this checkpoint.
