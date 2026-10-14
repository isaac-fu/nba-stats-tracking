[CmdletBinding()]
param([switch]$SkipBuild)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path -Parent $PSScriptRoot
Push-Location $taskRoot

function Invoke-Compose {
    param([string[]]$Arguments)
    & docker compose --env-file .env -f docker/compose.yaml @Arguments
    if ($LASTEXITCODE -ne 0) { throw 'Docker Compose command failed. See the output above.' }
}

try {
    if (-not (Test-Path -LiteralPath '.env')) {
        throw 'Create a root .env from .env.example and set DATABASE_PASS first.'
    }
    Get-Command docker -ErrorAction Stop | Out-Null

    if (-not $SkipBuild) {
        Write-Host '1. Prepare images and dependencies'
        Invoke-Compose -Arguments @('--profile', 'tools', 'pull', '--ignore-buildable')
        Invoke-Compose -Arguments @('--profile', 'tools', 'build')
    }

    Write-Host '2. Start PostgreSQL and wait for its health check'
    Invoke-Compose -Arguments @('up', '-d', '--wait', 'postgres')

    Write-Host '3. Import the clearly labeled synthetic sample into raw tables'
    Invoke-Compose -Arguments @('--profile', 'tools', 'run', '--rm', 'python', 'python', '-m', 'ingestion.cli', 'sample')

    Write-Host '4. Build, test, and atomically publish the analytics release'
    Invoke-Compose -Arguments @('--profile', 'tools', 'run', '--rm', 'pipeline', 'run', '--source', 'demo_fixture')

    Write-Host '5. Start the Go API and React application'
    Invoke-Compose -Arguments @('up', '-d', 'api', 'web')

    Write-Host 'Check the app at http://localhost:5173 (or WEB_PORT from .env).'
    Write-Host 'Check http://localhost:8080/api/v1/players?season=2024 in Postman (or API_PORT from .env).'
    Write-Host 'A repeated run creates raw observations and a new release; it does not add duplicate season totals.'
}
finally {
    Pop-Location
}
