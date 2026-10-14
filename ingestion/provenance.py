"""Validate provider-specific source identity without fetching anything."""

from __future__ import annotations

from dataclasses import dataclass
from html.parser import HTMLParser
from urllib.parse import urljoin, urlsplit

ORIGIN = "https://www.basketball-reference.com"


class ProvenanceError(ValueError):
    """Declared source metadata contradicts an available source URL."""


@dataclass(frozen=True)
class CanonicalEvidence:
    urls: tuple[str, ...]
    verified: bool


def canonical_source_url(kind: str, season: int, phase: str) -> str:
    if kind not in {"players", "teams"} or phase not in {"regular", "playoffs"}:
        raise ValueError("Invalid kind/phase")
    if not 1947 <= season <= 2100:
        raise ValueError("season must be between 1947 and 2100")
    folder = "leagues" if phase == "regular" else "playoffs"
    suffix = "_totals" if kind == "players" else ""
    return f"{ORIGIN}/{folder}/NBA_{season}{suffix}.html"


def _matches_url(value: str, expected: str, *, allow_archival_http: bool = False) -> bool:
    try:
        parsed = urlsplit(value)
        schemes = {"https", "http"} if allow_archival_http else {"https"}
        return (
            parsed.scheme in schemes
            and parsed.hostname in {"www.basketball-reference.com", "basketball-reference.com"}
            and parsed.port in {None, 443 if parsed.scheme == "https" else 80}
            and parsed.username is None and parsed.password is None
            and parsed.path == urlsplit(expected).path
            and not parsed.query
        )
    except ValueError:
        return False


def validate_source_url(source_name: str, url: str, kind: str, season: int, phase: str) -> str:
    """Return a normalized BRef identity, leaving other provider URLs unchanged."""
    if source_name.strip() != "basketball_reference":
        return url.strip()
    expected = canonical_source_url(kind, season, phase)
    if not _matches_url(url.strip(), expected):
        raise ProvenanceError(f"Source URL does not match {kind}/{season}/{phase}. Expected {expected}")
    return expected


class _CanonicalParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.urls: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if tag == "link" and "canonical" in (values.get("rel") or "").lower().split():
            self.urls.append((values.get("href") or "").strip())


def validate_html_canonical(html: bytes | str, *, source_name: str | None, kind: str, season: int, phase: str) -> CanonicalEvidence:
    """Reject contradictory BRef canonical metadata; absence does not prove phase.

    Generic/synthetic HTML remains supported. Its canonical links are reported,
    but no provider-specific interpretation or source verification is claimed.
    """
    text = html.decode("utf-8-sig") if isinstance(html, bytes) else html
    parser = _CanonicalParser()
    parser.feed(text)
    parser.close()
    urls = tuple(dict.fromkeys(parser.urls))
    known_provider = (source_name or "").strip() == "basketball_reference"
    complete_matches: list[bool] = []
    if known_provider and urls:
        expected = canonical_source_url(kind, season, phase)
        for value in urls:
            try:
                parsed = urlsplit(value)
            except ValueError as error:
                raise ProvenanceError("HTML canonical URL is malformed.") from error
            if not value:
                raise ProvenanceError(f"HTML canonical URL contradicts {kind}/{season}/{phase}. Expected {expected}")
            if parsed.scheme or parsed.netloc or value.startswith("/"):
                # Resolve root/protocol-relative URLs against the source origin,
                # never against a folder inferred from the caller's phase.
                resolved = urljoin(ORIGIN + "/", value)
                if not _matches_url(resolved, expected, allow_archival_http=True):
                    raise ProvenanceError(f"HTML canonical URL contradicts {kind}/{season}/{phase}. Expected {expected}")
                complete_matches.append(True)
            else:
                # Bare filenames carry year/kind evidence but no league/playoff
                # folder. Fragment-only links carry no page identity at all.
                # Do not invent that missing evidence by joining to expected.
                expected_filename = urlsplit(expected).path.rsplit("/", 1)[-1]
                if parsed.query or parsed.path not in {"", expected_filename}:
                    raise ProvenanceError(f"Relative HTML canonical URL conflicts with, or cannot establish, {kind}/{season}/{phase}. Expected {expected}")
                complete_matches.append(False)
    return CanonicalEvidence(urls, bool(complete_matches and all(complete_matches)))
