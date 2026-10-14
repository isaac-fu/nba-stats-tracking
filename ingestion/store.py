"""Append-only observations and complete raw parse batches in PostgreSQL."""

from __future__ import annotations

from dataclasses import dataclass
from collections.abc import Mapping
from datetime import datetime, timezone
import gzip
import hashlib
import json
import os
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

from psycopg import Connection
from psycopg.types.json import Jsonb

from ingestion.parser import PARSER_VERSION, RAW_CONTRACT_VERSION, parse_rows
from ingestion.provenance import validate_html_canonical, validate_source_url


@dataclass(frozen=True)
class IngestContext:
    run_id: UUID
    page_id: int
    kind: str
    season: int
    phase: str
    source_name: str = ""
    url: str = ""


@dataclass(frozen=True)
class ImportResult:
    run_id: UUID
    page_id: int
    snapshot_id: int
    parse_batch_id: UUID | None
    row_count: int
    reused: bool = False


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _sha(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def _code_sha() -> str:
    return os.getenv("NBA_CODE_SHA", "local-development")


def _values(row: Any) -> tuple[Any, ...] | None:
    if row is None:
        return None
    return tuple(row.values()) if isinstance(row, Mapping) else tuple(row)


def _start_run(connection: Connection, job_kind: str, config: dict[str, Any]) -> UUID:
    run_id = uuid4()
    digest = _sha(json.dumps(config, sort_keys=True, separators=(",", ":")).encode())
    connection.execute(
        "INSERT INTO meta.pipeline_run (run_id,job_kind,state,code_sha,config_sha256,details) VALUES (%s,%s,'running',%s,%s,%s)",
        (run_id, job_kind, _code_sha(), digest, Jsonb(config)),
    )
    return run_id


def _finish_run(connection: Connection, run_id: UUID, state: str, details: dict[str, Any]) -> None:
    connection.execute(
        "UPDATE meta.pipeline_run SET state=%s,finished_at=clock_timestamp(),details=details || %s WHERE run_id=%s AND state='running'",
        (state, Jsonb(details), run_id),
    )


def open_ingest(connection: Connection, *, kind: str, season: int, phase: str, source_name: str, url: str) -> IngestContext:
    if not connection.autocommit:
        raise ValueError("Use psycopg.connect(..., autocommit=True); ingestion manages its own transactions.")
    if kind not in {"players", "teams"} or phase not in {"regular", "playoffs"}:
        raise ValueError("Invalid source kind or phase")
    if not source_name.strip() or not url.strip():
        raise ValueError("source_name and url must not be empty")
    source_name = source_name.strip()
    url = validate_source_url(source_name, url, kind, season, phase)
    config = {"kind": kind, "season": season, "phase": phase, "source_name": source_name, "url": url}
    page_kind = "player_season_totals" if kind == "players" else "team_season_totals"
    logical_key = f"{kind}:{season}:{phase}"
    with connection.transaction():
        run_id = _start_run(connection, "ingest", config)
        connection.execute(
            "INSERT INTO raw.source_page (source_name,page_kind,canonical_url,logical_key,season_end_year,source_phase) VALUES (%s,%s,%s,%s,%s,%s) ON CONFLICT (canonical_url) DO NOTHING",
            (source_name, page_kind, url, logical_key, season, phase),
        )
        row = _values(connection.execute(
            "SELECT page_id,source_name,page_kind,logical_key FROM raw.source_page WHERE canonical_url=%s", (url,)
        ).fetchone())
        if row is None or tuple(row[1:]) != (source_name, page_kind, logical_key):
            raise ValueError("The canonical URL already belongs to a different source contract.")
    return IngestContext(run_id, row[0], kind, season, phase, source_name, url)


def record_attempt(
    connection: Connection, context: IngestContext, *, outcome: str, number: int = 1,
    status: int | None = None, snapshot_id: int | None = None, error_code: str | None = None,
    elapsed_ms: int | None = None, finish: bool = False,
) -> None:
    with connection.transaction():
        connection.execute(
            "INSERT INTO meta.fetch_attempt (ingest_run_id,page_id,attempted_at,attempt_number,http_status,outcome,page_snapshot_id,error_code,elapsed_ms) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s)",
            (context.run_id, context.page_id, _now(), number, status, outcome, snapshot_id, error_code, elapsed_ms),
        )
        if finish:
            _finish_run(connection, context.run_id, "failed", {"error_code": error_code, "outcome": outcome})


def latest_usable_snapshot(connection: Connection, page_id: int) -> dict[str, Any] | None:
    row = _values(connection.execute(
        """SELECT s.page_snapshot_id,s.response_headers,b.parse_batch_id,b.table_row_counts
           FROM raw.page_snapshot s JOIN raw.parse_batch b ON b.page_snapshot_id=s.page_snapshot_id
           WHERE s.page_id=%s
           ORDER BY s.fetched_at DESC,s.page_snapshot_id DESC,b.completed_at DESC LIMIT 1""", (page_id,)
    ).fetchone())
    if row is None:
        return None
    return {"snapshot_id": row[0], "headers": row[1], "parse_batch_id": row[2], "row_count": sum(row[3].values())}


def accept_not_modified(connection: Connection, context: IngestContext, *, number: int = 1, elapsed_ms: int | None = None) -> ImportResult:
    snapshot = latest_usable_snapshot(connection, context.page_id)
    if snapshot is None:
        record_attempt(connection, context, outcome="permanent_error", number=number, status=304, error_code="304_WITHOUT_USABLE_SNAPSHOT", finish=True)
        raise ValueError("HTTP 304 arrived without a previously parsed snapshot.")
    with connection.transaction():
        record_attempt(connection, context, outcome="not_modified", number=number, status=304, snapshot_id=snapshot["snapshot_id"], elapsed_ms=elapsed_ms)
        _finish_run(connection, context.run_id, "succeeded", {"reused_snapshot_id": snapshot["snapshot_id"]})
    return ImportResult(context.run_id, context.page_id, snapshot["snapshot_id"], snapshot["parse_batch_id"], snapshot["row_count"], True)


def accept_html(
    connection: Connection, context: IngestContext, html: bytes | str, *,
    response_headers: dict[str, str] | None = None, status: int | None = None,
    attempt_number: int = 1, elapsed_ms: int | None = None,
) -> ImportResult:
    body = html.encode("utf-8") if isinstance(html, str) else html
    if not body:
        record_attempt(connection, context, outcome="parse_rejected", number=attempt_number, status=status, error_code="EMPTY_BODY", finish=True)
        raise ValueError("Empty source body; no snapshot created")
    # Commit the immutable body BEFORE parsing: rejected source changes remain auditable.
    with connection.transaction():
        snapshot_id = _values(connection.execute(
            "INSERT INTO raw.page_snapshot (page_id,ingest_run_id,fetched_at,content_sha256,content_bytes,body_gzip,response_headers) VALUES (%s,%s,%s,%s,%s,%s,%s) RETURNING page_snapshot_id",
            (context.page_id, context.run_id, _now(), _sha(body), len(body), gzip.compress(body, mtime=0), Jsonb(response_headers or {})),
        ).fetchone())[0]
        record_attempt(connection, context, outcome="accepted", number=attempt_number, status=status, snapshot_id=snapshot_id, elapsed_ms=elapsed_ms)
        parse_run = _start_run(connection, "parse", {"snapshot_id": snapshot_id, "parser_version": PARSER_VERSION})
    try:
        validate_html_canonical(body, source_name=context.source_name, kind=context.kind, season=context.season, phase=context.phase)
        rows = parse_rows(body, context.kind, context.season, context.phase)
        batch_id = uuid4()
        relation = "raw.player_season_row" if context.kind == "players" else "raw.team_season_totals_row"
        counts = {relation: len(rows)}
        parser_digest = _sha(Path(__file__).with_name("parser.py").read_bytes())
        with connection.transaction():
            connection.execute(
                "INSERT INTO raw.parse_batch (parse_batch_id,page_snapshot_id,parse_run_id,parser_version,parser_code_sha,raw_contract_version,completed_at,table_row_counts) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)",
                (batch_id, snapshot_id, parse_run, PARSER_VERSION, parser_digest, RAW_CONTRACT_VERSION, _now(), Jsonb(counts)),
            )
            if context.kind == "players":
                values = [(batch_id, row.source_table_id, row.row_ordinal, context.season, context.phase, row.source_player_id, row.source_team_token, Jsonb(row.source_payload)) for row in rows]
                with connection.cursor() as cursor:
                    cursor.executemany("INSERT INTO raw.player_season_row (parse_batch_id,source_table_id,row_ordinal,season_end_year,source_phase,source_player_id,source_team_token,source_payload) VALUES (%s,%s,%s,%s,%s,%s,%s,%s)", values)
            else:
                values = [(batch_id, row.source_table_id, row.row_ordinal, context.season, context.phase, row.source_team_token, Jsonb(row.source_payload)) for row in rows]
                with connection.cursor() as cursor:
                    cursor.executemany("INSERT INTO raw.team_season_totals_row (parse_batch_id,source_table_id,row_ordinal,season_end_year,source_phase,source_team_code,source_payload) VALUES (%s,%s,%s,%s,%s,%s,%s)", values)
            _finish_run(connection, parse_run, "succeeded", {"parse_batch_id": str(batch_id), "row_counts": counts})
            _finish_run(connection, context.run_id, "succeeded", {"snapshot_id": snapshot_id, "parse_batch_id": str(batch_id), "row_counts": counts})
    except Exception as error:
        # The batch transaction rolls back; only its snapshot and failure metadata survive.
        with connection.transaction():
            _finish_run(connection, parse_run, "failed", {"error_type": type(error).__name__, "message": str(error)[:500]})
            _finish_run(connection, context.run_id, "failed", {"error_type": type(error).__name__, "snapshot_id": snapshot_id})
            record_attempt(connection, context, outcome="parse_rejected", number=attempt_number, status=status, snapshot_id=snapshot_id, error_code=type(error).__name__)
        raise
    return ImportResult(context.run_id, context.page_id, snapshot_id, batch_id, len(rows))


def load_html(
    connection: Connection, html: bytes | str, *, kind: str, season: int,
    phase: str = "regular", source_name: str = "basketball_reference", url: str,
) -> ImportResult:
    """Import saved HTML. Caller owns an autocommit psycopg connection; no HTTP occurs."""
    url = validate_source_url(source_name, url, kind, season, phase)
    validate_html_canonical(html, source_name=source_name, kind=kind, season=season, phase=phase)
    context = open_ingest(connection, kind=kind, season=season, phase=phase, source_name=source_name, url=url)
    return accept_html(connection, context, html)
