"""Read season totals from saved HTML without silently accepting changed layouts.

Source strings are deliberately retained. Numeric conversion belongs to dbt.
The HTML parser also reads table markup stored inside HTML comments.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from html.parser import HTMLParser
import re

PARSER_VERSION = "season-totals-v1.1"
RAW_CONTRACT_VERSION = 1
PLAYER_TABLES = {"totals_stats", "totals", "player-stats-base"}
TEAM_TABLES = {"totals-team", "team_stats", "team-stats-base", "team_totals"}
OPPONENT_TABLES = {"totals-opponent", "opponent_stats", "opponent-stats-base"}
MISC_TABLES = {"misc_stats", "misc", "team-stats-misc"}
PLAYER_LINK = re.compile(r"/players/[a-z]/([a-zA-Z0-9]+)\.html(?:[?#].*)?$")
TEAM_LINK = re.compile(r"/teams/([A-Z0-9]{2,4})/(\d{4})\.html(?:[?#].*)?$")
ALL_TEAMS = {"TOT", "2TM", "3TM", "4TM", "5TM"}
TOTAL_KEYS = ("g", "mp", "fg", "fga", "fg3", "fg3a", "ft", "fta", "orb", "drb", "reb", "ast", "stl", "blk", "tov", "pf", "pts")
ALIASES = {
    "player": "player_name", "name_display": "player_name", "name": "player_name",
    "position": "pos", "games": "g", "games_played": "g", "games_started": "gs",
    "minutes": "mp", "minutes_played": "mp", "fgm": "fg", "fg3m": "fg3",
    "3p": "fg3", "3pa": "fg3a", "ftm": "ft", "trb": "reb",
    "total_rebounds": "reb", "offensive_rebounds": "orb", "defensive_rebounds": "drb",
    "assists": "ast", "steals": "stl", "blocks": "blk", "turnovers": "tov",
    "personal_fouls": "pf", "points": "pts", "wins": "wins", "w": "wins",
    "losses": "losses", "l": "losses", "opp_pts": "opponent_pts",
    "team": "team_name", "team_name": "team_name", "team_id": "team_name",
    "team_name_abbr": "team_name", "team_abbreviation": "team_name",
}
MINIMUM_COLUMNS = {"g", "fg", "fga", "ft", "fta", "reb", "ast", "pts"}


class ParseError(ValueError):
    """The source cannot be used safely with this version of the raw contract."""


@dataclass
class Cell:
    key: str
    text_parts: list[str] = field(default_factory=list)
    links: list[str] = field(default_factory=list)

    @property
    def text(self) -> str:
        return "".join(self.text_parts).strip()


@dataclass
class Row:
    cells: list[Cell] = field(default_factory=list)
    repeated_header: bool = False
    section: str = ""


@dataclass
class Table:
    table_id: str
    rows: list[Row] = field(default_factory=list)
    caption_parts: list[str] = field(default_factory=list)


@dataclass(frozen=True)
class ParsedRow:
    source_table_id: str
    row_ordinal: int
    source_player_id: str | None
    source_team_token: str
    source_payload: dict[str, str]


class _TableParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.tables: list[Table] = []
        self.table: Table | None = None
        self.row: Row | None = None
        self.cell: Cell | None = None
        self.section = ""
        self.in_caption = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        if tag == "table":
            if self.table is not None:
                raise ParseError("Nested tables are unsupported; inspect the source layout.")
            self.table = Table(values.get("id") or "")
        elif self.table is not None:
            if tag == "caption":
                self.in_caption = True
            elif tag in {"thead", "tbody", "tfoot"}:
                self.section = tag
            elif tag == "tr":
                self.row = Row(repeated_header="thead" in (values.get("class") or "").split(), section=self.section)
            elif tag in {"th", "td"} and self.row is not None:
                self.cell = Cell(values.get("data-stat") or "")
            elif tag == "a" and self.cell is not None and values.get("href"):
                self.cell.links.append(values["href"] or "")

    def handle_data(self, data: str) -> None:
        if self.cell is not None:
            self.cell.text_parts.append(data)
        elif self.in_caption and self.table is not None:
            self.table.caption_parts.append(data)

    def handle_endtag(self, tag: str) -> None:
        if tag in {"th", "td"} and self.cell is not None and self.row is not None:
            self.row.cells.append(self.cell)
            self.cell = None
        elif tag == "tr" and self.row is not None and self.table is not None:
            self.table.rows.append(self.row)
            self.row = None
        elif tag == "table" and self.table is not None:
            self.tables.append(self.table)
            self.table = None
            self.row = None
            self.cell = None
            self.in_caption = False
        elif tag == "caption":
            self.in_caption = False
        elif tag in {"thead", "tbody", "tfoot"}:
            self.section = ""

    def handle_comment(self, data: str) -> None:
        if "<table" in data.lower():
            nested = _TableParser()
            nested.feed(data)
            nested.close()
            self.tables.extend(nested.tables)


def _payload(row: Row) -> dict[str, str]:
    result: dict[str, str] = {}
    for cell in row.cells:
        if not cell.key:
            continue  # Rank columns without data-stat never identify a player/team.
        if cell.key in result:
            raise ParseError(f"Duplicate column {cell.key!r} in one row.")
        result[cell.key] = cell.text
    for key, value in list(result.items()):
        canonical = ALIASES.get(key, key)
        if canonical in result and canonical != key and result[canonical] != value:
            raise ParseError(f"Conflicting aliases for {canonical!r}.")
        result[canonical] = value
    return result


def _link_id(row: Row, pattern: re.Pattern[str]) -> str | None:
    ids = {match.group(1) for cell in row.cells for link in cell.links if (match := pattern.search(link))}
    if len(ids) > 1:
        raise ParseError("One row links to multiple distinct players or teams.")
    return next(iter(ids), None)


def _data_rows(table: Table) -> list[Row]:
    rows = []
    for row in table.rows:
        if row.section in {"thead", "tfoot"} or row.repeated_header or not row.cells:
            continue
        values = _payload(row)
        if values.get("g", "").strip().upper() in {"G", "GP"}:
            continue
        if not any(cell.text or cell.links for cell in row.cells):
            continue
        rows.append(row)
    return rows


def _choose_table(tables: list[Table], allowed: set[str], kind: str) -> Table:
    selected = [table for table in tables if table.table_id in allowed]
    if len(selected) != 1:
        raise ParseError(f"Expected exactly one {kind} totals table; found {len(selected)}. Supported IDs: {sorted(allowed)}")
    return selected[0]


def _team_extras(tables: list[Table], season: int) -> dict[str, dict[str, str]]:
    extras: dict[str, dict[str, str]] = {}
    for table in tables:
        if table.table_id not in OPPONENT_TABLES | MISC_TABLES:
            continue
        seen: set[str] = set()
        for row in _data_rows(table):
            for cell in row.cells:
                for link in cell.links:
                    if (match := TEAM_LINK.search(link)) and int(match.group(2)) != season:
                        raise ParseError(f"Supplemental team link belongs to season {match.group(2)}, expected {season}.")
            code = _link_id(row, TEAM_LINK)
            if not code:
                continue
            if code in seen:
                raise ParseError(f"Duplicate supplemental team {code} in {table.table_id}.")
            seen.add(code)
            payload = _payload(row)
            current = extras.setdefault(code, {})
            if table.table_id in OPPONENT_TABLES:
                if "pts" in payload:
                    current["opponent_pts"] = payload["pts"]
            else:
                for key in ("wins", "losses", "opponent_pts"):
                    if key in payload:
                        current[key] = payload[key]
    return extras


def detected_tables(html: str | bytes) -> list[str]:
    """List table IDs in saved HTML, including tables hidden in comments."""
    text = html.decode("utf-8-sig") if isinstance(html, bytes) else html
    parser = _TableParser()
    parser.feed(text)
    parser.close()
    return [table.table_id for table in parser.tables]


def parse_rows(html: str | bytes, kind: str, season: int, phase: str = "regular") -> list[ParsedRow]:
    """Extract one complete player/team totals table, preserving raw strings.

    ``season`` is the season's ending year, e.g. 2024 means 2023–24.
    Missing individual values remain ``""``; missing required columns reject the page.
    """
    if kind not in {"players", "teams"} or phase not in {"regular", "playoffs"}:
        raise ValueError("kind must be players|teams and phase regular|playoffs")
    if not 1947 <= season <= 2100:
        raise ValueError("season must be an NBA season ending year between 1947 and 2100")
    if isinstance(html, bytes):
        html = html.decode("utf-8-sig")
    if not html.strip():
        raise ParseError("Empty HTML cannot produce a usable snapshot.")
    lowered = html.lower()
    if any(marker in lowered for marker in ("cf-chl-", "captcha", "verify you are human", "access denied", "just a moment...")):
        raise ParseError("Source returned an access challenge/block page; stop fetching.")
    parser = _TableParser()
    parser.feed(html)
    parser.close()
    table = _choose_table(parser.tables, PLAYER_TABLES if kind == "players" else TEAM_TABLES, kind)
    caption = " ".join(table.caption_parts).lower()
    if any(label in caption for label in ("per game", "per-game", "per 36", "per 100")):
        raise ParseError("This is a rate table; a season totals table is required.")
    extras = _team_extras(parser.tables, season) if kind == "teams" else {}
    parsed: list[ParsedRow] = []
    seen: set[tuple[str | None, str]] = set()
    for ordinal, row in enumerate(_data_rows(table), 1):
        payload = _payload(row)
        # League averages are summaries, not real teams.
        if kind == "teams" and payload.get("team_name", "").rstrip("*") in {"League Average", "NBA"}:
            continue
        missing = MINIMUM_COLUMNS - payload.keys()
        if missing:
            raise ParseError(f"Changed totals contract: missing columns {sorted(missing)} at row {ordinal}.")
        player_id = _link_id(row, PLAYER_LINK) if kind == "players" else None
        linked_team = _link_id(row, TEAM_LINK)
        # Validate linked season rather than silently mixing records from another year.
        for cell in row.cells:
            for link in cell.links:
                if (match := TEAM_LINK.search(link)) and int(match.group(2)) != season:
                    raise ParseError(f"Team link belongs to season {match.group(2)}, expected {season}.")
        if kind == "players":
            token = payload.get("team_name", "").strip().upper()
            team = linked_team or (token if token in ALL_TEAMS else None)
            if not player_id or not team or not payload.get("player_name"):
                raise ParseError(f"Missing stable player/team identity at row {ordinal}.")
            canonical_keys = ("player_name", "pos", "gs", *TOTAL_KEYS)
        else:
            team = linked_team
            if not team or not payload.get("team_name"):
                raise ParseError(f"Missing stable team identity at row {ordinal}.")
            payload.update({key: value for key, value in extras.get(team, {}).items() if not payload.get(key)})
            if not all(payload.get(key, "").strip() for key in ("g", "wins", "losses")):
                raise ParseError("Team season totals require nonblank games, wins, and losses; this page/layout is unsupported.")
            canonical_keys = ("team_name", "wins", "losses", "opponent_pts", *TOTAL_KEYS)
        for key in canonical_keys:
            payload.setdefault(key, "")
        identity = (player_id, team)
        if identity in seen:
            raise ParseError(f"Duplicate row identity {identity}; no partial batch will be loaded.")
        seen.add(identity)
        parsed.append(ParsedRow(table.table_id, ordinal, player_id, team, payload))
    if not parsed:
        raise ParseError("Totals table has no usable player/team rows.")
    return parsed
