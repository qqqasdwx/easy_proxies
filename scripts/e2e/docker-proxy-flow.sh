#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

IMAGE_NAME="${IMAGE_NAME:-easy_proxies:e2e}"
SOCKS_IMAGE="${SOCKS_IMAGE:-serjs/go-socks5-proxy:latest}"
BUILD_IMAGE="${BUILD_IMAGE:-1}"

E2E_MANAGEMENT_PORT="${E2E_MANAGEMENT_PORT:-19091}"
E2E_TARGET_PORT="${E2E_TARGET_PORT:-18080}"
E2E_POOL_PORT="${E2E_POOL_PORT:-23230}"
E2E_MULTI_BASE_PORT="${E2E_MULTI_BASE_PORT:-24030}"
E2E_HYBRID_POOL_PORT="${E2E_HYBRID_POOL_PORT:-23232}"
E2E_HYBRID_MULTI_BASE_PORT="${E2E_HYBRID_MULTI_BASE_PORT:-24040}"

SOCKS_PORT=1080
RUN_ID="${E2E_RUN_ID:-$(date +%s)-$$}"
EASY_CONTAINER="ep_e2e_easy_${RUN_ID}"
SOCKS_CONTAINER="ep_e2e_socks_${RUN_ID}"
TMP_ROOT="$(mktemp -d /tmp/easy-proxies-e2e.XXXXXX)"
TARGET_PID=""

log() {
  printf '[e2e] %s\n' "$*"
}

cleanup() {
  if [[ -n "${TARGET_PID}" ]]; then
    kill "${TARGET_PID}" >/dev/null 2>&1 || true
  fi
  docker rm -f "${EASY_CONTAINER}" "${SOCKS_CONTAINER}" >/dev/null 2>&1 || true
  rm -rf "${TMP_ROOT}"
}

on_error() {
  local code=$?
  log "failed at line ${BASH_LINENO[0]} with exit code ${code}"
  docker ps -a --filter "name=ep_e2e_" --format '{{.Names}} {{.Status}} {{.Image}}' || true
  docker logs --tail=120 "${EASY_CONTAINER}" 2>/dev/null || true
  docker logs --tail=80 "${SOCKS_CONTAINER}" 2>/dev/null || true
  cleanup
  exit "${code}"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'missing required command: %s\n' "$1" >&2
    exit 1
  fi
}

assert_port_free() {
  local port="$1"
  if ss -ltn | awk '{print $4}' | grep -Eq ":${port}$"; then
    printf 'port %s is already in use; override E2E_* ports or stop the listener first\n' "${port}" >&2
    exit 1
  fi
}

wait_http() {
  local url="$1"
  local attempts="${2:-60}"
  for _ in $(seq 1 "${attempts}"); do
    if curl -fsS "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  curl -fsS "${url}" >/dev/null
}

api_get() {
  curl -fsS "http://127.0.0.1:${E2E_MANAGEMENT_PORT}$1"
}

api_post() {
  curl -fsS -X POST "http://127.0.0.1:${E2E_MANAGEMENT_PORT}$1"
}

api_put_json() {
  curl -fsS -X PUT "http://127.0.0.1:${E2E_MANAGEMENT_PORT}$1" \
    -H 'Content-Type: application/json' \
    -d @-
}

wait_proxy() {
  local port="$1"
  for _ in $(seq 1 30); do
    if curl --noproxy "" -fsS --max-time 8 \
      -x "http://127.0.0.1:${port}" \
      "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" | grep -q 'E2E OK'; then
      return 0
    fi
    sleep 0.5
  done
  curl --noproxy "" -fsS --max-time 8 \
    -x "http://127.0.0.1:${port}" \
    "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" | grep -q 'E2E OK'
}

apply_settings() {
  local mode="$1"
  local listener_port="$2"
  local multi_base="$3"

  api_get /api/settings \
    | jq \
      --arg mode "${mode}" \
      --argjson listener_port "${listener_port}" \
      --argjson multi_base "${multi_base}" \
      '.mode=$mode
       | .listener.address="127.0.0.1"
       | .listener.port=$listener_port
       | .listener.protocol="mixed"
       | .multi_port.address="127.0.0.1"
       | .multi_port.base_port=$multi_base
       | .multi_port.protocol="mixed"
       | .geoip.enabled=false' \
    | api_put_json /api/settings >/dev/null

  api_post /api/reload >/dev/null
}

trap on_error ERR
trap cleanup EXIT

require_cmd docker
require_cmd curl
require_cmd jq
require_cmd python3
require_cmd ss

assert_port_free "${SOCKS_PORT}"
assert_port_free "${E2E_TARGET_PORT}"
assert_port_free "${E2E_MANAGEMENT_PORT}"
assert_port_free "${E2E_POOL_PORT}"
assert_port_free "${E2E_HYBRID_POOL_PORT}"
assert_port_free "${E2E_MULTI_BASE_PORT}"
assert_port_free "${E2E_HYBRID_MULTI_BASE_PORT}"

if [[ "${BUILD_IMAGE}" != "0" ]]; then
  log "building ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" "${ROOT_DIR}"
fi

mkdir -p "${TMP_ROOT}/data" "${TMP_ROOT}/logs"

log "starting local HTTP target on 127.0.0.1:${E2E_TARGET_PORT}"
python3 - "${E2E_TARGET_PORT}" "${SOCKS_PORT}" <<'PY' &
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

target_port = int(sys.argv[1])
socks_port = int(sys.argv[2])

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/sub.txt"):
            body = f"socks5://127.0.0.1:{socks_port}#sub-socks\n".encode()
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        body = b"E2E OK\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass

ThreadingHTTPServer(("127.0.0.1", target_port), Handler).serve_forever()
PY
TARGET_PID=$!
wait_http "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" 30
curl -fsS "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" | grep -q 'E2E OK'

log "starting SOCKS upstream on host port ${SOCKS_PORT}"
docker run -d --name "${SOCKS_CONTAINER}" --network host \
  -e REQUIRE_AUTH=false \
  "${SOCKS_IMAGE}" >/dev/null
for _ in $(seq 1 40); do
  if curl --noproxy "" --socks5-hostname "127.0.0.1:${SOCKS_PORT}" \
    -fsS "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
curl --noproxy "" --socks5-hostname "127.0.0.1:${SOCKS_PORT}" \
  -fsS "http://127.0.0.1:${E2E_TARGET_PORT}/e2e" | grep -q 'E2E OK'

log "starting Easy Proxies on management port ${E2E_MANAGEMENT_PORT}"
docker run -d --name "${EASY_CONTAINER}" --network host \
  -e MANAGEMENT_PORT="${E2E_MANAGEMENT_PORT}" \
  -v "${TMP_ROOT}/data:/app/data" \
  -v "${TMP_ROOT}/logs:/app/logs" \
  "${IMAGE_NAME}" >/dev/null
wait_http "http://127.0.0.1:${E2E_MANAGEMENT_PORT}/api/settings" 60

api_get /api/settings \
  | jq -e '.management | has("probe_target") and (has("enabled")|not) and (has("listen")|not)' >/dev/null
test -f "${TMP_ROOT}/data/data.db"
test ! -e "${TMP_ROOT}/data/config.yaml"
test ! -e "${TMP_ROOT}/data/nodes.txt"

log "adding manual SOCKS node"
curl -fsS -X POST "http://127.0.0.1:${E2E_MANAGEMENT_PORT}/api/nodes/config" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"manual-socks\",\"uri\":\"socks5://127.0.0.1:${SOCKS_PORT}#manual-socks\",\"inbound_protocol\":\"mixed\"}" \
  | jq -e '.node.name == "manual-socks"' >/dev/null

log "testing pool mode"
apply_settings pool "${E2E_POOL_PORT}" "${E2E_MULTI_BASE_PORT}"
wait_proxy "${E2E_POOL_PORT}"

log "testing multi-port mode"
apply_settings multi-port "$((E2E_POOL_PORT + 1))" "${E2E_MULTI_BASE_PORT}"
MANUAL_MULTI_PORT="$(api_get /api/nodes/config | jq -r '.nodes[] | select(.name=="manual-socks") | .port')"
test -n "${MANUAL_MULTI_PORT}"
test "${MANUAL_MULTI_PORT}" != "0"
test "${MANUAL_MULTI_PORT}" != "null"
wait_proxy "${MANUAL_MULTI_PORT}"

log "testing hybrid mode"
apply_settings hybrid "${E2E_HYBRID_POOL_PORT}" "${E2E_HYBRID_MULTI_BASE_PORT}"
wait_proxy "${E2E_HYBRID_POOL_PORT}"
MANUAL_HYBRID_PORT="$(api_get /api/nodes/config | jq -r '.nodes[] | select(.name=="manual-socks") | .port')"
test -n "${MANUAL_HYBRID_PORT}"
test "${MANUAL_HYBRID_PORT}" != "0"
test "${MANUAL_HYBRID_PORT}" != "null"
wait_proxy "${MANUAL_HYBRID_PORT}"

log "adding and refreshing subscription"
SUB_ID="$(curl -fsS -X POST "http://127.0.0.1:${E2E_MANAGEMENT_PORT}/api/subscriptions" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"local-sub\",\"url\":\"http://127.0.0.1:${E2E_TARGET_PORT}/sub.txt\",\"enabled\":true,\"auto_update\":false,\"interval\":\"24h\"}" \
  | jq -r '.subscription.id')"
test -n "${SUB_ID}"
test "${SUB_ID}" != "null"
api_post "/api/subscriptions/${SUB_ID}/refresh" | jq -e '.message' >/dev/null

api_get /api/nodes/config \
  | jq -e '[.nodes[] | .source] | (index("manual") != null and index("subscription") != null)' >/dev/null
api_get /api/settings \
  | jq -e --argjson port "${E2E_HYBRID_POOL_PORT}" --argjson base "${E2E_HYBRID_MULTI_BASE_PORT}" \
    '.mode == "hybrid" and .listener.port == $port and .multi_port.base_port == $base' >/dev/null
wait_proxy "${E2E_HYBRID_POOL_PORT}"

SUB_PORT="$(api_get /api/nodes/config | jq -r '.nodes[] | select(.name=="sub-socks") | .port')"
test -n "${SUB_PORT}"
test "${SUB_PORT}" != "0"
test "${SUB_PORT}" != "null"
wait_proxy "${SUB_PORT}"

log "restarting container and checking persistence"
docker restart "${EASY_CONTAINER}" >/dev/null
wait_http "http://127.0.0.1:${E2E_MANAGEMENT_PORT}/api/settings" 60
api_get /api/settings \
  | jq -e --argjson port "${E2E_HYBRID_POOL_PORT}" --argjson base "${E2E_HYBRID_MULTI_BASE_PORT}" \
    '.mode == "hybrid" and .listener.port == $port and .multi_port.base_port == $base' >/dev/null
api_get /api/nodes/config \
  | jq -e '[.nodes[] | .source] | (index("manual") != null and index("subscription") != null)' >/dev/null
wait_proxy "${E2E_HYBRID_POOL_PORT}"

log "passed: pool=${E2E_POOL_PORT} multi=${MANUAL_MULTI_PORT} hybrid_pool=${E2E_HYBRID_POOL_PORT} subscription_port=${SUB_PORT}"
