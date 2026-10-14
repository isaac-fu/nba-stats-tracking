from pathlib import Path

import pytest

from ingestion.parser import ParseError, parse_rows

FIXTURES = Path(__file__).parents[1] / "fixtures"


def fixture(kind="players"):
    return (FIXTURES / f"{kind}_2024.html").read_text(encoding="utf-8")


def test_commented_table_rank_and_repeated_headers_do_not_change_identity():
    rows = parse_rows(fixture(), "players", 2024)
    assert len(rows) == 4
    assert [(row.source_player_id, row.source_team_token) for row in rows] == [
        ("example01", "2TM"), ("example01", "BOS"), ("example01", "LAL"), ("sample01", "BOS")
    ]
    assert rows[0].source_payload["pts"] == "50"
    assert rows[0].source_payload["future_source_field"] == "retained"


def test_missing_statistics_are_blank_not_zero_and_original_keys_survive():
    payload = parse_rows(fixture(), "players", 2024)[3].source_payload
    assert payload["stl"] == payload["blk"] == ""
    assert payload["reb"] == payload["trb"] == "4"


@pytest.mark.parametrize("token", ["TOT", "2TM", "3TM"])
def test_all_team_tokens_are_not_real_team_ids(token):
    rows = parse_rows(fixture().replace(">2TM<", f">{token}<"), "players", 2024)
    assert rows[0].source_team_token == token


def test_current_column_and_table_aliases():
    html = fixture().replace('id="totals_stats"', 'id="player-stats-base"')
    for old, new in (("player", "name_display"), ("g", "games"), ("gs", "games_started"), ("fg", "fgm"), ("fg3", "fg3m"), ("ft", "ftm"), ("trb", "total_rebounds"), ("pos", "position")):
        html = html.replace(f'data-stat="{old}"', f'data-stat="{new}"')
    payload = parse_rows(html, "players", 2024)[0].source_payload
    assert payload["player_name"] == "Alex Example"
    assert payload["fg"] == payload["fgm"] == "20"
    assert payload["g"] == "4"
    assert payload["reb"] == "20"


def test_teams_merge_commented_records_and_opponent_totals_by_link():
    rows = parse_rows(fixture("teams"), "teams", 2024)
    assert len(rows) == 2
    assert rows[0].source_team_token == "BOS"
    assert rows[0].source_payload["wins"] == "3"
    assert rows[0].source_payload["losses"] == "1"
    assert rows[0].source_payload["opponent_pts"] == "400"


def test_missing_team_record_cannot_be_fabricated():
    html = fixture("teams").replace('id="misc_stats"', 'id="unsupported_misc"')
    with pytest.raises(ParseError, match="wins, and losses"):
        parse_rows(html, "teams", 2024)


@pytest.mark.parametrize("html", ["", "<html>Access denied</html>", "<html><title>Just a moment...</title></html>", "<table id='totals_stats'><tbody></tbody></table>"])
def test_empty_blocked_and_empty_tables_fail_closed(html):
    with pytest.raises(ParseError):
        parse_rows(html, "players", 2024)


def test_changed_contract_is_rejected_instead_of_partially_loaded():
    with pytest.raises(ParseError, match="missing columns"):
        parse_rows(fixture().replace('data-stat="fga"', 'data-stat="unknown_attempts"'), "players", 2024)


def test_rate_table_caption_cannot_be_mistaken_for_totals():
    html = fixture().replace('<table id="totals_stats">', '<table id="totals_stats"><caption>Per Game Stats</caption>')
    with pytest.raises(ParseError, match="rate table"):
        parse_rows(html, "players", 2024)


def test_duplicate_entity_row_is_rejected():
    html = fixture()
    first = html[html.index('<tr><td data-stat="ranker">1'):html.index('<tr><td data-stat="ranker">2')]
    with pytest.raises(ParseError, match="Duplicate row identity"):
        parse_rows(html.replace("</tbody>", first + "</tbody>", 1), "players", 2024)


def test_linked_wrong_season_is_rejected():
    with pytest.raises(ParseError, match="expected 2024"):
        parse_rows(fixture().replace("/teams/BOS/2024.html", "/teams/BOS/2023.html"), "players", 2024)
