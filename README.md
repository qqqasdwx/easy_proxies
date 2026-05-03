# Easy Proxies

[简体中文](README_ZH.md)

> A sing-box based proxy pool manager -- aggregate many upstream proxy nodes into one stable, health-checked, load-balanced local proxy endpoint.

## Features

- **Three runtime modes**: `pool` (single-port load balancing), `multi-port` (one port per node), and `hybrid` (both simultaneously)
- **Wide protocol support**: VLESS, VMess, Trojan, Shadowsocks, Hysteria2, TUIC, AnyTLS, SOCKS5, HTTP/HTTPS
- **Automatic health checking** with configurable failure thresholds and blacklist duration, plus manual blacklist/release from the dashboard
- **GeoIP region routing**: classify nodes by country and route traffic through a specific region via a dedicated HTTP proxy endpoint
- **Database-backed node sources**: manual WebUI/API nodes and multiple subscription sources (Base64, plain text, Clash YAML) coexist in SQLite
- **Subscription auto-refresh with hot-reload**: periodically fetches subscription updates and reloads without restart
- **WebUI dashboard**: real-time node status, traffic charts, diagnostics, log console, and full settings management
- **Management API**: RESTful endpoints for node CRUD, probing, blacklisting, subscription management, and config reload
- **Configurable DNS resolver** with fallback servers and IPv4/IPv6 strategy control
- **Log rotation**: size-based rotation with configurable backup count, age, and compression
- **Multi-platform Docker**: supports amd64 and arm64 with host networking

## Quick Start

### 1. Start the Management UI

```bash
mkdir -p logs data
docker compose up -d
```

The default container starts without `config.yaml` or `nodes.txt` and listens only on the management WebUI/API port.

```bash
MANAGEMENT_PORT=19091 MANAGEMENT_PASSWORD='change-me' docker compose up -d
```

### 2. Run from Source

```bash
go run ./cmd/easy_proxies --database data/data.db
```

### 3. Access WebUI

Open `http://localhost:9091` in your browser.

## Configuration

### Runtime Modes

| Mode | Description |
|------|-------------|
| `pool` | Single port proxy pool. All nodes share one port with load balancing |
| `multi-port` | One local port per node for direct access |
| `hybrid` | Both pool + multi-port simultaneously |

### Pool Scheduling

| Algorithm | Description |
|-----------|-------------|
| `sequential` | Round-robin through healthy nodes |
| `random` | Random node selection |
| `balance` | Least-connections balancing |

### Inbound Protocol

`listener.protocol` controls the pool entrypoint and `multi_port.protocol` controls per-node entrypoints. Supported values are `mixed` (default, HTTP + SOCKS5), `http`, and `socks5`.

### Runtime State

Easy Proxies no longer reads or writes `config.yaml` or `nodes.txt`. Built-in defaults are loaded first, then runtime settings, nodes, subscriptions, sessions, disabled flags, ports, and traffic statistics are restored from SQLite.

Use `--database data/data.db` for local development. In Docker, mount `./data:/app/data` to persist `/app/data/data.db` across restarts.

`MANAGEMENT_PORT` overrides the management WebUI/API port at process start. `MANAGEMENT_PASSWORD` enables login and is read only from the environment; it is not a database or WebUI setting.

See [SQLite Runtime Store](docs/sqlite-migration.md) for persistence and backup notes.

## GeoIP Region Routing

### Overview

When GeoIP is enabled, Easy Proxies automatically classifies your proxy nodes by geographic region and provides a separate HTTP proxy endpoint that lets you route traffic through nodes in a specific country/region.

### Supported Regions

| Code | Region |
|------|--------|
| `jp` | Japan 🇯🇵 |
| `kr` | South Korea 🇰🇷 |
| `us` | United States 🇺🇸 |
| `hk` | Hong Kong 🇭🇰 |
| `tw` | Taiwan 🇹🇼 |
| `sg` | Singapore 🇸🇬 |
| `other` | All other regions |

### Configuration

```yaml
geoip:
  enabled: true
  database_path: "./GeoLite2-Country.mmdb"
  listen: "0.0.0.0"          # defaults to listener.address if omitted
  port: 1221                  # defaults to listener.port if omitted
  auto_update_enabled: true   # auto-update the GeoIP database
  auto_update_interval: 24h   # check interval
```

The GeoIP router reuses the `listener.username` and `listener.password` for proxy authentication.

Key behaviors:
- The GeoIP database (MaxMind GeoLite2-Country) is **auto-downloaded** on first startup
- Auto-update is enabled by default (checks every 24h) with hot-reload -- no restart needed
- Node region classification happens automatically during startup and on every reload
- Nodes whose IP cannot be resolved or looked up are placed in the `other` category

### How to Use

The GeoIP router is an HTTP proxy that listens on its own port. You select a region by adding a path prefix to your request.

#### HTTP Requests

Format: `http://<geoip_host>:<geoip_port>/<region>/`

```bash
# Route through Japanese nodes
curl -x http://user:pass@localhost:1221/jp/ http://example.com

# Route through US nodes
curl -x http://user:pass@localhost:1221/us/ http://example.com

# Route through Hong Kong nodes
curl -x http://user:pass@localhost:1221/hk/ http://example.com

# Route through Singapore nodes
curl -x http://user:pass@localhost:1221/sg/ http://example.com

# No region prefix = use global pool (all nodes)
curl -x http://user:pass@localhost:1221/ http://example.com
```

#### HTTPS Requests (CONNECT Tunnel)

For HTTPS, the region prefix goes before the target host in the CONNECT request:

```bash
# Route HTTPS through Japanese nodes
https_proxy=http://user:pass@localhost:1221/jp/ curl https://www.google.com

# Route HTTPS through US nodes
https_proxy=http://user:pass@localhost:1221/us/ curl https://www.google.com

# No region prefix = use global pool
https_proxy=http://user:pass@localhost:1221/ curl https://www.google.com
```

#### Using with Applications

**Environment variables:**

```bash
# Use Japanese nodes for all traffic
export http_proxy=http://user:pass@your-server:1221/jp/
export https_proxy=http://user:pass@your-server:1221/jp/

# Use global pool (all nodes)
export http_proxy=http://user:pass@your-server:1221/
export https_proxy=http://user:pass@your-server:1221/
```

**Browser proxy extensions (SwitchyOmega, FoxyProxy, etc.):**

- Protocol: HTTP
- Server: your-server-ip
- Port: 1221
- Username/Password: as configured in `listener`
- For region-specific routing: set the proxy URL path to include the region prefix (e.g., `/jp/`)

**Python requests:**

```python
import requests

proxies = {
    "http": "http://user:pass@your-server:1221/jp/",
    "https": "http://user:pass@your-server:1221/jp/",
}
r = requests.get("http://example.com", proxies=proxies)
```

**Go net/http:**

```go
proxyURL, _ := url.Parse("http://user:pass@your-server:1221/jp/")
client := &http.Client{
    Transport: &http.Transport{
        Proxy: http.ProxyURL(proxyURL),
    },
}
resp, err := client.Get("http://example.com")
```

### How It Works

1. On startup, each node's server IP is resolved and looked up in the MaxMind GeoLite2-Country database
2. Nodes are grouped into per-region pools (`pool-jp`, `pool-kr`, `pool-us`, etc.) with independent health checking
3. The GeoIP router listens on its own port and inspects the request path for a region prefix
4. Matching requests are routed through the corresponding region pool; unmatched requests use the global pool
5. Each region pool uses the same scheduling algorithm configured in the `pool` section
6. DNS lookup results are cached to avoid repeated resolution on reload

## Supported Protocols

| Protocol | URI Schemes | Transport |
|----------|-------------|-----------|
| VLESS | `vless://` | TCP, WS, HTTP/2, gRPC, HTTPUpgrade; TLS/Reality/uTLS |
| VMess | `vmess://` | WS, HTTP/2, gRPC, HTTPUpgrade; TLS/uTLS |
| Trojan | `trojan://` | WS, HTTP/2, gRPC, HTTPUpgrade; TLS/Reality/uTLS |
| Shadowsocks | `ss://` | Direct; SIP002 format |
| Hysteria2 | `hysteria2://`, `hy2://` | QUIC-based |
| TUIC | `tuic://` | QUIC-based |
| AnyTLS | `anytls://` | TLS |
| SOCKS5 | `socks5://`, `socks://` | Direct |
| HTTP | `http://`, `https://` | Direct |

## Node Sources

### Manual Nodes

Add nodes from the WebUI or `POST /api/nodes/config`. The editor accepts URI import, structured sing-box outbound JSON, and per-node inbound settings. Manual nodes are editable and persist in SQLite.

### Subscriptions

Manage subscriptions from the WebUI subscription page or `/api/subscriptions`. Each subscription has its own enabled flag, auto-update flag, interval, refresh status, and node count. Subscription nodes appear in node management, can be enabled/disabled, but are read-only for edits. Subscription refresh hot-reloads sing-box without restart.

## WebUI Dashboard

Access at `http://your-server:9091` (configurable via the `management` section).

Features:

- **React dashboard**: Real-time node status, traffic charts, region availability, latency monitoring
- **Node Config**: Add/edit/delete/import manual nodes, view read-only subscription nodes, and batch enable/disable
- **Subscriptions**: Manage multiple subscription sources, auto-update, manual refresh, and refresh status
- **Diagnostics**: Connectivity testing and node state export
- **Console**: Application logs from the in-memory ring buffer (last 1000 lines)
- **Settings**: Runtime options are editable from the browser; node state persists in SQLite

Set `MANAGEMENT_PASSWORD` to require login. The management password is never stored in SQLite, logged, or returned by the settings API.

## Management API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/auth` | POST | Login with password |
| `/api/settings` | GET, PUT | Read/update settings |
| `/api/nodes` | GET | List all nodes with status |
| `/api/nodes/{tag}/probe` | POST | Test node connectivity |
| `/api/nodes/{tag}/blacklist` | POST | Manually blacklist a node |
| `/api/nodes/{tag}/release` | POST | Release node from blacklist |
| `/api/nodes/probe-all` | POST | Probe all nodes (SSE stream) |
| `/api/nodes/traffic/stream` | GET | Stream aggregated traffic stats (SSE) |
| `/api/export` | GET | Export node configuration |
| `/api/import` | POST | Import proxy URI lines |
| `/api/subscriptions` | GET, POST | List/create subscription sources |
| `/api/subscriptions/{id}` | PUT, DELETE | Update/delete one subscription source |
| `/api/subscriptions/{id}/refresh` | POST | Refresh one subscription source |
| `/api/subscription/status` | GET | Check aggregate subscription status |
| `/api/nodes/config` | GET, POST | List/create node config |
| `/api/nodes/config/{name}` | PUT, DELETE, PATCH | Update/delete/toggle one node |
| `/api/nodes/config/batch-toggle` | POST | Batch enable/disable nodes |
| `/api/nodes/config/batch-delete` | POST | Batch delete nodes |
| `/api/logs` | GET | Read recent application logs |
| `/api/reload` | POST | Reload sing-box instance |

## Docker Deployment

### docker-compose.yml

The default setup exposes only the management port and persists SQLite/log data:

```yaml
services:
  easy_proxies:
    image: ghcr.io/jasonwong1991/easy_proxies:latest
    container_name: easy_proxies
    restart: unless-stopped
    environment:
      MANAGEMENT_PORT: ${MANAGEMENT_PORT:-9091}
      MANAGEMENT_PASSWORD: ${MANAGEMENT_PASSWORD:-}
    ports:
      - "${MANAGEMENT_PORT:-9091}:${MANAGEMENT_PORT:-9091}"
    volumes:
      - ./data:/app/data
      - ./logs:/app/logs
```

### Important Notes

- **SQLite data**: keep `./data` mounted to preserve WebUI node state, sessions, and traffic totals across restarts.
- **Management password**: set `MANAGEMENT_PASSWORD`; it is not a config item.
- **Multi-platform**: Supports amd64 and arm64 architectures.
- **Reload**: `/api/reload` and subscription refresh will interrupt active connections.

### Ports

| Port | Usage |
|------|-------|
| 2323 | Pool proxy entry (pool/hybrid mode) |
| 9091 | WebUI and Management API |
| 1221 | GeoIP region router (when enabled, configurable) |
| 24000+ | Multi-port mode (one per node) |

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for version history.

## Development

```bash
go test ./...
npm ci --prefix frontend
npm run build --prefix frontend
docker build -t easy_proxies:dev .
```

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=jasonwong1991/easy_proxies&type=Date)](https://star-history.com/#jasonwong1991/easy_proxies&Date)

## License

MIT License
