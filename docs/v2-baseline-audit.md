# V2 Merge Baseline Audit

Date: 2026-05-02
Branch: `feat/v2-baseline-audit`
Baseline commit: `964a0b29fbe2e47ad9e3a1b36fdcd6547f4f7ed0`

## Current Baseline

- Runtime: Go module `easy_proxies`, Go `1.24.1`, toolchain `go1.24.4`.
- Verification: `go test ./...` passes after installing Go 1.24.4 in the VM.
- Deployment: Docker builds a Go-only binary with sing-box feature tags and runs under `/etc/easy_proxies`.
- Local runtime files: `config.yaml`, `nodes.txt`, `logs/`, GeoIP databases, binaries, and `references/` are ignored.

## Current API Surface

Current WebUI/API routes in `internal/monitor/server.go`:

- `/api/auth`
- `/api/settings`
- `/api/nodes`
- `/api/nodes/{tag}/probe`
- `/api/nodes/{tag}/blacklist`
- `/api/nodes/{tag}/release`
- `/api/nodes/probe-all`
- `/api/debug`
- `/api/export`
- `/api/subscription/status`
- `/api/subscription/refresh`
- `/api/subscription/config`
- `/api/nodes/config`
- `/api/nodes/config/{name}`
- `/api/reload`
- `/api/traffic`
- `/api/logs`

V2-only API candidates:

- `/api/import`
- `/api/nodes/config/batch-toggle`
- `/api/nodes/config/batch-delete`
- `/api/nodes/traffic/stream`

## Current Configuration Model

Current top-level config fields include:

- `mode`
- `listener`
- `multi_port`
- `pool`
- `management`
- `subscription_refresh`
- `geoip`
- `log`
- `nodes`
- `nodes_file`
- `subscriptions`
- `external_ip`
- `log_level`
- `skip_cert_verify`

V2 config candidates not yet merged:

- top-level `database_path`
- `listener.protocol`
- `multi_port.protocol`
- `management.health_check_interval`
- node `disabled` runtime/store state

## Current Strengths To Preserve

- TUIC support and Clash TUIC URI conversion.
- Hysteria2 port hopping parsing and normalization.
- HTTP/HTTPS and SOCKS upstream URI support.
- GeoIP standalone router on a separate port with `sg` region support.
- GeoIP DNS cache, VMess host extraction, Proxy-Authorization, and transport reuse.
- Concurrent startup health check.
- Manual blacklist API and pool shared blacklist state.
- Log rotation plus WebUI log buffer.
- File-lock based config and nodes persistence.
- Subscription configuration API and `nodes.txt` compatibility.
- sing-box outbound validation retry that removes invalid outbounds and related empty pools.

## V2 Features To Merge Selectively

- React/Vite/Tailwind/DaisyUI dashboard.
- Optional SQLite store under `internal/store`.
- Session persistence in store.
- Node statistics and traffic persistence.
- Configurable inbound protocol for pool and multi-port listeners.
- Batch node enable/disable and delete APIs.
- Import endpoint.
- Health check interval config.
- Store-backed subscription status.

## V2 Implementations Not Safe To Drop In

- `internal/builder`: older than current protocol support and lacks current Hysteria2/TUIC handling.
- `internal/geoip`: lacks current router separation, `sg`, cache and auth improvements.
- `internal/outbound/pool`: lacks current manual blacklist integration and startup probing changes.
- `cmd/easy_proxies/main.go`: lacks current startup retry, log rotation, and WebUI log capture.
- Dockerfile and Compose files: different paths and omit current logs/nodes compatibility assumptions.

## Branch Plan

Development continues from `dev`; every implementation phase uses a fresh branch:

1. `feat/v2-inbound-protocol`
2. `feat/v2-sqlite-store`
3. `feat/v2-node-persistence`
4. `feat/v2-batch-node-api`
5. `feat/v2-traffic-stats`
6. `feat/v2-react-webui`
7. `docs/v2-migration-guide`
8. `chore/v2-release-readiness`

## Verification

Baseline guard:

```bash
go test ./...
```

Result: passed.
