# V2 Merge Release Readiness

Date: 2026-05-07

## Automated Checks

| Check | Result |
| --- | --- |
| `go test ./...` | Passed |
| `npm --prefix frontend run lint` | Passed |
| `npm --prefix frontend run build` | Passed |
| `npm --prefix frontend audit --audit-level=moderate` | Passed |
| `git diff --check` | Passed |
| `docker build -t easy_proxies:dev .` | Passed |
| `go test ./internal/subscription ./internal/boxmgr ./internal/monitor ./internal/config` | Passed |
| `scripts/e2e/docker-proxy-flow.sh` | Passed |
| GitHub Actions image publishing | Verified |
| Docker E2E with clean SQLite, host networking, local SOCKS upstream, and local HTTP target | Passed |
| `pool`, `multi-port`, and `hybrid` proxy requests through Docker | Passed |
| Subscription refresh after WebUI settings changes | Passed |
| Restart persistence for runtime settings, manual nodes, and subscription nodes | Passed |

## Manual Scenario Matrix

| Scenario | Status | Notes |
| --- | --- | --- |
| Clean Docker startup | Passed | Starts without `config.yaml` or `nodes.txt`; only SQLite is required. |
| SQLite runtime persistence | Passed | Runtime settings, manual nodes, subscription sources, and subscription nodes survive container restart. |
| `pool` / `multi-port` / `hybrid` modes | Passed | Verified with a real SOCKS upstream and HTTP proxy requests. |
| Manual and subscription node coexistence | Passed | Both sources appear in node management and participate in reloads. |
| Subscription refresh and reload | Passed | Refresh preserves the current runtime settings instead of reverting to startup defaults. |
| Management settings safety | Passed | `/api/settings` exposes only `management.probe_target`; management port/password remain environment-only. |
| WebUI settings model | Passed | Runtime mode, listeners, DNS, GeoIP, health checks, external IP, SSL verification, and sing-box log level are browser-managed. |

## Release Notes

- SQLite is the only runtime persistence source. `config.yaml` and `nodes.txt` are not read, written, or migrated.
- Required Docker mounts are `./data:/app/data` for SQLite/GeoIP data and `./logs:/app/logs` for logs.
- `MANAGEMENT_PORT` controls the WebUI/API port at process start. `MANAGEMENT_PASSWORD` is read only from the process environment.
- Subscription refresh now rebuilds sing-box from the current runtime settings, so WebUI changes are preserved across manual and automatic refreshes.
- `.github/workflows/docker-publish.yml` still triggers on `main`, release tags, pull requests, and manual dispatch. GHCR image names are derived from `${{ github.repository }}`.
