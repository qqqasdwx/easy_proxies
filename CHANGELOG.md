# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [2.0.0] - 2026-05-07

### Breaking Changes

- Replaced file-based runtime configuration with SQLite-only persistence.
- Removed `config.yaml` and `nodes.txt` reads, writes, and migration behavior.
- Removed management port and password from persisted settings. `MANAGEMENT_PORT` and `MANAGEMENT_PASSWORD` are now process/container environment variables only.
- Changed clean Docker startup to expose only the management WebUI/API until nodes and runtime mode are configured from the WebUI or API.

### Added

- Added database-backed runtime settings for modes, listeners, DNS, GeoIP, health checks, external IP, SSL verification, and sing-box log level.
- Added multiple subscription sources with per-subscription enable, auto-update, interval, refresh status, and node count.
- Added coexistence for manual nodes and subscription nodes in node management. Subscription nodes are visible and can be enabled or disabled, but remain read-only for edits.
- Added a structured node editor for sing-box outbound JSON with URI parsing, form synchronization, and per-node inbound settings.
- Added GeoIP database management from the WebUI, including current database path, last update time, manual refresh, and configurable auto-update interval.
- Added runtime log console support for recent application logs and WebUI control for sing-box log verbosity.
- Added Docker proxy-flow E2E coverage for pool, multi-port, hybrid, subscription refresh, clean SQLite startup, and restart persistence.
- Added Docker and Compose defaults that persist `./data` and `./logs` without requiring config bind mounts.

### Changed

- Subscription refresh now rebuilds sing-box from current runtime settings instead of reverting to startup defaults.
- Settings UI is organized around runtime, network, health, and system tabs with mode-specific listener and multi-port configuration.
- GeoIP database paths are managed by the application under Docker `/app/data` or local `data/`.
- GitHub Actions image publishing uses the current repository name dynamically for GHCR images and keeps `main`, tag, pull request, and manual triggers.
- Frontend package version is now `2.0.0`, matching the WebUI version badge.

### Fixed

- Fixed subscription refresh overwriting WebUI settings changes.
- Fixed management panel disable behavior by keeping WebUI/API availability environment-controlled and always enabled in persisted settings.
- Fixed stale dynamic form fields in the node editor when TLS or transport options are disabled or changed.
- Fixed node editor synchronization gaps between form controls and outbound JSON across supported protocols.

## [1.1.0] - 2024-12-XX

### Added

- GeoIP region routing and dashboard statistics.
- Global `skip_cert_verify` option.
- Node port assignment persistence across reloads.
- ARM64 support for Docker images.

### Fixed

- Hybrid mode export credentials.
- Settings save permission issues.
- Health check timing after node registration.

## [1.0.0] - 2024-11-XX

### Added

- Initial release.
- Pool, multi-port, and hybrid runtime modes.
- Support for vmess, vless, trojan, shadowsocks, hysteria2, socks5, and HTTP protocols.
- Subscription support for Base64, plain text, and Clash YAML sources.
- Web dashboard with node management.
- Automatic health checks and blacklist recovery.
- Configurable DNS resolver.
