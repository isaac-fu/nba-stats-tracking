from types import SimpleNamespace

import httpx
import pytest

from ingestion import client as module
from ingestion.client import SourceAccessError, SourceClient, retry_delay, source_url


def test_live_access_is_explicit_and_minimum_pacing_is_enforced():
    with pytest.raises(SourceAccessError, match="authorized"):
        SourceClient(allow_live=False)
    with pytest.raises(ValueError, match="3.1"):
        SourceClient(allow_live=True, interval=0)


def test_source_urls_keep_season_and_phase_explicit():
    assert source_url("players", 2024, "regular").endswith("/leagues/NBA_2024_totals.html")
    assert source_url("teams", 2024, "playoffs").endswith("/playoffs/NBA_2024.html")


def test_retry_after_date_and_seconds_are_respected():
    from datetime import datetime, timezone
    now = datetime(2026, 10, 13, 12, 0, tzinfo=timezone.utc)
    assert retry_delay("15", 1, now) == 15
    assert retry_delay("Tue, 13 Oct 2026 12:01:00 GMT", 1, now) == 60


@pytest.mark.parametrize("status,body", [(403, "Forbidden"), (200, "User-agent: *\nDisallow: /\n"), (200, "<html>challenge</html>"), (200, "User-agent: *\nDisallow: /leagues/*\n")])
def test_robots_errors_disallow_and_invalid_content_stop_before_page_request(status, body):
    requested = []
    def handler(request):
        requested.append(str(request.url))
        return httpx.Response(status, text=body)
    with SourceClient(allow_live=True, transport=httpx.MockTransport(handler)) as client:
        with pytest.raises(SourceAccessError):
            client._check_robots(source_url("players", 2024, "regular"))
    assert requested == ["https://www.basketball-reference.com/robots.txt"]


@pytest.mark.parametrize("status,body,expected", [(403, b"Forbidden", "blocked"), (200, b"<html>Verify you are human</html>", "blocked"), (302, b"", "permanent_error")])
def test_source_blocks_challenges_and_redirects_are_not_retried(monkeypatch, status, body, expected):
    attempts = []
    requests = []
    monkeypatch.setattr(module, "open_ingest", lambda *args, **kwargs: SimpleNamespace(page_id=1))
    monkeypatch.setattr(module, "latest_usable_snapshot", lambda *args: None)
    monkeypatch.setattr(module, "record_attempt", lambda *args, **kwargs: attempts.append(kwargs))
    def handler(request):
        requests.append(str(request.url))
        return httpx.Response(status, content=body, headers={"content-type": "text/html"})
    with SourceClient(allow_live=True, transport=httpx.MockTransport(handler)) as client:
        monkeypatch.setattr(client, "_check_robots", lambda url: None)
        with pytest.raises(SourceAccessError):
            client.fetch(None, kind="players", season=2024)
    assert len(requests) == 1
    assert attempts[0]["outcome"] == expected and attempts[0]["finish"]


def test_long_retry_after_halts_without_retrying_early(monkeypatch):
    attempts = []
    requests = []
    monkeypatch.setattr(module, "open_ingest", lambda *args, **kwargs: SimpleNamespace(page_id=1))
    monkeypatch.setattr(module, "latest_usable_snapshot", lambda *args: None)
    monkeypatch.setattr(module, "record_attempt", lambda *args, **kwargs: attempts.append(kwargs))
    def handler(request):
        requests.append(str(request.url))
        return httpx.Response(429, headers={"retry-after": "3600"})
    with SourceClient(allow_live=True, transport=httpx.MockTransport(handler)) as client:
        monkeypatch.setattr(client, "_check_robots", lambda url: None)
        with pytest.raises(SourceAccessError, match="without an early retry"):
            client.fetch(None, kind="players", season=2024)
    assert len(requests) == 1 and attempts[0]["finish"]
