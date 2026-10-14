"""Run from the repository root: python -m ingestion.cli --help."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import sys
from typing import TYPE_CHECKING

from ingestion.inspection import inspect_html
from ingestion.provenance import validate_html_canonical, validate_source_url

if TYPE_CHECKING:
    import psycopg
    from ingestion.store import ImportResult

FIXTURES = Path(__file__).with_name("fixtures")


def result_json(result: ImportResult) -> str:
    return json.dumps({"run_id": str(result.run_id), "page_id": result.page_id, "snapshot_id": result.snapshot_id, "parse_batch_id": str(result.parse_batch_id) if result.parse_batch_id is not None else None, "row_count": result.row_count, "reused_snapshot": result.reused})


def load_sample(connection: psycopg.Connection) -> list[ImportResult]:
    """Import synthetic 2024 fixtures without source fetching or invented real statistics."""
    from ingestion.store import load_html
    return [load_html(connection, (FIXTURES / f"{kind}_2024.html").read_bytes(), kind=kind, season=2024, source_name="demo_fixture", url=f"demo://season/2024/regular/{kind}") for kind in ("players", "teams")]


def make_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Append source season totals to PostgreSQL raw tables.", allow_abbrev=False)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("sample", help="Import the bundled synthetic 2024 player/team HTML fixtures.")
    preview = sub.add_parser("inspect", help="Preview saved HTML without any database or HTTP calls.")
    preview.add_argument("--kind", choices=("players", "teams"), required=True)
    preview.add_argument("--season", type=int, required=True)
    preview.add_argument("--phase", choices=("regular", "playoffs"), default="regular")
    preview.add_argument("--file", type=Path, required=True)
    preview.add_argument("--source-name", help="Optional declared provider, e.g. demo_fixture or basketball_reference.")
    preview.add_argument("--url", help="Optional canonical source identity; BRef URLs are checked against kind/year/phase.")
    saved = sub.add_parser("import", help="Import an authorized saved HTML totals page without HTTP.")
    saved.add_argument("--kind", choices=("players", "teams"), required=True)
    saved.add_argument("--season", type=int, required=True)
    saved.add_argument("--phase", choices=("regular", "playoffs"), default="regular")
    saved.add_argument("--file", type=Path, required=True)
    saved.add_argument("--source-name", default="basketball_reference")
    saved.add_argument("--url", required=True, help="Canonical source URL; do not use a local filepath as source identity.")
    fetch = sub.add_parser("fetch", help="Refresh a historical season range using explicitly authorized source access.")
    fetch.add_argument("--start-season", type=int, required=True)
    fetch.add_argument("--end-season", type=int, required=True)
    daily = sub.add_parser("daily", help="Refresh one season's totals. Scheduling is handled outside this command.")
    daily.add_argument("--season", type=int, required=True)
    for command in (fetch, daily):
        command.add_argument("--phase", choices=("regular", "playoffs"), default="regular")
        command.add_argument("--allow-live", action="store_true", help="Acknowledge you have authorized access; this flag does not grant source permission.")
        command.add_argument("--user-agent", default="NBAStatsLearning/0.1", help="Descriptive source User-Agent; optionally include your contact URL.")
    return parser


def main(argv: list[str] | None = None) -> int:
    parser = make_parser()
    args = parser.parse_args(argv)
    if args.command in {"fetch", "daily"} and not args.allow_live:
        parser.error("Live fetching requires --allow-live after obtaining authorized source access. Use sample/import for offline learning.")
    if args.command == "fetch" and not 1947 <= args.start_season <= args.end_season <= 2100:
        parser.error("Use an ascending season-ending-year range between 1947 and 2100.")
    database_errors: tuple[type[BaseException], ...] = ()
    try:
        if args.command == "inspect":
            report = inspect_html(args.file.read_bytes(), kind=args.kind, season=args.season, phase=args.phase, source_name=args.source_name, url=args.url)
            print(json.dumps(report, indent=2))
            return 0
        if args.command == "import":
            # Reject contradictory saved-page identity before opening a database connection.
            args.url = validate_source_url(args.source_name, args.url, args.kind, args.season, args.phase)
            body = args.file.read_bytes()
            validate_html_canonical(body, source_name=args.source_name, kind=args.kind, season=args.season, phase=args.phase)
        # Offline inspection needs only the standard library. Other commands load
        # their runtime dependencies only after source metadata is checked.
        import psycopg
        from ingestion.client import SourceClient
        from ingestion.store import load_html
        database_errors = (psycopg.Error,)
        # No password is printed. Container PG* environment variables resolve the database.
        with psycopg.connect(os.getenv("DATABASE_URL", ""), autocommit=True, connect_timeout=10) as connection:
            if args.command == "sample":
                for result in load_sample(connection):
                    print(result_json(result))
            elif args.command == "import":
                result = load_html(connection, body, kind=args.kind, season=args.season, phase=args.phase, source_name=args.source_name, url=args.url)
                print(result_json(result))
            else:
                seasons = range(args.start_season, args.end_season + 1) if args.command == "fetch" else [args.season]
                with SourceClient(allow_live=args.allow_live, user_agent=args.user_agent) as client:
                    for season in seasons:
                        for kind in ("players", "teams"):
                            print(result_json(client.fetch(connection, kind=kind, season=season, phase=args.phase)))
    except Exception as error:
        # Driver diagnostics may quote malformed connection strings. Never print them.
        message = "Database operation failed; check PostgreSQL health, PG* configuration, and initialized schema." if isinstance(error, database_errors) else str(error)
        for name in ("PGPASSWORD", "DATABASE_PASS", "DATABASE_URL"):
            secret = os.getenv(name)
            if secret:
                message = message.replace(secret, "<redacted>")
        print(f"Ingestion failed ({type(error).__name__}): {message}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
