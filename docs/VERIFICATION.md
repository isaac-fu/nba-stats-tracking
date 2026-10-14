# Verification of the first working pipeline

Initial implementation verified on October 13, 2026; target-machine Docker application checked on October 14, 2026. The application uses a synthetic two-player, two-team sample for season-ending 2024.

## Target-machine Docker application

The user ran `scripts/checkpoints.cmd` and returned the player statistics response for published release `cc2562b5-ef3b-4341-8868-7495732aa1c2`. Read-only checks then confirmed:

- All eight implemented API routes returned 200 through the running service at `localhost:8080`.
- Alex Example has 50 points over four games and 12.5 points per game. Bailey Sample's unavailable steals remain NULL. Player and team comparisons preserve requested order.
- An invalid sort and duplicate comparison IDs returned 400; a missing comparison player returned 404.
- React at `localhost:5173` displayed both leaderboards and comparison tables. The synthetic-data label, percentage formatting, and missing-value dashes were present; switching views and comparing produced no browser warnings or errors.

These checks exercised the product API's normal database connection configuration. Docker build logs and a fresh full dbt test log were not captured during this follow-up.

## Completed checks

- **Python:** 58 parser/client/preview/source-identity tests passed after the October 14 continuation. Three database integration tests also passed again for immutable observations, A-to-B-to-A corrections, failed-parse atomicity, and HTTP 304 reuse.
- **Orchestration:** 12 regression tests passed for artifact validation, required resources, input markers, schema/runtime consistency, and publication ordering.
- **dbt:** A full build passed all 65 selected resources: 11 table models, one seed, and 53 data tests. All eight required serving models were published successfully.
- **Publication failures:** Changing a tested candidate table was rejected by the build seal. An inconsistent raw totals revision failed dbt tests. Both left the published release and serving rows unchanged. Reimporting valid data published a new release with the same two player and two team season rows.
- **Go:** Unit tests, vet, and executable build passed. Database-backed HTTP checks returned 200 for all eight routes, correct all-team totals and numeric rates, NULL for unavailable fields, ordered comparisons, 400 for invalid parameters, and 404 for absent comparison entities.
- **React:** Production build passed. Browser checks verified player/team comparisons, source labels, percentage formatting, missing-value dashes, team search, and switching entity views without console warnings after the final fix. Desktop and mobile layouts were inspected.
- **Contracts/configuration:** OpenAPI validation passed and YAML/JSON versions match. Compose YAML parsed. The original DDL and additive migration were validated; all original schema statements executed in the test database. PowerShell helper scripts passed syntax parsing.
- **Dependencies:** Combined Python/dbt requirements installed and `pip check` reported no broken requirements. The updated artifact checker imported dependencies and validated the contract and original SQL syntax.

The local `inspect` command was exercised with both fixtures: four player rows representing two players, and two team rows. Blank steals/blocks were reported without replacing them with zeros. Inspection also works with third-party Python packages disabled and does not connect to PostgreSQL or HTTP. Regression tests reject mismatched supplemental team years and contradictory source URLs. Ambiguous HTML canonical links remain unverified phase evidence.

## Runtime limits

Docker CLI execution was unavailable in this Codex shell. Windows Application Control blocked startup of the installed native PostgreSQL helper and the local oxlint native binding. No security policy was changed.

For SQL and pipeline integration, tests used an isolated PGlite 0.5.9 runtime reporting PostgreSQL 18.6 on WebAssembly, with a PostgreSQL wire-protocol bridge. The bridge differs from native PostgreSQL connection handling. Database tests disabled prepared statements only for this test environment; the API integration helper used a single connection and simple protocol. Product Go configuration was not changed for the bridge.

The Docker stack targets the project's PostgreSQL 17 image and normal pgx/psycopg behavior. Its running API and React application have now passed the target-machine checks above. The web linter is still a remaining check; run it in the Linux image after building:

```text
docker compose --env-file .env -f docker/compose.yaml run --rm --no-deps web npm run lint
```

Automated Basketball Reference access returned 403. The fetching client's request policy and response handling were tested with controlled responses, but its current live-site HTML compatibility was not established. The included fixtures are synthetic and must not be presented as real NBA results.

Game detail, verified full-season coverage, pre-2005 identity expansion, automatic scheduling, production database roles, and a production web deployment remain later work. Optional Tableau SQL is documented; no external Tableau workbook was created.
