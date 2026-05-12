# SQLite Runtime Store

Easy Proxies uses SQLite as the only persistence source. The default local database is `data/data.db`; the Docker image stores it at `/app/data/data.db`. The app no longer reads, writes, or migrates `config.yaml` or `nodes.txt`.

## What Is Persisted

- Runtime settings: proxy pools, multi-port defaults, log, DNS, GeoIP, subscription refresh, and probe target.
- Nodes: manual nodes and subscription nodes, including source, outbound JSON, inbound protocol, local port, disabled state, and subscription ownership.
- Subscriptions: name, URL, enabled flag, auto-update flag, interval, refresh timestamps, node count, and last error.
- Sessions and runtime data: login sessions, traffic counters, node stats, blacklist state, and timeline data.

## Docker Volumes

Keep the mutable data directories mounted:

```yaml
volumes:
  - ./data:/app/data
  - ./logs:/app/logs
```

Create them before first start:

```bash
mkdir -p logs data
```

## Local Development

Use the default database path:

```bash
go run ./cmd/easy_proxies --database data/data.db
```

Use `--database /path/to/data.db` only when you intentionally want a separate runtime store.

## Management Password

Set `MANAGEMENT_PASSWORD` to require WebUI/API login. The password is read only from the process environment and is never written to SQLite, shown in the WebUI, logged, or returned by the settings API.

`MANAGEMENT_PORT` overrides the management WebUI/API port at process start. It is an environment override, not a persisted setting.

## Backup and Reset

Back up `data/` before upgrades. To reset a disposable development instance, stop the service and remove `data/data.db`; the next start will recreate it from built-in defaults.

## Notes

- Do not commit `data/`, `*.db`, `*.db-shm`, or `*.db-wal`.
- Disabled nodes are stored in SQLite and are not selected during sing-box config generation.
- Subscription nodes are read-only in node management; update them by refreshing or editing their subscription source.
