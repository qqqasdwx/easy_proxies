#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

IMAGE_NAME="${IMAGE_NAME:-easy_proxies:e2e}"
BUILD_IMAGE="${BUILD_IMAGE:-1}"
E2E_UI_SMOKE="${E2E_UI_SMOKE:-1}"

E2E_MANAGEMENT_PORT="${E2E_MANAGEMENT_PORT:-19091}"
E2E_TARGET_PORT="${E2E_TARGET_PORT:-18080}"
E2E_SOCKS_A_PORT="${E2E_SOCKS_A_PORT:-18101}"
E2E_SOCKS_B_PORT="${E2E_SOCKS_B_PORT:-18102}"
E2E_SOCKS_C_PORT="${E2E_SOCKS_C_PORT:-18103}"
E2E_POOL_ALL_PORT="${E2E_POOL_ALL_PORT:-23230}"
E2E_POOL_ALPHA_PORT="${E2E_POOL_ALPHA_PORT:-23231}"
E2E_POOL_BETA_PORT="${E2E_POOL_BETA_PORT:-23232}"
E2E_POOL_RANDOM_PORT="${E2E_POOL_RANDOM_PORT:-23233}"
E2E_POOL_BALANCE_PORT="${E2E_POOL_BALANCE_PORT:-23234}"
E2E_NODE_A_PORT="${E2E_NODE_A_PORT:-24031}"
E2E_NODE_B_PORT="${E2E_NODE_B_PORT:-24032}"
E2E_NODE_C_PORT="${E2E_NODE_C_PORT:-24033}"

RUN_ID="${E2E_RUN_ID:-$(date +%s)-$$}"
EASY_CONTAINER="ep_e2e_easy_${RUN_ID}"
TMP_ROOT="$(mktemp -d /tmp/easy-proxies-pools-e2e.XXXXXX)"
FIXTURE_LOG="${TMP_ROOT}/fixture.jsonl"
FIXTURE_STDOUT="${TMP_ROOT}/fixture.log"
FIXTURE_PID=""

log() {
  printf '[e2e] %s\n' "$*"
}

cleanup() {
  if [[ -n "${FIXTURE_PID}" ]]; then
    kill "${FIXTURE_PID}" >/dev/null 2>&1 || true
  fi
  docker rm -f "${EASY_CONTAINER}" >/dev/null 2>&1 || true
  rm -rf "${TMP_ROOT}"
}

on_error() {
  local code=$?
  log "failed at line ${BASH_LINENO[0]} with exit code ${code}"
  log "fixture events:"
  tail -n 80 "${FIXTURE_LOG}" 2>/dev/null || true
  log "fixture output:"
  tail -n 80 "${FIXTURE_STDOUT}" 2>/dev/null || true
  log "easy proxies logs:"
  docker logs --tail=160 "${EASY_CONTAINER}" 2>/dev/null || true
  cleanup
  exit "${code}"
}

trap on_error ERR
trap cleanup EXIT

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

api_post_json() {
  curl -fsS -X POST "http://127.0.0.1:${E2E_MANAGEMENT_PORT}$1" \
    -H 'Content-Type: application/json' \
    -d @-
}

api_put_json() {
  curl -fsS -X PUT "http://127.0.0.1:${E2E_MANAGEMENT_PORT}$1" \
    -H 'Content-Type: application/json' \
    -d @-
}

wait_listener() {
  local port="$1"
  for _ in $(seq 1 40); do
    if ss -ltn | awk '{print $4}' | grep -Eq ":${port}$"; then
      return 0
    fi
    sleep 0.25
  done
  ss -ltn | awk '{print $4}' | grep -Eq ":${port}$"
}

proxy_get() {
  local port="$1"
  local path="$2"
  curl --noproxy "" -fsS --max-time 12 \
    -x "http://127.0.0.1:${port}" \
    "http://127.0.0.1:${E2E_TARGET_PORT}${path}" \
    | grep -F "E2E OK ${path}" >/dev/null
}

event_nodes_for_path() {
  local path="$1"
  jq -r --arg path "${path}" \
    'select(.kind == "socks_http_request" and .path == $path) | .node' \
    "${FIXTURE_LOG}" 2>/dev/null || true
}

target_seen_count() {
  local path="$1"
  jq -r --arg path "${path}" \
    'select(.kind == "target_request" and .path == $path) | .path' \
    "${FIXTURE_LOG}" 2>/dev/null | wc -l
}

assert_route() {
  local path="$1"
  local expected="$2"
  local actual=""
  for _ in $(seq 1 30); do
    actual="$(event_nodes_for_path "${path}" | sort -u | paste -sd ',' -)"
    if [[ -n "${actual}" ]]; then
      break
    fi
    sleep 0.1
  done
  if [[ "${actual}" != "${expected}" ]]; then
    printf 'route mismatch for %s: expected %s, got %s\n' "${path}" "${expected}" "${actual:-<none>}" >&2
    return 1
  fi
  if [[ "$(target_seen_count "${path}")" -lt 1 ]]; then
    printf 'target did not receive %s\n' "${path}" >&2
    return 1
  fi
  log "route ${path} -> ${actual}"
}

create_node() {
  local name="$1"
  local socks_port="$2"
  local listen_port="$3"
  jq -n \
    --arg name "${name}" \
    --arg uri "socks5://127.0.0.1:${socks_port}#${name}" \
    --arg protocol "mixed" \
    --argjson port "${listen_port}" \
    '{name:$name, uri:$uri, inbound_protocol:$protocol, port:$port, username:"", password:""}' \
    | api_post_json /api/nodes/config \
    | jq -er '.node.id'
}

pool_payload() {
  local name="$1"
  local port="$2"
  local mode="$3"
  local all_nodes="$4"
  local node_ids="${5:-[]}"
  jq -n \
    --arg name "${name}" \
    --arg address "127.0.0.1" \
    --arg protocol "mixed" \
    --arg mode "${mode}" \
    --argjson enabled true \
    --argjson port "${port}" \
    --argjson all_nodes "${all_nodes}" \
    --argjson node_ids "${node_ids}" \
    '{
      name:$name,
      enabled:$enabled,
      listen_address:$address,
      listen_port:$port,
      protocol:$protocol,
      username:"",
      password:"",
      mode:$mode,
      failure_threshold:3,
      blacklist_duration:"24h0m0s",
      all_nodes:$all_nodes,
      node_ids:$node_ids
    }'
}

update_pool() {
  local id="$1"
  shift
  pool_payload "$@" | api_put_json "/api/proxy-pools/${id}" >/dev/null
}

create_pool() {
  pool_payload "$@" | api_post_json /api/proxy-pools | jq -er '.proxy_pool.id'
}

run_ui_smoke() {
  if [[ "${E2E_UI_SMOKE}" != "1" ]]; then
    return 0
  fi
  log "running Playwright WebUI click smoke"
  local playwright_dir="${TMP_ROOT}/playwright"
  mkdir -p "${playwright_dir}"
  (
    cd "${playwright_dir}"
    npm init -y >/dev/null
    npm install --no-audit --no-fund playwright@1.56.1 >/dev/null
    npx playwright install --with-deps chromium >/dev/null
  )
  cat >"${TMP_ROOT}/ui-smoke.cjs" <<'JS'
const { chromium } = require('playwright')

const baseURL = process.env.BASE_URL
const screenshotPath = process.env.SCREENSHOT_PATH

;(async () => {
  const browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 1365, height: 900 } })
  await page.goto(baseURL, { waitUntil: 'domcontentloaded' })
  await page.getByRole('button', { name: /代理池管理/ }).click()
  await page.waitForURL(/#proxy-pools/)
  await page.getByText('pool-all').waitFor({ timeout: 10000 })
  await page.getByRole('button', { name: /pool-alpha/ }).click()
  await page.getByText('node-a').waitFor({ timeout: 10000 })
  await page.getByRole('button', { name: /添加代理池/ }).click()
  await page.locator('input[value*="代理池"]').first().waitFor({ timeout: 10000 })
  await page.getByRole('button', { name: /节点管理/ }).click()
  await page.waitForURL(/#manage/)
  await page.getByText('node-a').waitFor({ timeout: 10000 })
  await page.screenshot({ path: screenshotPath, fullPage: true })
  await browser.close()
})().catch(async err => {
  console.error(err)
  process.exit(1)
})
JS
  BASE_URL="http://127.0.0.1:${E2E_MANAGEMENT_PORT}/" \
  SCREENSHOT_PATH="${TMP_ROOT}/webui-smoke.png" \
  NODE_PATH="${playwright_dir}/node_modules" \
    node "${TMP_ROOT}/ui-smoke.cjs"
}

require_cmd docker
require_cmd curl
require_cmd go
require_cmd jq
require_cmd node
require_cmd npx
require_cmd ss

for port in \
  "${E2E_MANAGEMENT_PORT}" "${E2E_TARGET_PORT}" \
  "${E2E_SOCKS_A_PORT}" "${E2E_SOCKS_B_PORT}" "${E2E_SOCKS_C_PORT}" \
  "${E2E_POOL_ALL_PORT}" "${E2E_POOL_ALPHA_PORT}" "${E2E_POOL_BETA_PORT}" \
  "${E2E_POOL_RANDOM_PORT}" "${E2E_POOL_BALANCE_PORT}" \
  "${E2E_NODE_A_PORT}" "${E2E_NODE_B_PORT}" "${E2E_NODE_C_PORT}"; do
  assert_port_free "${port}"
done

if [[ "${BUILD_IMAGE}" != "0" ]]; then
  log "building ${IMAGE_NAME}"
  docker build -t "${IMAGE_NAME}" "${ROOT_DIR}"
fi

log "starting fixture target and SOCKS upstreams"
go build -o "${TMP_ROOT}/proxy_fixture" "${ROOT_DIR}/scripts/e2e/fixtures"
"${TMP_ROOT}/proxy_fixture" \
  --target-port "${E2E_TARGET_PORT}" \
  --log-file "${FIXTURE_LOG}" \
  --socks "node-a:${E2E_SOCKS_A_PORT}" \
  --socks "node-b:${E2E_SOCKS_B_PORT}" \
  --socks "node-c:${E2E_SOCKS_C_PORT}" \
  >"${FIXTURE_STDOUT}" 2>&1 &
FIXTURE_PID=$!
wait_http "http://127.0.0.1:${E2E_TARGET_PORT}/ready" 40
for port in "${E2E_SOCKS_A_PORT}" "${E2E_SOCKS_B_PORT}" "${E2E_SOCKS_C_PORT}"; do
  curl --noproxy "" --socks5-hostname "127.0.0.1:${port}" \
    -fsS "http://127.0.0.1:${E2E_TARGET_PORT}/socks-ready" >/dev/null
done

mkdir -p "${TMP_ROOT}/data" "${TMP_ROOT}/logs"
log "starting Easy Proxies on management port ${E2E_MANAGEMENT_PORT}"
docker run -d --name "${EASY_CONTAINER}" --network host \
  -e MANAGEMENT_PORT="${E2E_MANAGEMENT_PORT}" \
  -e MANAGEMENT_PASSWORD="" \
  -v "${TMP_ROOT}/data:/app/data" \
  -v "${TMP_ROOT}/logs:/app/logs" \
  "${IMAGE_NAME}" >/dev/null
wait_http "http://127.0.0.1:${E2E_MANAGEMENT_PORT}/api/settings" 80

log "configuring local probe target"
jq -n --arg probe "http://127.0.0.1:${E2E_TARGET_PORT}/generate_204" \
  '{external_ip:"127.0.0.1", probe_target:$probe, skip_cert_verify:false}' \
  | api_put_json /api/settings >/dev/null

log "adding three SOCKS nodes with independent listeners"
NODE_A_ID="$(create_node node-a "${E2E_SOCKS_A_PORT}" "${E2E_NODE_A_PORT}")"
NODE_B_ID="$(create_node node-b "${E2E_SOCKS_B_PORT}" "${E2E_NODE_B_PORT}")"
NODE_C_ID="$(create_node node-c "${E2E_SOCKS_C_PORT}" "${E2E_NODE_C_PORT}")"
NODE_AB_IDS="$(jq -n --argjson a "${NODE_A_ID}" --argjson b "${NODE_B_ID}" '[$a,$b]')"
NODE_C_IDS="$(jq -n --argjson c "${NODE_C_ID}" '[$c]')"

DEFAULT_POOL_ID="$(api_get /api/proxy-pools | jq -er '.proxy_pools[0].id')"
log "configuring single and multiple proxy pools"
update_pool "${DEFAULT_POOL_ID}" pool-all "${E2E_POOL_ALL_PORT}" sequential true "[]"
create_pool pool-alpha "${E2E_POOL_ALPHA_PORT}" sequential false "$(jq -n --argjson a "${NODE_A_ID}" '[$a]')" >/dev/null
create_pool pool-beta "${E2E_POOL_BETA_PORT}" sequential false "$(jq -n --argjson b "${NODE_B_ID}" '[$b]')" >/dev/null
create_pool pool-random-c "${E2E_POOL_RANDOM_PORT}" random false "${NODE_C_IDS}" >/dev/null
create_pool pool-balance-ab "${E2E_POOL_BALANCE_PORT}" balance false "${NODE_AB_IDS}" >/dev/null

api_post /api/reload >/dev/null
for port in \
  "${E2E_POOL_ALL_PORT}" "${E2E_POOL_ALPHA_PORT}" "${E2E_POOL_BETA_PORT}" \
  "${E2E_POOL_RANDOM_PORT}" "${E2E_POOL_BALANCE_PORT}" \
  "${E2E_NODE_A_PORT}" "${E2E_NODE_B_PORT}" "${E2E_NODE_C_PORT}"; do
  wait_listener "${port}"
done

log "testing sequential rotation on pool-all"
proxy_get "${E2E_POOL_ALL_PORT}" /seq-1
assert_route /seq-1 node-a
proxy_get "${E2E_POOL_ALL_PORT}" /seq-2
assert_route /seq-2 node-b
proxy_get "${E2E_POOL_ALL_PORT}" /seq-3
assert_route /seq-3 node-c
proxy_get "${E2E_POOL_ALL_PORT}" /seq-4
assert_route /seq-4 node-a

log "testing multiple proxy pools with selected nodes"
proxy_get "${E2E_POOL_ALPHA_PORT}" /alpha-only
assert_route /alpha-only node-a
proxy_get "${E2E_POOL_BETA_PORT}" /beta-only
assert_route /beta-only node-b
proxy_get "${E2E_POOL_RANDOM_PORT}" /random-c
assert_route /random-c node-c

log "testing balance mode prefers the least active node"
proxy_get "${E2E_POOL_BALANCE_PORT}" '/hold?ms=1800' &
HOLD_PID=$!
for _ in $(seq 1 30); do
  if [[ "$(event_nodes_for_path '/hold?ms=1800' | tail -n 1)" == "node-a" ]]; then
    break
  fi
  sleep 0.1
done
proxy_get "${E2E_POOL_BALANCE_PORT}" /balance-next
assert_route '/hold?ms=1800' node-a
assert_route /balance-next node-b
wait "${HOLD_PID}"

log "testing node-local independent listeners"
proxy_get "${E2E_NODE_A_PORT}" /node-a-local
assert_route /node-a-local node-a
proxy_get "${E2E_NODE_B_PORT}" /node-b-local
assert_route /node-b-local node-b
proxy_get "${E2E_NODE_C_PORT}" /node-c-local
assert_route /node-c-local node-c

log "checking health probes use the configured local target"
EXPECTED_PROBE_DST="127.0.0.1:${E2E_TARGET_PORT}"
for _ in $(seq 1 30); do
  PROBE_COUNT="$(jq -r --arg dst "${EXPECTED_PROBE_DST}" \
    'select(.kind == "socks_http_request" and .path == "/generate_204" and .dst == $dst) | .node' \
    "${FIXTURE_LOG}" 2>/dev/null | wc -l)"
  if [[ "${PROBE_COUNT}" -ge 3 ]]; then
    break
  fi
  sleep 0.2
done
test "${PROBE_COUNT}" -ge 3
if jq -e --arg dst "${EXPECTED_PROBE_DST}" \
  'select(.kind == "socks_http_request" and .path == "/generate_204" and .dst != $dst)' \
  "${FIXTURE_LOG}" >/dev/null 2>&1; then
  printf 'health probe used an unexpected destination\n' >&2
  jq -r 'select(.kind == "socks_http_request" and .path == "/generate_204")' "${FIXTURE_LOG}" >&2
  exit 1
fi

log "checking export and monitor data"
api_get /api/export?scheme=http >"${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_POOL_ALL_PORT}" "${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_POOL_ALPHA_PORT}" "${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_POOL_BETA_PORT}" "${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_NODE_A_PORT}" "${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_NODE_B_PORT}" "${TMP_ROOT}/export.txt"
grep -Fq ":${E2E_NODE_C_PORT}" "${TMP_ROOT}/export.txt"

api_get /api/nodes >"${TMP_ROOT}/nodes.json"
jq -e '
  .total_nodes == 3
  and (.total_upload > 0)
  and (.total_download > 0)
  and ([.nodes[] | select(.name == "node-a" and .success_count > 0 and .total_upload > 0 and .total_download > 0 and .port == '"${E2E_NODE_A_PORT}"')] | length == 1)
  and ([.nodes[] | select(.name == "node-b" and .success_count > 0 and .total_upload > 0 and .total_download > 0 and .port == '"${E2E_NODE_B_PORT}"')] | length == 1)
  and ([.nodes[] | select(.name == "node-c" and .success_count > 0 and .total_upload > 0 and .total_download > 0 and .port == '"${E2E_NODE_C_PORT}"')] | length == 1)
' "${TMP_ROOT}/nodes.json" >/dev/null

api_get /api/debug >"${TMP_ROOT}/debug.json"
jq -e '.total_calls > 0 and .total_success > 0 and (.success_rate > 0)' "${TMP_ROOT}/debug.json" >/dev/null

run_ui_smoke

log "all proxy pool E2E checks passed"
