"""Read-only dependency, connection, and artifact checks inside the tool image."""

from __future__ import annotations

import argparse
import importlib
from importlib.metadata import version
from pathlib import Path


DEPENDENCIES = {
    "httpx": "httpx",
    "beautifulsoup4": "bs4",
    "lxml": "lxml.etree",
    "psycopg": "psycopg",
    "pydantic": "pydantic",
    "pytest": "pytest",
    "ruff": "ruff",
    "dagster": "dagster",
    "dagster-webserver": "dagster_webserver",
    "dagster-postgres": "dagster_postgres",
    "PyYAML": "yaml",
    "openapi-spec-validator": "openapi_spec_validator",
    "pglast": "pglast",
}


def check_dependencies() -> None:
    for distribution, module in DEPENDENCIES.items():
        importlib.import_module(module)
        print(f"{distribution}: {version(distribution)}")


def check_database() -> None:
    import psycopg

    # libpq reads explicit PG* environment variables; no password is logged.
    with psycopg.connect(connect_timeout=10) as connection:
        with connection.cursor() as cursor:
            cursor.execute("SELECT current_database(), current_user, version()")
            database, user, server_version = cursor.fetchone()
            print(f"Database connection: {database} as {user}")
            print(server_version)
            expected_relations = [
                "meta.pipeline_run",
                "meta.fetch_attempt",
                "meta.release",
                "meta.release_input",
                "raw.source_page",
                "raw.page_snapshot",
                "raw.parse_batch",
                "analytics.dim_player",
                "analytics.fact_player_game",
                "analytics.mart_player_season",
                "analytics.mart_team_season",
                "api.published_release",
                "api.current_player_season",
                "api.current_team_season",
            ]
            cursor.execute(
                "SELECT name, to_regclass(name)::text "
                "FROM unnest(%s::text[]) AS expected(name)",
                (expected_relations,),
            )
            missing = []
            for name, relation in cursor.fetchall():
                if relation is None:
                    missing.append(name)
                else:
                    print(f"Database relation: {name}")
            if missing:
                raise ValueError("Missing expected relations: " + ", ".join(missing))


def check_artifacts(root: Path) -> None:
    import yaml
    from openapi_spec_validator import validate
    from pglast import parse_sql

    for filename in ("compose.yaml", "profiles.yml", "dbt_project.yml"):
        path = root / "docker" / filename
        with path.open(encoding="utf-8") as file:
            document = yaml.safe_load(file)
        if not isinstance(document, dict):
            raise ValueError(f"Expected a YAML mapping in {path}")
        print(f"YAML syntax: {path.name}")

    with (root / "contracts" / "openapi.yaml").open(encoding="utf-8") as file:
        spec = yaml.safe_load(file)
    validate(spec)
    print("OpenAPI schema: valid")

    statements = parse_sql((root / "database.sql").read_text(encoding="utf-8"))
    print(f"PostgreSQL SQL syntax: {len(statements)} statements parsed")
    print("SQL parsing does not verify table references, privileges, or execution.")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--database", action="store_true")
    parser.add_argument("--artifacts", action="store_true")
    parser.add_argument("--root", type=Path, default=Path("/workspace"))
    args = parser.parse_args()
    check_dependencies()
    if args.database:
        check_database()
    if args.artifacts:
        check_artifacts(args.root)


if __name__ == "__main__":
    main()
