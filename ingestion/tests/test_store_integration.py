"""Use a disposable initialized PostgreSQL database; never point at production."""

import os
from pathlib import Path
from uuid import uuid4

import psycopg
import pytest

from ingestion.parser import ParseError
from ingestion.store import accept_not_modified, load_html, open_ingest

DSN = os.getenv("NBA_TEST_DATABASE_DSN")
pytestmark = pytest.mark.skipif(not DSN, reason="NBA_TEST_DATABASE_DSN must name a disposable initialized database")
HTML = Path(__file__).parents[1].joinpath("fixtures", "players_2024.html").read_bytes()


@pytest.fixture
def database():
    # A disposable single-session PostgreSQL-compatible bridge can opt out of
    # statement caching. Native PostgreSQL uses psycopg's normal default.
    options = {"prepare_threshold": None} if os.getenv("NBA_TEST_DISABLE_PREPARED") == "1" else {}
    with psycopg.connect(DSN, autocommit=True, **options) as connection:
        yield connection


def import_fixture(database, source, html=HTML):
    return load_html(database, html, kind="players", season=2024, source_name=source, url=f"test://{source}/players")


def test_unchanged_html_keeps_observation_revisions_and_complete_batches(database):
    source = "test_" + uuid4().hex
    first = import_fixture(database, source)
    second = import_fixture(database, source)
    assert first.snapshot_id != second.snapshot_id
    assert first.parse_batch_id != second.parse_batch_id
    assert first.row_count == second.row_count == 4
    snapshots = database.execute("SELECT content_sha256 FROM raw.page_snapshot WHERE page_id=%s ORDER BY page_snapshot_id", (first.page_id,)).fetchall()
    assert len(snapshots) == 2 and snapshots[0] == snapshots[1]
    for result in (first, second):
        assert database.execute("SELECT count(*) FROM raw.player_season_row WHERE parse_batch_id=%s", (result.parse_batch_id,)).fetchone()[0] == 4
    correction = import_fixture(database, source, HTML.replace(b"retained", b"source-corrected"))
    reversion = import_fixture(database, source)
    assert len({first.snapshot_id, second.snapshot_id, correction.snapshot_id, reversion.snapshot_id}) == 4
    hashes = database.execute("SELECT content_sha256 FROM raw.page_snapshot WHERE page_id=%s ORDER BY page_snapshot_id", (first.page_id,)).fetchall()
    assert hashes[0] == hashes[1] == hashes[3] and hashes[2] != hashes[0]


def test_failed_parse_retains_body_and_failure_without_partial_rows(database):
    source = "test_" + uuid4().hex
    with pytest.raises(ParseError):
        import_fixture(database, source, HTML.replace(b'data-stat="fga"', b'data-stat="changed_fga"'))
    page_id = database.execute("SELECT page_id FROM raw.source_page WHERE source_name=%s", (source,)).fetchone()[0]
    assert database.execute("SELECT count(*) FROM raw.page_snapshot WHERE page_id=%s", (page_id,)).fetchone()[0] == 1
    assert database.execute("SELECT count(*) FROM raw.parse_batch b JOIN raw.page_snapshot s USING(page_snapshot_id) WHERE s.page_id=%s", (page_id,)).fetchone()[0] == 0
    outcomes = database.execute("SELECT outcome FROM meta.fetch_attempt WHERE page_id=%s ORDER BY fetch_attempt_id", (page_id,)).fetchall()
    assert outcomes == [("accepted",), ("parse_rejected",)]
    states = database.execute("SELECT r.state FROM meta.pipeline_run r JOIN raw.page_snapshot s ON s.ingest_run_id=r.run_id WHERE s.page_id=%s", (page_id,)).fetchall()
    assert states == [("failed",)]


def test_http_304_reuses_last_good_snapshot_without_new_raw_rows(database):
    source = "test_" + uuid4().hex
    first = import_fixture(database, source)
    context = open_ingest(database, kind="players", season=2024, phase="regular", source_name=source, url=f"test://{source}/players")
    reused = accept_not_modified(database, context)
    assert reused.reused and reused.snapshot_id == first.snapshot_id
    assert reused.parse_batch_id == first.parse_batch_id
    assert database.execute("SELECT count(*) FROM raw.page_snapshot WHERE page_id=%s", (first.page_id,)).fetchone()[0] == 1
