# Repository Guidelines

## Project Structure & Module Organization

This is a Go proxy pool manager built around sing-box. The application entry point is `cmd/easy_proxies/main.go`. Core packages live under `internal/`: `app` wires the runtime, `config` defines defaults and store-backed runtime settings, `store` owns SQLite persistence, `builder` creates sing-box configuration, `outbound/pool` handles scheduling and node state, `geoip` provides region routing, `subscription` refreshes node sources, and `monitor` serves the WebUI/API. The React frontend lives in `frontend/src`; built assets are embedded under `internal/monitor/assets/`. Documentation and implementation plans live in `docs/`. Runtime data is stored in `data/data.db`; logs and generated frontend assets are build outputs.

## Build, Test, and Development Commands

- `scripts/test-go.sh` runs all Go unit tests under `cmd` and `internal` without descending into `frontend/node_modules`.
- `scripts/test-go.sh -cover` checks package-level coverage when changing shared logic.
- `go run ./cmd/easy_proxies --database data/data.db` starts the app from source with the local SQLite store.
- `npm --prefix frontend run lint` checks the React/TypeScript frontend.
- `npm --prefix frontend run build` builds the frontend and refreshes embedded monitor assets.
- `go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies ./cmd/easy_proxies` matches the Docker build feature tags.
- `docker compose up -d` starts the default container with `./data` and `./logs` mounted.
- `docker compose -f docker-compose.dev.yml up --build` builds and runs the local development image.

## Coding Style & Naming Conventions

Use Go 1.24.x as declared in `go.mod`. Format Go code with `gofmt`; keep imports grouped by standard library, third-party packages, then local `easy_proxies/...` packages. Package names should be short, lowercase, and aligned with the directory name. Prefer explicit errors with context and `context.Context` for long-running work. Frontend code uses React and TypeScript in `frontend/src`; keep component state synchronized with API types in `frontend/src/types`.

## Testing Guidelines

Tests use the standard Go `testing` package and live beside the package under test as `*_test.go`, for example `internal/config/config_hysteria2_test.go`. Name tests `TestFeatureScenario` and use table tests for parser, builder, store, and config cases. Add tests when changing SQLite persistence, node URI/JSON handling, scheduling, subscription refresh, blacklist behavior, or generated sing-box output.

## Commit & Pull Request Guidelines

Recent history uses conventional-style subjects such as `fix: export only available nodes`. Use a concise imperative subject with a type prefix (`fix:`, `feat:`, `docs:`, `test:`). Pull requests should describe the behavior change, list validation commands run, link related issues, and include screenshots or API examples for WebUI/API changes.

## Security & Configuration Tips

Do not commit real SQLite databases, subscription URLs, credentials, logs, `.mmdb` databases, or generated binaries. `MANAGEMENT_PORT` may override the management port at runtime. `MANAGEMENT_PASSWORD` is read only from the container/process environment and must never be stored in SQLite, returned by APIs, logged, or shown as a WebUI setting.
