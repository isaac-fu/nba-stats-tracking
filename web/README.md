# React checkpoint

`build/client` contains a React application that calls the Go API. The existing
React/Vite dependencies and package lock are preserved. No additional UI package
is required.

From the project root:

```text
docker compose --env-file .env -f docker/compose.yaml up -d --build api web
```

Open `http://127.0.0.1:5173`. The dashboard provides player/team leaderboards,
season and phase filters, search, sorting and side-by-side comparisons of 2–5
entities. It shows whether the published release contains synthetic demo data.
All displayed statistics come from the API. Unavailable values display an em dash.

The Vite development server proxies `/api` requests to the Go server, avoiding a
separate browser CORS configuration. `API_PROXY_TARGET` defaults to
`http://localhost:8080`; Docker supplies `http://api:8080`.

For native development, run these commands in `web/build/client` while the API is
running on port 8080:

```text
npm ci
npm run dev
```

Checks:

```text
npm run build
npm run lint
```

Vite's proxy is a development feature. When deploying the compiled `dist` folder,
configure your production web server to forward `/api` to the Go API. Do not expect
the proxy to be embedded in the static JavaScript build.
