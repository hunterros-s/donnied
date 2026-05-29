# donnied web UI

Preact + Vite dashboard served by `donnied` at `http://127.0.0.1:8080/`.

The UI uses Tailwind CSS for a minimal component layer and Apache ECharts for time-series, activity, and sleep-stage visualizations.

## Development

```sh
cd frontend
npm install
npm run dev
```

The Vite dev server proxies `/api` to the daemon on `127.0.0.1:8080`.

## Production assets

```sh
cd frontend
npm run build
```

The build writes embedded assets to `../internal/web/dist`, which are served by the Go HTTP server.
