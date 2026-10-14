[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][ValidateSet('historical','daily')][string]$Mode,
    [int]$Season,
    [int]$StartSeason,
    [int]$EndSeason,
    [ValidateSet('regular','playoffs')][string]$Phase = 'regular',
    [switch]$AllowLive
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if (-not $AllowLive) {
    throw 'Use saved-page imports unless you have approved source access. Approved live fetching requires -AllowLive.'
}
$taskRoot = Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot
try {
    if ($Mode -eq 'historical') {
        if ($StartSeason -lt 2005 -or $EndSeason -lt $StartSeason) {
            throw 'Use a valid historical range starting at 2005 or later. Expand the reviewed team seed before earlier imports.'
        }
        $taskFetchArgs = @('fetch', '--start-season', "$StartSeason", '--end-season', "$EndSeason", '--phase', $Phase, '--allow-live')
    } else {
        if ($Season -lt 2005) { throw 'Provide a valid season ending year, for example -Season 2027.' }
        $taskFetchArgs = @('daily', '--season', "$Season", '--phase', $Phase, '--allow-live')
    }
    & docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli @taskFetchArgs
    if ($LASTEXITCODE -ne 0) { throw 'Source import failed; analytics publication was not started.' }
    & docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source basketball_reference
    if ($LASTEXITCODE -ne 0) { throw 'Build or publication failed; inspect the pipeline output.' }
}
finally {
    Pop-Location
}
