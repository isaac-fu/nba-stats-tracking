import { useEffect, useRef, useState } from 'react'

const playerMetrics = [
  ['games_played', 'Games', 'integer'], ['pts_per_game', 'Points / game', 'decimal'],
  ['reb_per_game', 'Rebounds / game', 'decimal'], ['ast_per_game', 'Assists / game', 'decimal'],
  ['fg_pct', 'Field goals', 'percent'], ['fg3_pct', 'Three-pointers', 'percent'],
  ['ft_pct', 'Free throws', 'percent'], ['efg_pct', 'Effective FG', 'percent'],
  ['ts_pct', 'True shooting', 'percent'], ['pts', 'Total points', 'integer'],
  ['reb', 'Total rebounds', 'integer'], ['ast', 'Total assists', 'integer'],
]
const teamMetrics = [
  ['games_played', 'Games', 'integer'], ['wins', 'Wins', 'integer'], ['losses', 'Losses', 'integer'],
  ['win_pct', 'Win percentage', 'percent'], ['pts_per_game', 'Points / game', 'decimal'],
  ['points_against_per_game', 'Opponent points / game', 'decimal'],
  ['point_differential_per_game', 'Point difference / game', 'decimal'],
  ['reb_per_game', 'Rebounds / game', 'decimal'], ['ast_per_game', 'Assists / game', 'decimal'],
  ['fg_pct', 'Field goals', 'percent'], ['ts_pct', 'True shooting', 'percent'],
]

function format(value, kind = 'decimal') {
  if (value === null || value === undefined) return '—'
  if (kind === 'percent') return `${(value * 100).toFixed(1)}%`
  return new Intl.NumberFormat('en-US', { maximumFractionDigits: kind === 'integer' ? 0 : 1,
    minimumFractionDigits: kind === 'integer' ? 0 : 1 }).format(value)
}

async function getJSON(path, signal) {
  const response = await fetch(path, { signal, headers: { Accept: 'application/json' } })
  const body = await response.json().catch(() => null)
  if (!response.ok) throw new Error(body?.detail || `The API returned HTTP ${response.status}.`)
  if (!body) throw new Error('The API returned an invalid response.')
  return body
}

function CourtMark() {
  return <svg viewBox="0 0 40 40" aria-hidden="true"><circle cx="20" cy="20" r="17" />
    <path d="M3 20h34M20 3v34M8 8c12 5 12 19 0 24M32 8c-12 5-12 19 0 24" /></svg>
}

export default function App() {
  const [status, setStatus] = useState(null)
  const [seasons, setSeasons] = useState([])
  const [season, setSeason] = useState('')
  const [phase, setPhase] = useState('regular')
  const [view, setView] = useState('players')
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState('pts_per_game')
  const [rows, setRows] = useState([])
  const [rowRelease, setRowRelease] = useState('')
  const [selected, setSelected] = useState([])
  const [comparison, setComparison] = useState(null)
  const [loading, setLoading] = useState(true)
  const [comparing, setComparing] = useState(false)
  const [error, setError] = useState('')
  const [compareError, setCompareError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [initialized, setInitialized] = useState(false)
  const comparisonRef = useRef(null)
  const comparisonController = useRef(null)

  useEffect(() => {
    const controller = new AbortController()
    setInitialized(false)
    setLoading(true)
    setError('')
    Promise.all([getJSON('/api/v1/status', controller.signal), getJSON('/api/v1/seasons', controller.signal)])
      .then(([metadata, available]) => {
        setStatus(metadata)
        setSeasons(available.data)
        setSeason(current => available.data.some(s => String(s.season_end_year) === current)
          ? current : String(available.data[0]?.season_end_year || ''))
        setInitialized(true)
        if (!available.data.length) setLoading(false)
      })
      .catch(err => {
        if (err.name !== 'AbortError') { setError(err.message); setLoading(false) }
      })
    return () => controller.abort()
  }, [refresh])

  useEffect(() => {
    const timer = setTimeout(() => setQuery(search.trim()), 250)
    return () => clearTimeout(timer)
  }, [search])

  useEffect(() => {
    if (!initialized || !season) return undefined
    const controller = new AbortController()
    const params = new URLSearchParams({ season, phase, q: query, sort, limit: '1000' })
    setLoading(true)
    setError('')
    getJSON(`/api/v1/${view}?${params}`, controller.signal)
      .then(body => { setRows(body.data); setRowRelease(body.release_id); setLoading(false) })
      .catch(err => { if (err.name !== 'AbortError') { setError(err.message); setRows([]); setLoading(false) } })
    return () => controller.abort()
  }, [initialized, season, phase, view, query, sort, refresh])

  useEffect(() => {
    setSelected([])
    setComparison(null)
    setCompareError('')
    setComparing(false)
    comparisonController.current?.abort()
  }, [season, phase, view, refresh])

  useEffect(() => () => comparisonController.current?.abort(), [])

  const idOf = row => row.player_id ?? row.team_season_id
  const seasonOptions = [...new Map(seasons.map(s => [String(s.season_end_year), s])).values()]
  const chosenSeason = seasonOptions.find(s => String(s.season_end_year) === season)
  const metrics = view === 'players' ? playerMetrics : teamMetrics

  function changeView(nextView) {
    if (view === nextView) return
    comparisonController.current?.abort()
    setRows([])
    setSelected([])
    setComparison(null)
    setCompareError('')
    setComparing(false)
    setLoading(true)
    setSearch('')
    setQuery('')
    setSort(nextView === 'players' ? 'pts_per_game' : 'wins')
    setView(nextView)
  }

  function toggle(row) {
    const id = idOf(row)
    setSelected(current => current.includes(id) ? current.filter(item => item !== id)
      : current.length < 5 ? [...current, id] : current)
    setComparison(null)
    setCompareError('')
    comparisonController.current?.abort()
    setComparing(false)
  }

  async function compare() {
    comparisonController.current?.abort()
    const controller = new AbortController()
    comparisonController.current = controller
    setComparing(true)
    setCompareError('')
    const params = new URLSearchParams({ season, phase, ids: selected.join(',') })
    try {
      const body = await getJSON(`/api/v1/comparisons/${view}?${params}`, controller.signal)
      setComparison(body)
      setComparing(false)
      requestAnimationFrame(() => comparisonRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }))
    } catch (err) {
      if (err.name !== 'AbortError') { setCompareError(err.message); setComparing(false) }
    }
  }

  return <div className="app-shell">
    <header className="topbar">
      <a href="/" className="brand"><span className="brand-mark"><CourtMark /></span>
        <span>COURTSIDE <span className="brand-small">NBA statistics</span></span></a>
      <div className="topbar-note"><span className="status-dot" /> PostgreSQL → API → your view</div>
    </header>

    <main>
      <section className="hero">
        <div><p className="eyebrow">THE NUMBERS BEHIND THE GAME</p>
          <h1>Explore. Compare.<br /><span>Know the game.</span></h1>
          <p className="hero-copy">Season statistics for players and teams, with clear sources and comparisons that put performance in perspective.</p>
        </div>
        <div className="hero-card"><CourtMark /><span className="hero-card-label">SEASON EXPLORER</span>
          <strong>{chosenSeason?.season_label || 'NBA'}</strong><span>{phase === 'regular' ? 'Regular season' : 'Playoffs'}</span>
        </div>
      </section>

      {status && <aside className={`source-banner ${status.sample_data ? 'sample' : ''}`}>
        <span className="source-tag">{status.sample_data ? 'SYNTHETIC DEMO' : 'PUBLISHED DATA'}</span>
        <div><strong>{status.sample_data ? 'Learning with sample data' : 'Statistics from Basketball Reference'}</strong>
          <p>{status.sample_data ? 'These invented players and totals demonstrate the pipeline. They are not NBA results.'
            : `Source: ${status.source_name}. Season totals reflect the selected imported observations.`}</p></div>
        <span className="published-at">Published {new Date(status.published_at).toLocaleDateString()}</span>
      </aside>}

      <section className="workspace" aria-label="Season statistics">
        <div className="workspace-heading">
          <div><p className="eyebrow">SEASON LEADERBOARDS</p><h2>Find your next comparison</h2></div>
          <button className="button button-quiet" onClick={() => setRefresh(value => value + 1)}>Refresh data</button>
        </div>
        <div className="controls">
          <div className="tabs" aria-label="Entity type">
            <button className={view === 'players' ? 'active' : ''} aria-pressed={view === 'players'} onClick={() => changeView('players')}>Players</button>
            <button className={view === 'teams' ? 'active' : ''} aria-pressed={view === 'teams'} onClick={() => changeView('teams')}>Teams</button>
          </div>
          <label className="field"><span>Season</span><select value={season} onChange={e => setSeason(e.target.value)} disabled={!seasons.length}>
            {!seasons.length && <option value="">No published seasons</option>}
            {seasonOptions.map(s => <option key={s.season_end_year} value={s.season_end_year}>{s.season_label}</option>)}
          </select></label>
          <label className="field"><span>Phase</span><select value={phase} onChange={e => setPhase(e.target.value)}><option value="regular">Regular season</option><option value="playoffs">Playoffs</option></select></label>
          <label className="field search-field"><span>Search {view}</span><input type="search" value={search} maxLength={100} placeholder={view === 'players' ? 'Search a player name…' : 'Search a team name…'} onChange={e => setSearch(e.target.value)} /></label>
          <label className="field"><span>Sort by</span><select value={sort} onChange={e => setSort(e.target.value)}>
            {view === 'teams' && <><option value="wins">Wins</option><option value="win_pct">Win percentage</option></>}
            <option value="pts_per_game">Points / game</option><option value="reb_per_game">Rebounds / game</option><option value="ast_per_game">Assists / game</option><option value="ts_pct">True shooting</option><option value="games_played">Games played</option><option value="name">Name</option>
          </select></label>
        </div>

        <div className="selection-bar"><span><strong>{selected.length}</strong> selected <span className="muted">· Choose 2–5 {view} to compare</span></span>
          <div><button className="text-button" disabled={!selected.length} onClick={() => { setSelected([]); setComparison(null); setCompareError(''); comparisonController.current?.abort(); setComparing(false) }}>Clear</button>
            <button className="button button-primary" disabled={selected.length < 2 || comparing || loading} onClick={compare}>{comparing ? 'Comparing…' : 'Compare selected →'}</button></div>
        </div>

        {error ? <div className="state state-error" role="alert"><h3>Statistics are unavailable</h3><p>{error}</p><p className="muted">Check that the API is running and the pipeline has published a release, then refresh.</p></div>
          : loading ? <div className="state" role="status"><span className="loader" /><h3>Loading season statistics…</h3></div>
            : !rows.length ? <div className="state"><h3>No statistics match these filters</h3><p>Try another season or phase, or clear the search.</p></div>
              : <div className="table-scroll"><table className="leaderboard"><thead><tr>
                <th className="selection-cell"><span className="sr-only">Select</span></th><th className="rank-cell">#</th><th>{view === 'players' ? 'Player' : 'Team'}</th><th>GP</th>
                {view === 'teams' && <th>W–L</th>}<th>PPG</th><th>RPG</th><th>APG</th><th>FG%</th><th>TS%</th>
              </tr></thead><tbody>{rows.map((row, index) => <tr key={idOf(row)} className={selected.includes(idOf(row)) ? 'selected' : ''}>
                <td className="selection-cell"><input type="checkbox" aria-label={`Compare ${row.display_name}`} checked={selected.includes(idOf(row))} disabled={!selected.includes(idOf(row)) && selected.length >= 5} onChange={() => toggle(row)} /></td>
                <td className="rank-cell">{index + 1}</td><td className="entity-cell"><span className="entity-avatar">{row.display_name.split(' ').map(word => word[0]).slice(0, 2).join('')}</span><div><strong>{row.display_name}</strong><span>{view === 'teams' ? row.abbreviation : row.source_scope.replaceAll('_', ' ')}</span></div></td>
                <td>{row.games_played}</td>{view === 'teams' && <td>{row.wins}–{row.losses}</td>}
                <td className="metric-primary">{format(row.pts_per_game)}</td><td>{format(row.reb_per_game)}</td><td>{format(row.ast_per_game)}</td><td>{format(row.fg_pct, 'percent')}</td><td>{format(row.ts_pct, 'percent')}</td>
              </tr>)}</tbody></table></div>}
        <div className="table-footer"><span>{!loading && !error ? `${rows.length} ${view} shown · up to 1,000 results` : 'Season totals'} · — means unavailable</span><span>GP games · PPG points · RPG rebounds · APG assists</span></div>
      </section>

      {compareError && <p className="inline-error" role="alert">{compareError}</p>}
      {comparison && <section className="comparison workspace" ref={comparisonRef} aria-label="Selected comparison">
        <div className="workspace-heading"><div><p className="eyebrow">SIDE BY SIDE</p><h2>Let the numbers speak</h2></div><span className="comparison-count">{comparison.data.length} {view}</span></div>
        <div className="table-scroll"><table className="comparison-table"><thead><tr><th>Metric</th>{comparison.data.map(row => <th key={idOf(row)}>{row.display_name}</th>)}</tr></thead>
          <tbody>{metrics.map(([key, label, kind]) => <tr key={key}><th scope="row">{label}</th>{comparison.data.map(row => <td key={idOf(row)}>{format(row[key], kind)}</td>)}</tr>)}</tbody>
        </table></div><p className="comparison-note">Values share one published release, season and phase. Shooting percentages use total makes and attempts.</p>
      </section>}

      {rowRelease && status && rowRelease !== status.release_id && <p className="inline-notice">A newer release is available. Refresh data to update the source details.</p>}
      <footer className="footer"><span>COURTSIDE <span className="muted">/ NBA statistics explorer</span></span><span>Source totals · Versioned publication · Clear comparisons</span></footer>
    </main>
  </div>
}
