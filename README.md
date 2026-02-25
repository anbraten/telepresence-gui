# tp-gui

A lightweight desktop GUI for [Telepresence](https://www.telepresence.io/) — pick a Kubernetes namespace, start intercepts, and watch live traffic routing, all from your browser.

Built as a single static Go binary with an embedded web UI. No Electron, no Node runtime.

---

## Features

- **Namespace picker** — lists available namespaces via `kubectl`; click a row to connect
- **Workloads list** — shows all interceptable Deployments/Services in the connected namespace; intercepted rows float to the top
- **Per-service intercept control** — start and stop intercepts individually, with per-row loading spinners
- **Live SSE updates** — status and workload list refresh automatically every 3 s via Server-Sent Events
- **Event log** — scrollable live stream of all server-side events
- **Debug mode** — `--debug` prints every telepresence/kubectl subprocess call with stdout, stderr and timing

---

## Requirements

- [`telepresence`](https://www.telepresence.io/docs/latest/quick-start/) v2 on `$PATH`
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/) on `$PATH` with a valid kubeconfig

---

## Installation

### Build from source

```bash
git clone https://github.com/user/tp-gui
cd tp-gui
make build          # outputs dist/tp-gui (static Linux amd64 binary)
```

Or for a quick local run without installing:

```bash
go run .
```

---

## Usage

```
tp-gui [flags]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-p`, `--port` | `7777` | Port the HTTP server listens on |
| `--open-browser` | `false` | Open the UI in your default browser on startup |
| `--debug` | `false` | Log all telepresence/kubectl subprocess calls with timing |

### Examples

```bash
# Start on default port and open the browser
tp-gui --open-browser

# Use a different port
tp-gui --port 8888

# Debug mode — prints every CLI call and its output
tp-gui --debug
```

Then open [http://localhost:7777](http://localhost:7777).

---

## Workflow

1. **Launch** `tp-gui` — the UI discovers namespaces automatically
2. **Pick a namespace** — click a row; tp-gui runs `telepresence connect --namespace <ns>`
3. **Intercept a service** — click **Intercept**, configure local port, optional remote port / env file / mount path
4. **Develop locally** — traffic for that service is now routed to your machine
5. **Stop** — click **Stop** on the row, or **Disconnect** in the header to quit the telepresence session entirely

---

## Architecture

```
tp-gui (Go binary)
├── main.go           CLI entry point (cobra), HTTP server setup
├── server.go         HTTP handlers + SSE broadcaster (go:embed static/)
├── telepresence.go   Wrappers around telepresence / kubectl CLI calls
└── static/
    ├── index.html    Single-page app (Alpine.js v3 + Tailwind CSS CDN)
    ├── app.js        Alpine store — state, API calls, SSE client
    └── style.css     Custom styles
```

The frontend communicates with the Go backend over a small JSON REST API:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/status` | Current connection status |
| `GET` | `/api/namespaces` | List namespaces via kubectl |
| `GET` | `/api/workloads?namespace=` | List interceptable workloads |
| `POST` | `/api/connect` | `telepresence connect` |
| `POST` | `/api/intercept` | `telepresence intercept` |
| `POST` | `/api/leave` | `telepresence leave` |
| `POST` | `/api/quit` | `telepresence quit` |
| `GET` | `/events` | SSE stream (status + workloads every 3 s) |

---

## Development

```bash
make run    # go run .
make test   # go test ./...
make build  # static binary → dist/tp-gui
```

---

## License

[MIT](LICENSE)
