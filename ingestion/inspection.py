"""Offline previews: no database connections, imports, or HTTP requests."""

from __future__ import annotations

from ingestion.parser import ALL_TEAMS, TOTAL_KEYS, detected_tables, parse_rows
from ingestion.provenance import validate_html_canonical, validate_source_url


def inspect_html(html: bytes | str, *, kind: str, season: int, phase: str = "regular", source_name: str | None = None, url: str | None = None) -> dict:
    canonical_url = validate_source_url(source_name or "", url, kind, season, phase) if url else None
    evidence = validate_html_canonical(html, source_name=source_name, kind=kind, season=season, phase=phase)
    rows = parse_rows(html, kind, season, phase)
    fields = ("gs", *TOTAL_KEYS) if kind == "players" else ("wins", "losses", "opponent_pts", *TOTAL_KEYS)
    missing = {key: count for key in fields if (count := sum(not row.source_payload.get(key, "").strip() for row in rows))}
    players = {row.source_player_id for row in rows if row.source_player_id}
    teams = {row.source_team_token for row in rows if row.source_team_token not in ALL_TEAMS}
    return {
        "kind": kind,
        "season": season,
        "phase": phase,
        "source_name": source_name,
        "canonical_source_url": canonical_url,
        "html_canonical_urls": list(evidence.urls),
        "html_canonical_verified": evidence.verified,
        "phase_evidence": "matching HTML canonical URL" if evidence.verified else "caller argument; HTML phase is not verified",
        "detected_tables": detected_tables(html),
        "selected_table": rows[0].source_table_id,
        "row_count": len(rows),
        "distinct_entities": len(players) if kind == "players" else len(teams),
        "distinct_players": len(players),
        "distinct_teams": len(teams),
        "all_team_rows": sum(row.source_team_token in ALL_TEAMS for row in rows),
        "missing_metric_fields": missing,
    }
