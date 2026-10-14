@echo off
setlocal
pushd "%~dp0.."
if not exist .env (
  echo Create a root .env from .env.example and set DATABASE_PASS first.
  goto failed
)

echo 1. Prepare dependencies
docker compose --env-file .env -f docker/compose.yaml --profile tools pull --ignore-buildable
if errorlevel 1 goto failed
docker compose --env-file .env -f docker/compose.yaml --profile tools build
if errorlevel 1 goto failed

echo 2. Start PostgreSQL
docker compose --env-file .env -f docker/compose.yaml up -d --wait postgres
if errorlevel 1 goto failed

echo 3. Import the synthetic sample
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm python python -m ingestion.cli sample
if errorlevel 1 goto failed

echo 4. Build, test, and publish analytics
docker compose --env-file .env -f docker/compose.yaml --profile tools run --rm pipeline run --source demo_fixture
if errorlevel 1 goto failed

echo 5. Start the API and React app
docker compose --env-file .env -f docker/compose.yaml up -d api web
if errorlevel 1 goto failed

echo Open http://localhost:5173 or your WEB_PORT from .env.
echo Request http://localhost:8080/api/v1/players?season=2024 or your API_PORT from .env.
popd
exit /b 0

:failed
echo Checkpoint stopped. See the error above; no later stages were started.
popd
exit /b 1
