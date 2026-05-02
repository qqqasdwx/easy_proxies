# SQLite Store Migration Guide

Easy Proxies now uses an optional SQLite store at `database_path` (default `data/data.db`) to persist WebUI-managed nodes, disabled flags, sessions, and runtime statistics. Existing `config.yaml` and `nodes.txt` workflows continue to work.

## What Changes

- `config.yaml` remains the primary configuration file.
- `nodes.txt` remains supported for file-based node lists and subscription write-back.
- SQLite mirrors nodes on startup and preserves WebUI state such as disabled nodes and traffic totals.
- If the database cannot be opened, the app logs a warning and continues in file-compatible mode.

## Docker Volumes

Keep all mutable files mounted:

```yaml
volumes:
  - ./config.yaml:/etc/easy_proxies/config.yaml
  - ./nodes.txt:/etc/easy_proxies/nodes.txt
  - ./logs:/app/logs
  - ./data:/etc/easy_proxies/data
```

Create them before first start:

```bash
cp config.example.yaml config.yaml
touch nodes.txt
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

## Rollback

Stop the service and remove or change `database_path` if you need pure file mode. Keep `config.yaml` and `nodes.txt`; do not delete `data/` unless you intentionally want to discard WebUI state and runtime statistics.

## Notes

- Do not commit `data/`, `*.db`, `*.db-shm`, or `*.db-wal`.
- Back up `config.yaml`, `nodes.txt`, and `data/` together before major upgrades.
- Disabled nodes are stored in SQLite and are not selected during sing-box config generation.
