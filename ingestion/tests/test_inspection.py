import json
from pathlib import Path
import subprocess
import sys

import httpx
import psycopg
import pytest

from ingestion.cli import main
from ingestion.inspection import inspect_html
from ingestion.parser import ParseError, parse_rows
from ingestion.provenance import ProvenanceError, canonical_source_url, validate_html_canonical, validate_source_url
from ingestion.store import load_html, open_ingest

FIXTURES = Path(__file__).parents[1] / "fixtures"


def fixture(kind="players"):
    return (FIXTURES / f"{kind}_2024.html").read_text(encoding="utf-8")


def with_canonical(html, url):
    return html.replace("</head>", f'<link rel="canonical" href="{url}"></head>')


def test_inspect_cli_never_connects_to_database_or_http(monkeypatch, capsys):
    def unexpected(*args, **kwargs):
        pytest.fail("Offline inspection attempted database or HTTP access")
    monkeypatch.setattr(psycopg, "connect", unexpected)
    monkeypatch.setattr(httpx, "Client", unexpected)
    assert main(["inspect", "--kind", "players", "--season", "2024", "--file", str(FIXTURES / "players_2024.html"), "--source-name", "demo_fixture", "--url", "demo://season/2024/regular/players"]) == 0
    report = json.loads(capsys.readouterr().out)
    assert report["source_name"] == "demo_fixture"
    assert report["canonical_source_url"] == "demo://season/2024/regular/players"
    assert report["row_count"] == 4 and report["distinct_entities"] == 2
    assert report["distinct_players"] == report["distinct_teams"] == 2
    assert report["all_team_rows"] == 1
    assert report["detected_tables"] == ["totals_stats"]
    assert report["missing_metric_fields"] == {"stl": 1, "blk": 1}
    assert report["html_canonical_verified"] is False


def test_team_inspection_reports_commented_tables_and_complete_metrics():
    report = inspect_html(fixture("teams"), kind="teams", season=2024, source_name="demo_fixture")
    assert report["detected_tables"] == ["totals-team", "misc_stats", "totals-opponent"]
    assert report["row_count"] == report["distinct_entities"] == 2
    assert report["missing_metric_fields"] == {}


def test_inspection_runs_with_site_packages_disabled():
    # -S disables all third-party site packages, including psycopg and httpx.
    process = subprocess.run([sys.executable, "-S", "-m", "ingestion.cli", "inspect", "--kind", "teams", "--season", "2024", "--file", str(FIXTURES / "teams_2024.html"), "--source-name", "demo_fixture"], cwd=FIXTURES.parents[1], capture_output=True, text=True, check=False)
    assert process.returncode == 0, process.stderr
    assert json.loads(process.stdout)["row_count"] == 2


@pytest.mark.parametrize("table", ["misc_stats", "totals-opponent"])
def test_supplemental_team_rows_cannot_mix_years(table):
    html = fixture("teams")
    start = html.index(f'<table id="{table}">')
    end = html.index("</table>", start)
    altered = html[:start] + html[start:end].replace("/2024.html", "/2023.html") + html[end:]
    with pytest.raises(ParseError, match="Supplemental team link belongs to season 2023"):
        parse_rows(altered, "teams", 2024)


@pytest.mark.parametrize("url", [
    "https://www.basketball-reference.com/leagues/NBA_2023_totals.html",
    "https://www.basketball-reference.com/playoffs/NBA_2024_totals.html",
    "https://www.basketball-reference.com/leagues/NBA_2024.html",
    "https://example.com/leagues/NBA_2024_totals.html",
    "https://www.basketball-reference.com/leagues/NBA_2024_totals.html?season=2023",
])
def test_wrong_provider_scope_url_is_rejected_before_any_database_write(url):
    class NoDatabaseWrites:
        autocommit = True
        def transaction(self):
            pytest.fail("Invalid source metadata reached a database transaction")
    with pytest.raises(ProvenanceError, match="Source URL does not match"):
        open_ingest(NoDatabaseWrites(), kind="players", season=2024, phase="regular", source_name="basketball_reference", url=url)


def test_url_normalization_keeps_one_canonical_source_page():
    url = "https://basketball-reference.com/leagues/NBA_2024_totals.html#totals_stats"
    assert validate_source_url("basketball_reference", url, "players", 2024, "regular") == canonical_source_url("players", 2024, "regular")


@pytest.mark.parametrize("url", [
    "https://www.basketball-reference.com/playoffs/NBA_2024_totals.html",
    "https://www.basketball-reference.com/leagues/NBA_2023_totals.html",
    "https://www.basketball-reference.com/leagues/NBA_2024.html",
    "",
])
def test_saved_html_canonical_contradiction_is_rejected_before_database_access(url):
    html = with_canonical(fixture(), url)
    with pytest.raises(ProvenanceError, match="HTML canonical URL contradicts"):
        load_html(None, html, kind="players", season=2024, source_name="basketball_reference", url=canonical_source_url("players", 2024, "regular"))


@pytest.mark.parametrize("url", [
    "https://www.basketball-reference.com/leagues/NBA_2024_totals.html",
    "/leagues/NBA_2024_totals.html",
    "http://www.basketball-reference.com/leagues/NBA_2024_totals.html",
])
def test_matching_canonical_metadata_provides_explicit_scope_evidence(url):
    report = inspect_html(with_canonical(fixture(), url), kind="players", season=2024, source_name="basketball_reference")
    assert report["html_canonical_verified"] is True
    assert report["phase_evidence"] == "matching HTML canonical URL"


def test_conflicting_canonical_tags_are_rejected():
    html = with_canonical(fixture(), canonical_source_url("players", 2024, "regular"))
    html = with_canonical(html, canonical_source_url("players", 2024, "playoffs"))
    with pytest.raises(ProvenanceError):
        validate_html_canonical(html, source_name="basketball_reference", kind="players", season=2024, phase="regular")


@pytest.mark.parametrize("phase", ["regular", "playoffs"])
@pytest.mark.parametrize("url", ["#totals_stats", "NBA_2024_totals.html"])
def test_relative_or_fragment_canonical_does_not_invent_phase_evidence(phase, url):
    report = inspect_html(with_canonical(fixture(), url), kind="players", season=2024, phase=phase, source_name="basketball_reference")
    assert report["row_count"] == 4
    assert report["html_canonical_verified"] is False
    assert "not verified" in report["phase_evidence"]


@pytest.mark.parametrize("phase", ["regular", "playoffs"])
@pytest.mark.parametrize("url", ["NBA_2023_totals.html", "NBA_2024.html"])
def test_relative_canonical_filename_still_rejects_wrong_year_or_kind(phase, url):
    with pytest.raises(ProvenanceError, match="Relative HTML canonical URL"):
        inspect_html(with_canonical(fixture(), url), kind="players", season=2024, phase=phase, source_name="basketball_reference")


@pytest.mark.parametrize("phase,matching,contradictory", [
    ("regular", "/leagues/NBA_2024_totals.html", "/playoffs/NBA_2024_totals.html"),
    ("playoffs", "/playoffs/NBA_2024_totals.html", "/leagues/NBA_2024_totals.html"),
])
def test_root_relative_canonical_has_independent_phase_evidence(phase, matching, contradictory):
    assert inspect_html(with_canonical(fixture(), matching), kind="players", season=2024, phase=phase, source_name="basketball_reference")["html_canonical_verified"] is True
    with pytest.raises(ProvenanceError, match="HTML canonical URL contradicts"):
        inspect_html(with_canonical(fixture(), contradictory), kind="players", season=2024, phase=phase, source_name="basketball_reference")


def test_generic_html_and_missing_canonical_do_not_claim_phase_detection():
    html = with_canonical(fixture(), "https://another-provider.example/totals")
    report = inspect_html(html, kind="players", season=2024, phase="playoffs", source_name="demo_fixture", url="demo://season/2024/playoffs/players")
    assert report["row_count"] == 4 and report["phase"] == "playoffs"
    assert report["html_canonical_verified"] is False
    assert "not verified" in report["phase_evidence"]
    assert len(parse_rows(html, "players", 2024, "playoffs")) == 4


def test_cli_import_rejects_wrong_phase_url_before_connecting(monkeypatch, capsys):
    monkeypatch.setattr(psycopg, "connect", lambda *args, **kwargs: pytest.fail("Invalid provenance opened a database connection"))
    assert main(["import", "--kind", "players", "--season", "2024", "--phase", "playoffs", "--file", str(FIXTURES / "players_2024.html"), "--url", canonical_source_url("players", 2024, "regular")]) == 1
    assert "Source URL does not match" in capsys.readouterr().err
