# Repository Guidelines

## Project Structure & Module Organization

This is a Go proxy pool manager built around sing-box. The application entry point is `cmd/easy_proxies/main.go`. Core packages live under `internal/`: `app` wires the runtime, `config` loads and persists YAML settings, `builder` creates sing-box configuration, `outbound/pool` handles scheduling and node state, `geoip` provides region routing, `subscription` refreshes node sources, and `monitor` serves the WebUI/API. Static dashboard assets are in `internal/monitor/assets/`. Documentation and proposals live in `docs/`. Runtime examples are `config.example.yaml` and `nodes.example`; local `config.yaml`, `nodes.txt`, logs, binaries, and GeoIP databases are intentionally ignored.

## Build, Test, and Development Commands

- `go test ./...` runs all unit tests.
- `go test -cover ./...` checks package-level coverage when changing shared logic.
- `go run ./cmd/easy_proxies --config config.yaml` starts the app from source.
- `go build -tags "with_utls with_quic with_grpc with_wireguard with_gvisor with_clash_api" -o easy_proxies ./cmd/easy_proxies` matches the Docker build feature tags.
- `./start.sh` prepares bind-mounted config files, then pulls and starts the default Compose service.
- `docker compose -f docker-compose.dev.yml up --build` builds and runs the local development image with mapped ports.

## Coding Style & Naming Conventions

Use Go 1.24.x as declared in `go.mod`. Format Go code with `gofmt` before committing; keep imports grouped by standard library, third-party packages, then local `easy_proxies/...` packages. Package names should be short, lowercase, and aligned with the directory name. Prefer explicit errors with context, `context.Context` for long-running work, and YAML tags that match existing config keys. Keep JavaScript/CSS inside `internal/monitor/assets/index.html` minimal and self-contained unless a frontend build step is introduced.

## Testing Guidelines

Tests use the standard Go `testing` package and live beside the package under test as `*_test.go`, for example `internal/config/config_hysteria2_test.go`. Name tests `TestFeatureScenario` and use table tests for parser, builder, and config cases. Add tests when changing config parsing, node URI handling, scheduling, blacklist behavior, or generated sing-box output.

## Commit & Pull Request Guidelines

Recent history uses conventional-style subjects such as `fix: export only available nodes`. Use a concise imperative subject with a type prefix (`fix:`, `feat:`, `docs:`, `test:`). Pull requests should describe the behavior change, list validation commands run, link related issues, and include screenshots or API examples for WebUI/API changes.

## Security & Configuration Tips

Do not commit real `config.yaml`, `nodes.txt`, subscription URLs, credentials, logs, `.mmdb` databases, or generated binaries. Update `config.example.yaml` and `nodes.example` when changing user-facing configuration.
