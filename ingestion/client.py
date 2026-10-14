"""Conservative HTTP access for a source the operator is authorized to use."""

from __future__ import annotations

from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import time
from urllib.parse import urlparse
from urllib.robotparser import RobotFileParser

import httpx
from psycopg import Connection
from ingestion.provenance import ORIGIN, canonical_source_url

from ingestion.store import (
    ImportResult, accept_html, accept_not_modified, latest_usable_snapshot,
    open_ingest, record_attempt,
)

MIN_INTERVAL = 3.1
MAX_BYTES = 8 * 1024 * 1024
MAX_RETRY_DELAY = 120.0
RETRY_STATUSES = {408, 429, 500, 502, 503, 504}


class SourceAccessError(RuntimeError):
    pass


def source_url(kind: str, season: int, phase: str) -> str:
    return canonical_source_url(kind, season, phase)


def retry_delay(value: str | None, attempt: int, now: datetime | None = None) -> float:
    """Respect Retry-After; a very long wait causes a halt instead of an early retry."""
    if value:
        try:
            return max(0.0, float(value))
        except ValueError:
            try:
                date = parsedate_to_datetime(value)
                if date.tzinfo is None:
                    date = date.replace(tzinfo=timezone.utc)
                return max(0.0, (date - (now or datetime.now(timezone.utc))).total_seconds())
            except (ValueError, TypeError, OverflowError):
                pass
    return min(2.0 ** attempt, MAX_RETRY_DELAY)


class SourceClient:
    def __init__(self, *, allow_live: bool, user_agent: str = "NBAStatsLearning/0.1", interval: float = MIN_INTERVAL, transport: httpx.BaseTransport | None = None) -> None:
        if not allow_live:
            raise SourceAccessError("Live fetching requires --allow-live after you obtain authorized source access.")
        if interval < MIN_INTERVAL:
            raise ValueError(f"The request interval must be at least {MIN_INTERVAL} seconds.")
        if not user_agent.strip():
            raise ValueError("A descriptive user agent is required")
        self.interval = interval
        self.user_agent = user_agent
        self.http = httpx.Client(headers={"User-Agent": user_agent}, timeout=httpx.Timeout(20.0, connect=10.0), follow_redirects=False, transport=transport)
        self.last_request: float | None = None
        self.robots: RobotFileParser | None = None

    def __enter__(self) -> SourceClient:
        return self

    def __exit__(self, *args: object) -> None:
        self.http.close()

    def _pace(self) -> None:
        if self.last_request is not None:
            time.sleep(max(0.0, self.interval - (time.monotonic() - self.last_request)))
        self.last_request = time.monotonic()

    def _request(self, url: str, headers: dict[str, str] | None = None) -> tuple[int, dict[str, str], bytes, int]:
        if urlparse(url).netloc != "www.basketball-reference.com":
            raise SourceAccessError("Only the configured source host is permitted.")
        self._pace()
        started = time.monotonic()
        with self.http.stream("GET", url, headers=headers) as response:
            chunks: list[bytes] = []
            size = 0
            for chunk in response.iter_bytes():
                size += len(chunk)
                if size > MAX_BYTES:
                    raise SourceAccessError("Source response exceeded the 8 MiB limit.")
                chunks.append(chunk)
            return response.status_code, dict(response.headers), b"".join(chunks), max(0, int((time.monotonic() - started) * 1000))

    def _check_robots(self, url: str) -> None:
        if self.robots is None:
            try:
                status, _, body, _ = self._request(f"{ORIGIN}/robots.txt")
            except (httpx.HTTPError, SourceAccessError) as error:
                raise SourceAccessError("Could not verify robots.txt; fetching stopped.") from error
            if status != 200:
                raise SourceAccessError(f"robots.txt returned HTTP {status}; fetching stopped.")
            text = body.decode("utf-8-sig", errors="replace")
            if "user-agent:" not in text.lower() or "<html" in text.lower():
                raise SourceAccessError("robots.txt is not a valid rules file; fetching stopped.")
            # stdlib RobotFileParser does not implement RFC wildcard/end-anchor rules.
            # Never treat unsupported patterns as permission to fetch.
            for line in text.splitlines():
                directive, separator, rule = line.partition(":")
                if separator and directive.strip().lower() in {"allow", "disallow"}:
                    pattern = rule.split("#", 1)[0].strip()
                    if "*" in pattern or "$" in pattern:
                        raise SourceAccessError("robots.txt uses unsupported wildcard rules; fetching stopped for review.")
            rules = RobotFileParser()
            rules.parse(text.splitlines())
            self.robots = rules
            self.interval = max(self.interval, float(rules.crawl_delay(self.user_agent) or rules.crawl_delay("*") or 0))
        if not self.robots.can_fetch(self.user_agent, url):
            raise SourceAccessError("robots.txt disallows this URL; fetching stopped.")

    def fetch(self, connection: Connection, *, kind: str, season: int, phase: str = "regular") -> ImportResult:
        url = source_url(kind, season, phase)
        context = open_ingest(connection, kind=kind, season=season, phase=phase, source_name="basketball_reference", url=url)
        try:
            self._check_robots(url)
        except SourceAccessError:
            record_attempt(connection, context, outcome="blocked", error_code="ROBOTS_UNVERIFIED_OR_DISALLOWED", finish=True)
            raise
        existing = latest_usable_snapshot(connection, context.page_id)
        headers: dict[str, str] = {}
        if existing:
            old = {key.lower(): value for key, value in existing["headers"].items()}
            if old.get("etag"):
                headers["If-None-Match"] = old["etag"]
            if old.get("last-modified"):
                headers["If-Modified-Since"] = old["last-modified"]
        for attempt in range(1, 4):
            try:
                status, response_headers, body, elapsed_ms = self._request(url, headers)
            except httpx.HTTPError as error:
                record_attempt(connection, context, outcome="retryable_error", number=attempt, error_code=type(error).__name__, finish=attempt == 3)
                if attempt == 3:
                    raise SourceAccessError("Source network request failed after three attempts.") from error
                time.sleep(retry_delay(None, attempt))
                continue
            except SourceAccessError:
                record_attempt(connection, context, outcome="permanent_error", number=attempt, error_code="RESPONSE_LIMIT_OR_HOST", finish=True)
                raise
            if status == 304:
                return accept_not_modified(connection, context, number=attempt, elapsed_ms=elapsed_ms)
            if status in {401, 403}:
                record_attempt(connection, context, outcome="blocked", number=attempt, status=status, error_code="SOURCE_ACCESS_DENIED", elapsed_ms=elapsed_ms, finish=True)
                raise SourceAccessError(f"Source returned HTTP {status}; stop and resolve source permission/access.")
            if status in RETRY_STATUSES:
                delay = retry_delay(response_headers.get("retry-after"), attempt)
                final = attempt == 3 or delay > MAX_RETRY_DELAY
                record_attempt(connection, context, outcome="retryable_error", number=attempt, status=status, error_code="SOURCE_RETRYABLE_STATUS", elapsed_ms=elapsed_ms, finish=final)
                if final:
                    raise SourceAccessError(f"Source returned HTTP {status}; fetching stopped without an early retry.")
                time.sleep(delay)
                continue
            if status != 200:
                record_attempt(connection, context, outcome="permanent_error", number=attempt, status=status, error_code="UNEXPECTED_HTTP_STATUS", elapsed_ms=elapsed_ms, finish=True)
                raise SourceAccessError(f"Source returned HTTP {status}; no redirect/bypass attempted.")
            lowered = body[:262144].lower()
            if any(marker in lowered for marker in (b"cf-chl-", b"captcha", b"verify you are human", b"access denied", b"just a moment...")):
                record_attempt(connection, context, outcome="blocked", number=attempt, status=status, error_code="SOURCE_CHALLENGE", elapsed_ms=elapsed_ms, finish=True)
                raise SourceAccessError("Source returned a challenge page; fetching stopped.")
            if not response_headers.get("content-type", "").lower().startswith(("text/html", "application/xhtml+xml")):
                record_attempt(connection, context, outcome="permanent_error", number=attempt, status=status, error_code="UNEXPECTED_CONTENT_TYPE", elapsed_ms=elapsed_ms, finish=True)
                raise SourceAccessError("Source response is not HTML; fetching stopped.")
            return accept_html(connection, context, body, response_headers=response_headers, status=status, attempt_number=attempt, elapsed_ms=elapsed_ms)
        raise AssertionError("Unreachable retry state")
