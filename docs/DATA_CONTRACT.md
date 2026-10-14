# First pipeline contract

MVP scope: player/team season totals, historical imports, daily refreshes of aggregates for completed games. Game logs are a later extension; capability game_detail_available=false, game_detail_complete=false, observed_games=0, expected_games=NULL.

## Raw contract

The existing root database.sql stays unchanged. Use raw.player_season_row and raw.team_season_totals_row, immutable raw.page_snapshot observations, complete raw.parse_batch transactions, and meta.pipeline_run/meta.fetch_attempt lineage.

- Player JSON values remain strings: player_name,pos,g,gs,mp,fg,fga,fg3,fg3a,ft,fta,orb,drb,reb,ast,stl,blk,tov,pf,pts.
- Team JSON values remain strings: team_name,g,wins,losses,mp,the same totals,opponent_pts.
- Unknown source keys remain in payloads. Missing statistics use blank strings and become NULL in dbt. Numeric conversion belongs to dbt.
- Player/team IDs come from links; preserve TOT/2TM/3TM as aggregate source tokens, not team IDs.
- Synthetic fixture source_name=demo_fixture. Approved real imports source_name=basketball_reference. Never mix these sources within a release.

## Pipeline interface

Ingestion: python -m ingestion.cli inspect --kind players|teams --season YEAR --phase regular|playoffs --file PATH [--source-name SOURCE] [--url URL] previews local files without HTTP or database access; sample imports the bundled fixtures; import --kind players|teams --season YEAR --phase regular|playoffs --file PATH --source-name SOURCE --url URL; fetch --start-season YEAR --end-season YEAR --phase regular|playoffs --allow-live; daily --season YEAR --allow-live.

Basketball Reference imports require a canonical source URL matching kind/year/phase and reject conflicting HTML canonical links when present. Supplemental team links must match the requested year. A missing HTML canonical link does not verify phase; the preview reports that limitation explicitly. Synthetic/generic inputs remain supported without provider-specific claims.

Build: python -m orchestration.pipeline prepare --source demo_fixture prints only release UUID to stdout. Freeze latest successfully parsed observation per page at source_cutoff into meta.release_input. Build run details include source_name and sample_data. Target schema build_<UUID hex>; dbt variable release_id required. Full automated command python -m orchestration.pipeline run --source demo_fixture calls prepare, dbt build, and guarded atomic publication. Project=/workspace/dbt; profiles=/opt/dbt; artifacts=/artifacts/<release UUID>.

Required publishing models with existing analytics table column names: dim_season,dim_player,dim_franchise,dim_team_season,mart_player_season,mart_player_team_season,mart_team_season,release_capability. dbt may calculate generated rates to test parity; publisher omits generated columns from INSERT. Curated reviewed team/franchise CSV seed handles identity; unknown teams fail tests. Player aggregate prefers one all-team row over stints; otherwise one single-team row or null-safe stint sums. metrics_basis=source_totals; source_scope follows existing constraints.

Publication gates: successful complete dbt build+test artifacts for this release/schema, exact input manifest, required models/counts, rate parity and integrity. Atomic FK-ordered inserts plus published pointer swap under advisory lock; failures preserve old release. No analytics mutations after publication.

## HTTP interface

Routes: /health/live; /health/ready; /api/v1/status; /api/v1/seasons; /api/v1/players; /api/v1/teams; /api/v1/comparisons/players; /api/v1/comparisons/teams.

Pin one published release within each request. Use bound SQL parameters and read-only transactions. Do not expose database exception text.

- Status fields: release_id,source_name,sample_data,published_at,source_observed_at. Source metadata comes from build run details on meta.release.build_run_id. Return a clear bootstrap state if no release exists.
- Seasons: {release_id,data:[{season_end_year,season_label,phase}]}.
- Lists: season ending year, phase regular default or playoffs, q, allowlisted sort, limit bounded at 1000. Envelope {release_id,data:[...]}.
- Players: player_id,display_name,season_end_year,phase,games_played,base totals,nullable numeric rates pts_per_game,reb_per_game,ast_per_game,fg_pct,efg_pct,ts_pct,metrics_basis,source_scope.
- Teams: team_season_id,display_name,abbreviation,season_end_year,phase,games_played,wins,losses,win_pct,base totals and nullable numeric rates.
- Comparisons require season and 2-5 unique comma-separated ids plus phase. Preserve request order and return same rows/envelope as lists. Missing entities return 404.
- Bad query parameters return 400. Missing published statistics returns 503. Missing metrics are JSON null, not zero.

React calls same-origin /api/v1 through Vite proxy API_PROXY_TARGET (default http://localhost:8080; Docker http://api:8080). Show source/sample labels, filters, players/teams/comparisons and loading/empty/error states. All displayed statistics come from the API.
