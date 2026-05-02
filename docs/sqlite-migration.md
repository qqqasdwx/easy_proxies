# SQLite Store Migration Guide

Easy Proxies uses a SQLite store at `database_path` (default `data/data.db`) to persist WebUI-managed nodes, disabled flags, sessions, and runtime statistics. Existing `config.yaml` and `nodes.txt` workflows continue to work as legacy file mode.

## What Changes

- A new deployment can start without `config.yaml` or `nodes.txt`.
- With no nodes configured, only the management WebUI/API listener starts.
- `nodes.txt` remains supported only when `nodes_file` is explicitly configured.
- SQLite restores WebUI nodes on startup and preserves disabled nodes and traffic totals.
- If the database cannot be opened, the app logs a warning and continues in file-compatible mode.

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

## Upgrade From File Mode

1. Add or keep this setting in `config.yaml`:

   ```yaml
   database_path: data/data.db
   ```

2. Start normally:

   ```bash
   docker compose up -d
   ```

3. On first startup, nodes from `config.yaml` / `nodes.txt` are upserted into SQLite. Existing node URIs are used as the stable identity, so repeated starts do not duplicate nodes.

4. Use the WebUI node management page for add/edit/disable/delete. After those actions, reload from the WebUI when prompted.

## Management Password

Set `MANAGEMENT_PASSWORD` to require WebUI/API login. The password is read only from the process environment and is never written to `config.yaml` or returned by the settings API.

## Rollback

Stop the service and remove or change `database_path` if you need pure file mode. Keep `config.yaml` and `nodes.txt` if you still use them; do not delete `data/` unless you intentionally want to discard WebUI state and runtime statistics.

## Notes

- Do not commit `data/`, `*.db`, `*.db-shm`, or `*.db-wal`.
- Back up `data/` before major upgrades; include `config.yaml` and `nodes.txt` only if you still use legacy file mode.
- Disabled nodes are stored in SQLite and are not selected during sing-box config generation.
