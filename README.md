# donnied

Local daemon and web dashboard for the watch.

## Run

```sh
go run ./cmd/donnied
```

By default the web dashboard listens on all interfaces on port 80. The daemon logs usable URLs on startup, for example:

```txt
urls=[http://127.0.0.1/ http://192.168.1.24/]
```

Open the LAN IP URL from another device on the same network.

If port 80 is not available or requires privileges, use another port:

```sh
DONNIED_HTTP_ADDR=0.0.0.0:8080 go run ./cmd/donnied
```

Then open the logged URL, e.g. `http://192.168.1.24:8080/`.

## Web UI development

```sh
cd frontend
npm install
npm run dev
```

The Vite dev server proxies `/api` to the daemon.

## Production web assets

```sh
cd frontend
npm run build
```

The build writes embedded assets to `internal/web/dist`.

## Notes

There is no built-in auth yet. Only expose the LAN listener on trusted networks.
