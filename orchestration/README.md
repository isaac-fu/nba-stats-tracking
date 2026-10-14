# Building and publishing releases

Dagster libraries are installed, but scheduling is optional at this stage. The CLI here is the common workflow that a scheduler or Dagster job can call after ingestion succeeds.

```text
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline python -m orchestration.pipeline run --source demo_fixture
```

For approved real imports, use `--source basketball_reference`. Those releases do not include synthetic sample inputs. Add repeatable `--season 2024 --season 2025` options to restrict the candidate to those seasons; the published release represents that selected scope, rather than merging rows from an older publication.

## What runs, in order

1. Apply the additive metadata-view migration in `database/migrations`. Initial `database.sql` is not rerun.
2. Create a candidate and a build run. Capture the actual installed dbt versions, code hash, and team identity seed hash.
3. Freeze one latest accepted, successfully parsed observation per source page at the build cutoff into `meta.release_input`. Validate raw parse row counts and reject competing logical pages for the same kind/season/phase.
4. Write `build_context.json` into `/artifacts/<release UUID>`. Run a full `dbt build` in an isolated build schema. Its models read only the frozen inputs.
5. Check complete same-invocation manifest/run-results artifacts, all enabled models/seeds/tests, required serving models, source/input hashes, schema, runtime versions, and success/pass status. Seal each serving table's count and SHA-256 fingerprint in `build_seal.json`; record that seal's hash in build-run metadata.
6. Acquire a PostgreSQL advisory transaction lock. Check the serving pointer still equals the candidate's previous pointer. Lock the build tables and confirm their contents match the recorded successful build. Copy explicitly listed nongenerated columns in foreign-key order, verify counts and generated-rate parity, record publication metadata, and swap the pointer in the same transaction.

Any build/test/copy failure preserves the previous published release. Failed candidate schemas and artifacts are kept for diagnosis. They can be removed later under an explicit retention policy; this implementation never drops schemas or deletes raw observations automatically.

## Separate learning checkpoints

These commands use the same workflow in visible parts:

```text
python -m orchestration.pipeline prepare --source demo_fixture
python -m orchestration.pipeline build --release UUID --artifacts /artifacts/UUID
python -m orchestration.pipeline publish --release UUID --artifacts /artifacts/UUID
```

Run each through the pipeline service, replacing UUID with the value printed by prepare. Prepare prints only a UUID to stdout; operational logs go to stderr. A failed candidate is not reused: prepare a new release after correcting the problem.

The API can read `source_name` and `sample_data` through `api.release_metadata` without receiving raw-table access. Every sample should be visibly labeled as synthetic in the UI.

## Regression checks

```text
python -m unittest discover -s orchestration/tests -v
```

Artifact-gate tests reject incomplete, selected, mismatched, wrong-schema, and failed-test builds. A publisher ordering test confirms failed copy validation occurs before pointer writes. Full integration validation must additionally run the sample against a real PostgreSQL instance and confirm serving results.

This local MVP uses the configured database user. Separate ingestor, builder, publisher, API, and Tableau roles are a later infrastructure checkpoint; the API uses read-only request transactions already.
