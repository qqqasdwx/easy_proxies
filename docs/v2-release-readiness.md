# V2 Merge Release Readiness

Date: 2026-05-02

## Automated Checks

| Check | Result |
| --- | --- |
| `go test ./...` | Passed |
| `CGO_ENABLED=1 go test -race ./internal/config ./internal/monitor ./internal/store ./internal/subscription` | Passed |
| `npm ci --prefix frontend` | Passed |
| `npm run build --prefix frontend` | Passed |
| `npm audit --omit=dev --prefix frontend` | Passed, 0 production vulnerabilities |
| `docker build -t easy_proxies:merge-v2 .` | Passed |
| `git diff --check` | Passed |

Note: race tests require CGO and a C compiler. This VM needed `gcc` and `libc6-dev` installed before the race run.

## Manual Scenario Matrix

| Scenario | Status | Notes |
| --- | --- | --- |
| File mode startup | Ready for operator validation | Requires real node URIs in `nodes.txt` or `config.yaml`. |
| Store mode startup | Ready for operator validation | `database_path: data/data.db`; keep `./data` mounted. |
| `pool` / `multi-port` / `hybrid` modes | Ready for operator validation | Build/tests cover config generation; live proxy verification needs valid upstreams. |
| `http` / `socks5` / `mixed` inbound protocols | Ready for operator validation | Config and builder tests cover protocol selection. |
| Subscription refresh and reload | Ready for operator validation | Requires a real subscription URL. |
| WebUI node add/disable/delete/batch delete | Ready for operator validation | API tests cover backend paths; browser flow needs live WebUI session. |
| GeoIP region route | Ready for operator validation | Requires GeoIP DB download and reachable upstreams. |
| Manual blacklist/release | Ready for operator validation | Backend API retained and React controls present. |
| Log file output and WebUI log view | Ready for operator validation | React log panel reads `/api/logs`; file output depends on `log.output: file`. |

## Release Notes

- React/Vite WebUI is now the embedded dashboard; Docker builds the frontend before the Go binary.
- SQLite store persists WebUI-managed nodes, disabled state, sessions, and traffic totals.
- `config.yaml` and `nodes.txt` remain supported; do not remove existing file-based workflows during upgrade.
- Required mutable Docker mounts: `config.yaml`, `nodes.txt`, `logs/`, and `data/`.
