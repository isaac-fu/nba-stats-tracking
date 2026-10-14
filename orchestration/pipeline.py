"""Freeze inputs, build/test in dbt, then publish a coherent release atomically.

Run with ``python -m orchestration.pipeline run --source demo_fixture``.
All operational messages go to stderr; successful prepare/run prints one UUID.
"""

from __future__ import annotations

import argparse
from contextlib import contextmanager
from datetime import datetime, timezone
from hashlib import sha256
from importlib import metadata
import json
import os
from pathlib import Path
import subprocess
import sys
from typing import Any
from uuid import UUID, uuid4

import psycopg
from psycopg import sql
from psycopg.rows import dict_row


ROOT = Path(__file__).resolve().parents[1]
MODELS = (
    "dim_season", "dim_player", "dim_franchise", "dim_team_season",
    "mart_player_season", "mart_player_team_season", "mart_team_season",
    "release_capability",
)
STATS = "fg fga fg3 fg3a ft fta orb drb reb ast stl blk tov pf pts".split()
PLAYER_COLUMNS = [
    "release_id", "season_end_year", "phase", "player_id", "metrics_basis",
    "source_scope", "games_played", "games_started", "minutes_seconds", *STATS,
]
TEAM_COLUMNS = [
    "release_id", "season_end_year", "phase", "team_season_id", "metrics_basis",
    "games_played", "wins", "losses", "minutes_seconds", *STATS, "opponent_pts",
]
COPY_COLUMNS = {
    "dim_season": ["release_id", "season_end_year", "season_label", "starts_on", "ends_on"],
    "dim_player": [
        "release_id", "player_id", "display_name", "search_name", "born_on",
        "height_cm", "weight_kg", "position_source",
    ],
    "dim_franchise": ["release_id", "franchise_id", "display_name"],
    "dim_team_season": [
        "release_id", "team_season_id", "season_end_year", "franchise_id",
        "source_team_code", "display_name", "abbreviation", "conference", "division",
    ],
    "mart_player_season": PLAYER_COLUMNS,
    "mart_player_team_season": [*PLAYER_COLUMNS, "team_season_id"],
    "mart_team_season": TEAM_COLUMNS,
    "release_capability": [
        "release_id", "season_end_year", "phase", "player_season_totals_available",
        "team_season_totals_available", "game_detail_available", "game_detail_complete",
        "observed_games", "expected_games", "coverage_starts_on", "coverage_through_date",
        "missing_fields", "coverage_notes",
    ],
}
KEY_COLUMNS = {
    "mart_player_season": ["release_id", "season_end_year", "phase", "player_id"],
    "mart_player_team_season": [
        "release_id", "season_end_year", "phase", "player_id", "team_season_id",
    ],
    "mart_team_season": ["release_id", "season_end_year", "phase", "team_season_id"],
}
RATES = [
    "pts_per_game", "reb_per_game", "ast_per_game", "pts_per36", "reb_per36",
    "ast_per36", "fg_pct", "fg3_pct", "ft_pct", "efg_pct", "ts_pct",
]
TEAM_RATES = [
    *RATES, "win_pct", "points_for_per_game", "points_against_per_game",
    "point_differential_per_game",
]
RAW_TABLES = ("player_season_row", "team_season_totals_row")
METRIC_CONTRACT_VERSION = "1.0.0"


class PipelineError(RuntimeError):
    """A validation gate failed; publication has not changed the serving pointer."""


def canonical_bytes(value: Any) -> bytes:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False).encode()


def hash_json(value: Any) -> str:
    return sha256(canonical_bytes(value)).hexdigest()


def code_hash() -> str:
    files = sorted([
        *ROOT.joinpath("dbt").rglob("*.sql"),
        *ROOT.joinpath("dbt").rglob("*.yml"),
        *ROOT.joinpath("orchestration").rglob("*.py"),
    ])
    parts = [
        {"path": path.relative_to(ROOT).as_posix(), "sha256": sha256(path.read_bytes()).hexdigest()}
        for path in files
    ]
    return hash_json(parts)


def seed_hash() -> str:
    return hash_json([
        {"path": path.relative_to(ROOT).as_posix(), "sha256": sha256(path.read_bytes()).hexdigest()}
        for path in sorted(ROOT.joinpath("dbt", "seeds").glob("*.csv"))
    ])


def installed_versions() -> tuple[str, str]:
    try:
        return metadata.version("dbt-core"), metadata.version("dbt-postgres")
    except metadata.PackageNotFoundError as exc:
        raise PipelineError("Run this command with the pipeline image, which includes dbt.") from exc


@contextmanager
def connection():
    # Uses PGHOST/PGPORT/PGDATABASE/PGUSER/PGPASSWORD supplied by Docker.
    with psycopg.connect(connect_timeout=10, row_factory=dict_row) as conn:
        yield conn


def migrate() -> None:
    """Apply additive idempotent serving metadata migration; never rerun initial DDL."""
    with connection() as conn:
        conn.execute("SELECT pg_advisory_xact_lock(81421, 2)")
        for path in sorted(ROOT.joinpath("database", "migrations").glob("*.sql")):
            conn.execute(path.read_text(encoding="utf-8"))


def _input_rows(conn, release_id: UUID) -> list[dict[str, Any]]:
    rows = conn.execute(
        """SELECT page_id, page_snapshot_id, parse_batch_id
           FROM meta.release_input WHERE release_id = %s ORDER BY page_id""",
        (release_id,),
    ).fetchall()
    return [
        {"page_id": row["page_id"], "page_snapshot_id": row["page_snapshot_id"],
         "parse_batch_id": str(row["parse_batch_id"])}
        for row in rows
    ]


def _validate_raw_counts(conn, rows: list[dict[str, Any]]) -> None:
    for item in rows:
        batch = conn.execute(
            "SELECT table_row_counts FROM raw.parse_batch WHERE parse_batch_id = %s",
            (UUID(item["parse_batch_id"]),),
        ).fetchone()
        declared = batch["table_row_counts"]
        observed_total = 0
        for table in RAW_TABLES:
            count = conn.execute(
                sql.SQL("SELECT count(*) AS n FROM raw.{} WHERE parse_batch_id = %s")
                .format(sql.Identifier(table)), (UUID(item["parse_batch_id"]),),
            ).fetchone()["n"]
            expected = declared.get(table, declared.get("raw." + table, 0))
            if not isinstance(expected, int) or isinstance(expected, bool) or expected != count:
                raise PipelineError(f"Parse batch row-count contract failed for {table}.")
            observed_total += count
        if observed_total == 0:
            raise PipelineError("A frozen summary input contains no rows.")


def prepare(source: str, seasons: list[int] | None = None) -> UUID:
    """Freeze latest complete accepted source observations at one cutoff."""
    if source not in {"demo_fixture", "basketball_reference"}:
        raise PipelineError("Source must be demo_fixture or basketball_reference.")
    migrate()
    release_id = uuid4()
    run_id = uuid4()
    cutoff = datetime.now(timezone.utc)
    dbt_version, adapter_version = installed_versions()
    current_code_hash, current_seed_hash = code_hash(), seed_hash()
    config = {"source_name": source, "seasons": sorted(set(seasons or []))}
    with connection() as conn:
        selected = conn.execute(
            """SELECT DISTINCT ON (p.page_id)
                       p.page_id, s.page_snapshot_id, b.parse_batch_id,
                       p.page_kind, p.season_end_year, p.source_phase
                FROM raw.source_page p
                JOIN raw.page_snapshot s USING (page_id)
                JOIN meta.pipeline_run ingest ON ingest.run_id = s.ingest_run_id
                JOIN raw.parse_batch b USING (page_snapshot_id)
                JOIN meta.pipeline_run parsed ON parsed.run_id = b.parse_run_id
                WHERE p.source_name = %s
                  AND p.page_kind IN ('player_season_totals','team_season_totals')
                  AND s.fetched_at <= %s AND b.completed_at <= %s
                  AND ingest.state = 'succeeded' AND parsed.state = 'succeeded'
                  AND b.raw_contract_version = 1
                  AND (%s::integer[] IS NULL OR p.season_end_year = ANY(%s::integer[]))
                  AND EXISTS (
                      SELECT 1 FROM meta.fetch_attempt a
                      WHERE a.page_id = p.page_id AND a.page_snapshot_id = s.page_snapshot_id
                        AND a.ingest_run_id = s.ingest_run_id AND a.outcome = 'accepted')
                ORDER BY p.page_id, s.fetched_at DESC, s.page_snapshot_id DESC,
                         b.completed_at DESC, b.parse_batch_id DESC""",
            (source, cutoff, cutoff, seasons or None, seasons or None),
        ).fetchall()
        if not selected:
            raise PipelineError("No successfully ingested and parsed inputs exist for this source.")
        slots = [(r["page_kind"], r["season_end_year"], r["source_phase"]) for r in selected]
        if len(slots) != len(set(slots)):
            raise PipelineError("Multiple logical source pages claim the same kind/season/phase.")
        if any(year is None or phase not in {"regular", "playoffs"} for _, year, phase in slots):
            raise PipelineError("Source pages require a valid season and phase.")
        rows = [
            {"page_id": r["page_id"], "page_snapshot_id": r["page_snapshot_id"],
             "parse_batch_id": str(r["parse_batch_id"])} for r in selected
        ]
        _validate_raw_counts(conn, rows)
        input_hash = hash_json(rows)
        previous = conn.execute("SELECT release_id FROM api.published_release WHERE singleton").fetchone()
        previous_id = previous["release_id"] if previous else None
        details = {
            **config, "sample_data": source == "demo_fixture", "release_id": str(release_id),
            "schema": "build_" + release_id.hex, "input_pages": len(rows),
        }
        conn.execute(
            """INSERT INTO meta.pipeline_run
                (run_id, job_kind, state, code_sha, config_sha256, details)
                VALUES (%s,'build','running',%s,%s,%s::jsonb)""",
            (run_id, current_code_hash, hash_json(config), json.dumps(details)),
        )
        conn.execute(
            """INSERT INTO meta.release
                 (release_id, build_run_id, status, source_cutoff, code_sha, dbt_version,
                  dbt_adapter_version, metric_contract_version, seed_manifest_sha256,
                  input_manifest_sha256, previous_release_id)
                VALUES (%s,%s,'candidate',%s,%s,%s,%s,%s,%s,%s,%s)""",
            (release_id, run_id, cutoff, current_code_hash, dbt_version, adapter_version,
             METRIC_CONTRACT_VERSION, current_seed_hash, input_hash, previous_id),
        )
        for row in rows:
            conn.execute(
                """INSERT INTO meta.release_input
                    (release_id,page_id,page_snapshot_id,parse_batch_id) VALUES (%s,%s,%s,%s)""",
                (release_id, row["page_id"], row["page_snapshot_id"], UUID(row["parse_batch_id"])),
            )
    return release_id


def context_for(release_id: UUID) -> dict[str, Any]:
    with connection() as conn:
        release = conn.execute(
            """SELECT r.*, b.details FROM meta.release r
               JOIN meta.pipeline_run b ON b.run_id = r.build_run_id
               WHERE r.release_id = %s""", (release_id,),
        ).fetchone()
        if release is None or release["status"] != "candidate":
            raise PipelineError("A candidate release is required.")
        rows = _input_rows(conn, release_id)
        if hash_json(rows) != release["input_manifest_sha256"]:
            raise PipelineError("Frozen release inputs changed after prepare.")
        return {
            "release_id": str(release_id), "schema": "build_" + release_id.hex,
            "input_manifest_sha256": release["input_manifest_sha256"], "inputs": rows,
            "code_sha": release["code_sha"], "seed_manifest_sha256": release["seed_manifest_sha256"],
            "source_name": release["details"]["source_name"],
            "sample_data": release["details"]["sample_data"],
            "dbt_version": release["dbt_version"], "dbt_adapter_version": release["dbt_adapter_version"],
        }


def write_context(release_id: UUID, artifact_dir: Path) -> dict[str, Any]:
    context = context_for(release_id)
    artifact_dir.mkdir(parents=True, exist_ok=True)
    temporary = artifact_dir / "build_context.tmp"
    temporary.write_bytes(canonical_bytes(context))
    temporary.replace(artifact_dir / "build_context.json")
    return context


def validate_artifacts(artifact_dir: Path, context: dict[str, Any]) -> dict[str, Any]:
    """Require the complete same-invocation dbt build, not a lone successful model."""
    try:
        stored = json.loads(artifact_dir.joinpath("build_context.json").read_text())
        manifest = json.loads(artifact_dir.joinpath("manifest.json").read_text())
        results = json.loads(artifact_dir.joinpath("run_results.json").read_text())
    except (OSError, ValueError) as exc:
        raise PipelineError("Complete build_context, manifest, and run_results artifacts are required.") from exc
    if stored != context:
        raise PipelineError("Artifact context does not match this release's frozen inputs.")
    if context["code_sha"] != code_hash() or context["seed_manifest_sha256"] != seed_hash():
        raise PipelineError("Model/publisher code or identity seeds changed after prepare.")
    versions = installed_versions()
    if versions != (context["dbt_version"], context["dbt_adapter_version"]):
        raise PipelineError("The pipeline runtime differs from the candidate's recorded dbt versions.")
    if manifest.get("metadata", {}).get("dbt_version") != context["dbt_version"]:
        raise PipelineError("The dbt manifest version does not match the candidate.")
    invocation = manifest.get("metadata", {}).get("invocation_id")
    if not invocation or invocation != results.get("metadata", {}).get("invocation_id"):
        raise PipelineError("Manifest and run_results are from different dbt invocations.")
    args = results.get("args", {})
    if args.get("which") != "build":
        raise PipelineError("Artifacts must be produced by a full dbt build command.")
    if args.get("select") or args.get("exclude"):
        raise PipelineError("Selected or excluded dbt nodes cannot prove a complete release.")
    nodes = manifest.get("nodes", {})
    relevant = {
        key: node for key, node in nodes.items()
        if node.get("resource_type") in {"model", "seed", "test"}
        and node.get("package_name") == "nba_stats"
        and node.get("config", {}).get("enabled", True)
    }
    by_id = {item.get("unique_id"): item for item in results.get("results", [])}
    if not relevant or set(relevant) - set(by_id):
        raise PipelineError("dbt results are missing models, seeds, or data tests.")
    required = {f"model.nba_stats.{name}" for name in MODELS}
    if required - set(relevant):
        raise PipelineError("The manifest lacks required serving models.")
    if not any(n.get("resource_type") == "test" for n in relevant.values()):
        raise PipelineError("A release must include data tests.")
    marker = f"release_id={context['release_id']} input_manifest_sha256={context['input_manifest_sha256']}"
    for key, node in relevant.items():
        item = by_id[key]
        expected_status = "pass" if node["resource_type"] == "test" else "success"
        if item.get("status") != expected_status:
            raise PipelineError(f"dbt node {key} did not {expected_status}.")
        if node["resource_type"] in {"model", "seed"} and node.get("schema") != context["schema"]:
            raise PipelineError("A dbt node used a schema outside the isolated candidate build.")
        if node["resource_type"] == "model" and marker not in node.get("compiled_code", ""):
            raise PipelineError("A compiled model does not identify this exact input freeze.")
    return {
        "manifest_sha256": sha256(artifact_dir.joinpath("manifest.json").read_bytes()).hexdigest(),
        "run_results_sha256": sha256(artifact_dir.joinpath("run_results.json").read_bytes()).hexdigest(),
        "dbt_invocation_id": invocation, "nodes": len(relevant),
    }


def _count_and_verify(conn, schema: str, model: str, release_id: UUID) -> int:
    row = conn.execute(
        sql.SQL("SELECT count(*) AS n, count(*) FILTER (WHERE release_id IS DISTINCT FROM %s) AS wrong FROM {}.{}")
        .format(sql.Identifier(schema), sql.Identifier(model)), (release_id,),
    ).fetchone()
    if row["wrong"]:
        raise PipelineError(f"Build table {model} contains another release's rows.")
    if not row["n"]:
        raise PipelineError(f"Build table {model} is empty.")
    return row["n"]


def _lock_build_tables(conn, schema: str) -> None:
    """Prevent a concurrent builder from modifying a table while it is sealed/copied."""
    tables = sql.SQL(", ").join(
        sql.SQL("{}.{}").format(sql.Identifier(schema), sql.Identifier(model))
        for model in MODELS
    )
    conn.execute(sql.SQL("LOCK TABLE {} IN SHARE MODE").format(tables))


def _fingerprints(conn, schema: str, release_id: UUID) -> dict[str, dict[str, Any]]:
    """Hash stable PostgreSQL JSON text, preserving NULLs and exact numeric values.

    Sorting canonical JSONB text in C collation avoids dependence on insertion
    order or locale. JSONB renders UUIDs/dates consistently; numeric values stay
    decimal strings rather than passing through Python floating-point conversion.
    """
    result = {}
    for model in MODELS:
        count = _count_and_verify(conn, schema, model, release_id)
        digest = sha256()
        rows = conn.execute(
            sql.SQL(
                "SELECT to_jsonb(t)::text AS canonical_row FROM {}.{} t "
                'ORDER BY to_jsonb(t)::text COLLATE "C"'
            ).format(sql.Identifier(schema), sql.Identifier(model)),
        )
        for row in rows:
            digest.update(row["canonical_row"].encode("utf-8"))
            digest.update(b"\n")
        result[model] = {"count": count, "sha256": digest.hexdigest()}
    return result


def seal_build(release_id: UUID, artifact_dir: Path) -> None:
    """Record fingerprints only after full build artifacts and data tests pass."""
    context = context_for(release_id)
    artifact_report = validate_artifacts(artifact_dir, context)
    with connection() as conn:
        release = conn.execute(
            "SELECT build_run_id, status FROM meta.release WHERE release_id = %s FOR UPDATE",
            (release_id,),
        ).fetchone()
        if release["status"] != "candidate":
            raise PipelineError("Only an unpublished candidate can be sealed.")
        _lock_build_tables(conn, context["schema"])
        tables = _fingerprints(conn, context["schema"], release_id)
        seal = {"format_version": 1, "context": context, "artifacts": artifact_report, "tables": tables}
        seal_bytes = canonical_bytes(seal)
        temporary = artifact_dir / "build_seal.tmp"
        temporary.write_bytes(seal_bytes)
        temporary.replace(artifact_dir / "build_seal.json")
        conn.execute(
            """UPDATE meta.pipeline_run SET details=details || %s::jsonb WHERE run_id = %s""",
            (json.dumps({"build_seal_sha256": sha256(seal_bytes).hexdigest()}), release["build_run_id"]),
        )


def _read_seal(conn, release, artifact_dir: Path, context: dict[str, Any],
               artifact_report: dict[str, Any]) -> dict[str, Any]:
    try:
        seal_bytes = (artifact_dir / "build_seal.json").read_bytes()
        seal = json.loads(seal_bytes)
    except (OSError, ValueError) as exc:
        raise PipelineError("A successful pipeline build's table fingerprint seal is required.") from exc
    recorded = conn.execute(
        "SELECT details->>'build_seal_sha256' AS seal_hash FROM meta.pipeline_run WHERE run_id=%s",
        (release["build_run_id"],),
    ).fetchone()["seal_hash"]
    if not recorded or recorded != sha256(seal_bytes).hexdigest():
        raise PipelineError("The build seal differs from the recorded successful build.")
    if seal.get("format_version") != 1 or seal.get("context") != context or seal.get("artifacts") != artifact_report:
        raise PipelineError("The build seal does not match the release or dbt artifacts.")
    return seal


def _parity(conn, schema: str, model: str, release_id: UUID) -> None:
    keys = KEY_COLUMNS[model]
    rates = TEAM_RATES if model == "mart_team_season" else RATES
    mismatch = sql.SQL(" OR ").join(
        sql.SQL("a.{} IS DISTINCT FROM b.{}").format(sql.Identifier(name), sql.Identifier(name))
        for name in rates
    )
    query = sql.SQL(
        "SELECT count(*) AS n FROM analytics.{} a JOIN {}.{} b USING ({}) "
        "WHERE a.release_id = %s AND ({})"
    ).format(
        sql.Identifier(model), sql.Identifier(schema), sql.Identifier(model),
        sql.SQL(",").join(map(sql.Identifier, keys)), mismatch,
    )
    if conn.execute(query, (release_id,)).fetchone()["n"]:
        raise PipelineError(f"Generated serving rates differ from dbt rates for {model}.")


def publish(release_id: UUID, artifact_dir: Path) -> dict[str, int]:
    """Copy validated serving models and swap the pointer in one locked transaction."""
    context = context_for(release_id)
    report = validate_artifacts(artifact_dir, context)
    schema = context["schema"]
    counts = {}
    with connection() as conn:
        conn.execute("SELECT pg_advisory_xact_lock(81421, 1)")
        release = conn.execute(
            "SELECT * FROM meta.release WHERE release_id = %s FOR UPDATE", (release_id,),
        ).fetchone()
        if release["status"] != "candidate":
            raise PipelineError("This release has already changed state; it cannot be republished.")
        previous = conn.execute(
            "SELECT release_id FROM api.published_release WHERE singleton FOR UPDATE",
        ).fetchone()
        previous_id = previous["release_id"] if previous else None
        if previous_id != release["previous_release_id"]:
            raise PipelineError("Another release was published after prepare; rebuild against the current pointer.")
        if hash_json(_input_rows(conn, release_id)) != context["input_manifest_sha256"]:
            raise PipelineError("Frozen inputs changed before publication.")
        seal = _read_seal(conn, release, artifact_dir, context, report)
        _lock_build_tables(conn, schema)
        fingerprints = _fingerprints(conn, schema, release_id)
        if fingerprints != seal["tables"]:
            raise PipelineError("Build table contents changed after successful dbt tests.")
        # A single transaction covers all copies. A FK/check/count/parity failure
        # rolls back these inserts along with release status and serving pointer.
        for model in MODELS:
            expected = fingerprints[model]["count"]
            columns = sql.SQL(", ").join(map(sql.Identifier, COPY_COLUMNS[model]))
            cursor = conn.execute(
                sql.SQL("INSERT INTO analytics.{} ({}) SELECT {} FROM {}.{} WHERE release_id = %s")
                .format(sql.Identifier(model), columns, columns, sql.Identifier(schema), sql.Identifier(model)),
                (release_id,),
            )
            if cursor.rowcount != expected:
                raise PipelineError(f"Publication row-count mismatch for {model}.")
            counts[model] = expected
            if model in KEY_COLUMNS:
                _parity(conn, schema, model, release_id)
        # Deliberate aggregate-only contract; do not publish fictitious game coverage.
        invalid_coverage = conn.execute(
            """SELECT count(*) AS n FROM analytics.release_capability
               WHERE release_id = %s AND
                 (NOT player_season_totals_available OR NOT team_season_totals_available
                  OR game_detail_available OR game_detail_complete OR observed_games <> 0
                  OR expected_games IS NOT NULL)""", (release_id,),
        ).fetchone()["n"]
        if invalid_coverage:
            raise PipelineError("Published capabilities contradict the aggregate-only release scope.")
        report["row_counts"] = counts
        report["input_manifest_sha256"] = context["input_manifest_sha256"]
        quality_path = artifact_dir / "quality_report.json"
        quality_path.write_bytes(canonical_bytes(report))
        conn.execute(
            """UPDATE meta.release SET status = 'published', sealed_at = now(),
                first_published_at = now(), dbt_manifest_uri = %s, dbt_run_results_uri = %s,
                quality_report_uri = %s WHERE release_id = %s""",
            (str(artifact_dir / "manifest.json"), str(artifact_dir / "run_results.json"),
             str(quality_path), release_id),
        )
        conn.execute(
            """INSERT INTO api.published_release (singleton, release_id) VALUES (true,%s)
                ON CONFLICT (singleton) DO UPDATE
                SET release_id = excluded.release_id, published_at = now()""", (release_id,),
        )
        conn.execute(
            """INSERT INTO meta.publication_event (release_id, previous_release_id, actor, reason)
                VALUES (%s,%s,'orchestration.pipeline','Full dbt build/test passed; frozen-input atomic publication')""",
            (release_id, previous_id),
        )
        conn.execute(
            """UPDATE meta.pipeline_run SET state='succeeded', finished_at=now(),
                details=details || %s::jsonb WHERE run_id = %s""",
            (json.dumps({"quality_report": report}), release["build_run_id"]),
        )
    return counts


def fail_release(release_id: UUID, message: str) -> None:
    with connection() as conn:
        release = conn.execute(
            "SELECT build_run_id, status FROM meta.release WHERE release_id=%s FOR UPDATE", (release_id,),
        ).fetchone()
        if release is not None and release["status"] == "candidate":
            conn.execute("UPDATE meta.release SET status='failed' WHERE release_id=%s", (release_id,))
            conn.execute(
                """UPDATE meta.pipeline_run SET state='failed', finished_at=now(),
                    details=details || %s::jsonb WHERE run_id=%s""",
                (json.dumps({"failure": message[:2000]}), release["build_run_id"]),
            )


def build(release_id: UUID, artifact_dir: Path, project_dir: Path, profiles_dir: Path) -> None:
    context = write_context(release_id, artifact_dir)
    environment = os.environ.copy()
    environment["DBT_SCHEMA"] = context["schema"]
    environment["DBT_ENGINE_TARGET_PATH"] = str(artifact_dir)
    environment["DBT_ENGINE_LOG_PATH"] = str(artifact_dir / "logs")
    process = subprocess.run(
        [sys.executable, "-m", "dbt.cli.main", "build", "--project-dir", str(project_dir), "--profiles-dir", str(profiles_dir),
         "--target-path", str(artifact_dir), "--log-path", str(artifact_dir / "logs"),
         "--vars", json.dumps({"release_id": str(release_id),
                                "input_manifest_sha256": context["input_manifest_sha256"]})],
        env=environment, stdout=sys.stderr, stderr=sys.stderr, check=False,
    )
    if process.returncode:
        raise PipelineError(f"dbt build failed with exit code {process.returncode}; serving release is unchanged.")
    seal_build(release_id, artifact_dir)


def run(source: str, artifact_root: Path, project_dir: Path, profiles_dir: Path,
        seasons: list[int] | None = None) -> UUID:
    release_id = prepare(source, seasons)
    artifact_dir = artifact_root / str(release_id)
    try:
        build(release_id, artifact_dir, project_dir, profiles_dir)
        counts = publish(release_id, artifact_dir)
        print(f"Published {release_id}: {json.dumps(counts, sort_keys=True)}", file=sys.stderr)
        return release_id
    except Exception as exc:
        try:
            fail_release(release_id, str(exc))
        except Exception:
            print("Unable to update failure metadata; serving pointer was not changed by this failed build.", file=sys.stderr)
        raise


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("migrate", help="Apply the additive serving metadata migration.")
    for name in ("prepare", "run"):
        command = commands.add_parser(name)
        command.add_argument("--source", choices=["demo_fixture", "basketball_reference"], required=True)
        command.add_argument("--season", action="append", type=int, help="Optional repeatable season filter.")
        if name == "run":
            command.add_argument("--artifacts-root", type=Path, default=Path("/artifacts"))
            command.add_argument("--project-dir", type=Path, default=ROOT / "dbt")
            command.add_argument("--profiles-dir", type=Path, default=Path("/opt/dbt"))
    command = commands.add_parser("build", help="Build/test a previously prepared candidate.")
    command.add_argument("--release", type=UUID, required=True)
    command.add_argument("--artifacts", type=Path, required=True)
    command.add_argument("--project-dir", type=Path, default=ROOT / "dbt")
    command.add_argument("--profiles-dir", type=Path, default=Path("/opt/dbt"))
    command = commands.add_parser("publish")
    command.add_argument("--release", type=UUID, required=True)
    command.add_argument("--artifacts", type=Path, required=True)
    args = parser.parse_args()
    try:
        if args.command == "migrate":
            migrate()
        elif args.command == "prepare":
            print(prepare(args.source, args.season))
        elif args.command == "run":
            print(run(args.source, args.artifacts_root, args.project_dir, args.profiles_dir, args.season))
        elif args.command == "build":
            build(args.release, args.artifacts, args.project_dir, args.profiles_dir)
        elif args.command == "publish":
            counts = publish(args.release, args.artifacts)
            print(json.dumps({"release_id": str(args.release), "row_counts": counts}))
    except (PipelineError, psycopg.Error, OSError, ValueError) as exc:
        if args.command in {"build", "publish"}:
            try:
                fail_release(args.release, str(exc))
            except Exception:
                pass
        # Keep credentials, DSNs, and PostgreSQL exception details out of output.
        message = str(exc) if isinstance(exc, PipelineError) else f"{type(exc).__name__}: see service logs for details."
        print(f"Pipeline failed: {message}", file=sys.stderr)
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
