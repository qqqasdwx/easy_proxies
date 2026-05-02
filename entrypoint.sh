#!/bin/sh

set -eu

: "${MANAGEMENT_PORT:=9091}"
export MANAGEMENT_PORT

mkdir -p /app/data /app/logs
chown -R easy:easy /etc/easy_proxies /app 2>/dev/null || true

exec gosu easy /usr/local/bin/easy_proxies "$@"
