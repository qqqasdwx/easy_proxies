import type {
  AuthResponse,
  NodesResponse,
  DebugResponse,
  LogsResponse,
  SettingsData,
  SettingsUpdateResponse,
  ConfigNodesResponse,
  ConfigNodePayload,
  ConfigNodeMutationResponse,
  NodeURIParseResponse,
  SubscriptionStatus,
  SubscriptionSource,
  SubscriptionSourcePayload,
  ProbeSSEEvent,
  TrafficStreamEvent,
} from '../types'

// ---- Token management ----

let authToken: string | null = localStorage.getItem('auth_token')

export function getToken(): string | null {
  return authToken
}

export function setToken(token: string | null) {
  authToken = token
  if (token) {
    localStorage.setItem('auth_token', token)
  } else {
    localStorage.removeItem('auth_token')
  }
}

export function clearToken() {
  setToken(null)
}

// ---- Base request helper ----

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers: Record<string, string> = {
    ...(options.headers as Record<string, string> || {}),
  }

  // Add auth header if we have a token
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }

  // Set JSON content type for non-GET requests with body
  if (options.body && typeof options.body === 'string') {
    headers['Content-Type'] = 'application/json'
  }

  const res = await fetch(path, {
    ...options,
    headers,
    credentials: 'include', // send cookies
  })

  if (res.status === 401) {
    clearToken()
    // Dispatch a custom event so App can react
    window.dispatchEvent(new CustomEvent('auth:unauthorized'))
    throw new ApiError('未授权，请重新登录', 401)
  }

  if (!res.ok) {
    let msg = `HTTP ${res.status}`
    try {
      const body = await res.json()
      if (body.error) msg = body.error
    } catch { /* ignore parse errors */ }
    throw new ApiError(msg, res.status)
  }

  // Handle empty responses
  const text = await res.text()
  if (!text) return {} as T
  return JSON.parse(text) as T
}

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

interface RawSettings {
  external_ip?: string
  probe_target?: string
  log_level?: string
  skip_cert_verify?: boolean
  mode?: string
  listener?: {
    address?: string
    port?: number
    protocol?: string
    username?: string
    password?: string
  }
  multi_port?: {
    address?: string
    base_port?: number
    protocol?: string
    username?: string
    password?: string
  }
  pool?: {
    mode?: string
    failure_threshold?: number
    blacklist_duration?: string
  }
  management?: {
    enabled?: boolean
    listen?: string
    probe_target?: string
  }
  subscription_refresh?: {
    enabled?: boolean
    interval?: string
    timeout?: string
    health_check_timeout?: string
    drain_timeout?: string
    min_available_nodes?: number
  }
  geoip?: {
    enabled?: boolean
    database_path?: string
    auto_update_enabled?: boolean
    auto_update_interval?: string
  }
}

interface SubscriptionConfigResponse {
  enabled?: boolean
  interval?: string
  timeout?: string
  health_check_timeout?: string
  drain_timeout?: string
  min_available_nodes?: number
}

// ---- Auth API ----

/** Check if password is required & login */
export async function checkAuth(): Promise<AuthResponse> {
  const res = await fetch('/api/auth', { credentials: 'include' })
  if (!res.ok) {
    return { message: '需要登录' }
  }
  try {
    return await res.json()
  } catch {
    return { message: '需要登录' }
  }
}

export async function login(password: string): Promise<AuthResponse> {
  const res = await fetch('/api/auth', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password }),
    credentials: 'include',
  })

  if (!res.ok) {
    const body = await res.json()
    throw new ApiError(body.error || '登录失败', res.status)
  }

  const data: AuthResponse = await res.json()
  if (data.token) {
    setToken(data.token)
  }
  return data
}

export function logout() {
  clearToken()
}

// ---- Nodes API ----

export async function fetchNodes(): Promise<NodesResponse> {
  return request<NodesResponse>('/api/nodes')
}

export async function probeNode(tag: string): Promise<{ message: string; latency_ms: number }> {
  return request(`/api/nodes/${encodeURIComponent(tag)}/probe`, { method: 'POST' })
}

export async function releaseNode(tag: string): Promise<{ message: string }> {
  return request(`/api/nodes/${encodeURIComponent(tag)}/release`, { method: 'POST' })
}

/** Probe all nodes with SSE progress updates */
export function probeAllNodes(
  onEvent: (event: ProbeSSEEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    try {
      const headers: Record<string, string> = {}
      if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`
      }

      const res = await fetch('/api/nodes/probe-all', {
        method: 'POST',
        headers,
        credentials: 'include',
        signal: controller.signal,
      })

      if (!res.ok) {
        throw new ApiError(`探测失败: HTTP ${res.status}`, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as ProbeSSEEvent
              onEvent(data)
            } catch { /* skip malformed events */ }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

// ---- Traffic Stream API ----

/** Subscribe real-time traffic speeds via SSE */
export function streamTraffic(
  onEvent: (event: TrafficStreamEvent) => void,
  onError?: (error: Error) => void
): AbortController {
  const controller = new AbortController()

  const doFetch = async () => {
    try {
      const headers: Record<string, string> = {}
      if (authToken) {
        headers['Authorization'] = `Bearer ${authToken}`
      }

      const res = await fetch('/api/nodes/traffic/stream', {
        method: 'GET',
        headers,
        credentials: 'include',
        signal: controller.signal,
      })

      if (!res.ok) {
        throw new ApiError(`流量流订阅失败: HTTP ${res.status}`, res.status)
      }

      const reader = res.body?.getReader()
      if (!reader) throw new Error('No response body')

      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          const trimmed = line.trim()
          if (trimmed.startsWith('data: ')) {
            try {
              const data = JSON.parse(trimmed.slice(6)) as TrafficStreamEvent
              if (data.type === 'traffic') {
                onEvent(data)
              }
            } catch { /* skip malformed events */ }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name !== 'AbortError') {
        onError?.(err as Error)
      }
    }
  }

  doFetch()
  return controller
}

// ---- Debug API ----

export async function fetchDebug(): Promise<DebugResponse> {
  return request<DebugResponse>('/api/debug')
}

export async function fetchLogs(): Promise<LogsResponse> {
  return request<LogsResponse>('/api/logs')
}

// ---- Settings API ----

export async function fetchSettings(): Promise<SettingsData> {
  const raw = await request<RawSettings>('/api/settings')
  let sub: SubscriptionConfigResponse = {}
  try {
    sub = await request<SubscriptionConfigResponse>('/api/subscription/config')
  } catch {
    // Older or partially configured backends may not expose subscription config.
  }
  return normalizeSettings(raw, sub)
}

export async function updateSettings(settings: SettingsData): Promise<SettingsUpdateResponse> {
  const core = await request<SettingsUpdateResponse>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify({
      external_ip: settings.external_ip,
      probe_target: settings.management_probe_target,
      log_level: settings.log_level,
      skip_cert_verify: settings.skip_cert_verify,
      mode: settings.mode,
      listener: {
        address: settings.listener_address,
        port: settings.listener_port,
        protocol: settings.listener_protocol,
        username: settings.listener_username,
        password: settings.listener_password,
      },
      multi_port: {
        address: settings.multi_port_address,
        base_port: settings.multi_port_base_port,
        protocol: settings.multi_port_protocol,
        username: settings.multi_port_username,
        password: settings.multi_port_password,
      },
      pool: {
        mode: settings.pool_mode,
        failure_threshold: settings.pool_failure_threshold,
        blacklist_duration: settings.pool_blacklist_duration,
      },
      management: {
        enabled: settings.management_enabled,
        listen: settings.management_listen,
        probe_target: settings.management_probe_target,
      },
      geoip: {
        enabled: settings.geoip_enabled,
        database_path: settings.geoip_database_path,
        auto_update_enabled: settings.geoip_auto_update_enabled,
        auto_update_interval: settings.geoip_auto_update_interval,
      },
    }),
  })

  const sub = await request<{ message?: string }>('/api/subscription/config', {
    method: 'PUT',
    body: JSON.stringify({
      enabled: settings.sub_refresh_enabled,
      interval: settings.sub_refresh_interval,
      timeout: settings.sub_refresh_timeout,
      health_check_timeout: settings.sub_refresh_health_check_timeout,
      drain_timeout: settings.sub_refresh_drain_timeout,
      min_available_nodes: settings.sub_refresh_min_available_nodes,
    }),
  })

  return {
    message: sub.message || core.message || '设置已保存',
    need_reload: true,
  }
}

function normalizeSettings(raw: RawSettings, sub: SubscriptionConfigResponse): SettingsData {
  return {
    mode: raw.mode || 'pool',
    log_level: raw.log_level || 'info',
    external_ip: raw.external_ip || '',
    skip_cert_verify: raw.skip_cert_verify || false,

    listener_address: raw.listener?.address || '0.0.0.0',
    listener_port: raw.listener?.port || 2323,
    listener_protocol: raw.listener?.protocol || 'mixed',
    listener_username: raw.listener?.username || '',
    listener_password: raw.listener?.password || '',

    multi_port_address: raw.multi_port?.address || '0.0.0.0',
    multi_port_base_port: raw.multi_port?.base_port || 24000,
    multi_port_protocol: raw.multi_port?.protocol || 'mixed',
    multi_port_username: raw.multi_port?.username || '',
    multi_port_password: raw.multi_port?.password || '',

    pool_mode: raw.pool?.mode || 'sequential',
    pool_failure_threshold: raw.pool?.failure_threshold || 3,
    pool_blacklist_duration: raw.pool?.blacklist_duration || '24h0m0s',

    management_enabled: raw.management?.enabled ?? true,
    management_listen: raw.management?.listen || '0.0.0.0:9091',
    management_probe_target: raw.management?.probe_target || raw.probe_target || '',
    management_health_check_interval: '5m0s',

    sub_refresh_enabled: sub.enabled ?? raw.subscription_refresh?.enabled ?? false,
    sub_refresh_interval: sub.interval || raw.subscription_refresh?.interval || '1h0m0s',
    sub_refresh_timeout: sub.timeout || raw.subscription_refresh?.timeout || '30s',
    sub_refresh_health_check_timeout:
      sub.health_check_timeout || raw.subscription_refresh?.health_check_timeout || '30s',
    sub_refresh_drain_timeout: sub.drain_timeout || raw.subscription_refresh?.drain_timeout || '30s',
    sub_refresh_min_available_nodes:
      sub.min_available_nodes || raw.subscription_refresh?.min_available_nodes || 1,

    geoip_enabled: raw.geoip?.enabled || false,
    geoip_database_path: raw.geoip?.database_path || './GeoLite2-Country.mmdb',
    geoip_auto_update_enabled: raw.geoip?.auto_update_enabled ?? true,
    geoip_auto_update_interval: raw.geoip?.auto_update_interval || '24h0m0s',

  }
}

// ---- Config Nodes CRUD API ----

export async function fetchConfigNodes(): Promise<ConfigNodesResponse> {
  return request<ConfigNodesResponse>('/api/nodes/config')
}

export async function createConfigNode(payload: ConfigNodePayload): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>('/api/nodes/config', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export async function parseNodeURI(uri: string, name: string): Promise<NodeURIParseResponse> {
  return request<NodeURIParseResponse>('/api/nodes/config/parse-uri', {
    method: 'POST',
    body: JSON.stringify({ uri, name }),
  })
}

export async function updateConfigNode(name: string, payload: ConfigNodePayload): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export async function deleteConfigNode(name: string): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
}

export async function toggleConfigNode(name: string, enabled: boolean): Promise<ConfigNodeMutationResponse> {
  return request<ConfigNodeMutationResponse>(`/api/nodes/config/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify({ enabled }),
  })
}

export async function batchToggleConfigNodes(names: string[], enabled: boolean): Promise<{ message: string; success: number; total: number; errors?: string[] }> {
  return request('/api/nodes/config/batch-toggle', {
    method: 'POST',
    body: JSON.stringify({ names, enabled }),
  })
}

export async function batchDeleteConfigNodes(names: string[]): Promise<{ message: string; success: number; total: number; errors?: string[] }> {
  return request('/api/nodes/config/batch-delete', {
    method: 'POST',
    body: JSON.stringify({ names }),
  })
}

// ---- Reload API ----

export async function triggerReload(): Promise<{ message: string }> {
  return request('/api/reload', { method: 'POST' })
}

// ---- Subscription API ----

export async function fetchSubscriptionStatus(): Promise<SubscriptionStatus> {
  return request<SubscriptionStatus>('/api/subscription/status')
}

export async function refreshSubscription(): Promise<{ message: string; node_count: number }> {
  return request('/api/subscription/refresh', { method: 'POST' })
}

export async function fetchSubscriptions(): Promise<{ subscriptions: SubscriptionSource[] }> {
  return request('/api/subscriptions')
}

export async function createSubscription(payload: SubscriptionSourcePayload): Promise<{ subscription: SubscriptionSource; message: string }> {
  return request('/api/subscriptions', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}

export async function updateSubscription(id: number, payload: SubscriptionSourcePayload): Promise<{ subscription: SubscriptionSource; message: string }> {
  return request(`/api/subscriptions/${encodeURIComponent(String(id))}`, {
    method: 'PUT',
    body: JSON.stringify(payload),
  })
}

export async function deleteSubscription(id: number): Promise<{ message: string }> {
  return request(`/api/subscriptions/${encodeURIComponent(String(id))}`, { method: 'DELETE' })
}

export async function refreshSubscriptionSource(id: number): Promise<{ message: string; subscription?: SubscriptionSource }> {
  return request(`/api/subscriptions/${encodeURIComponent(String(id))}/refresh`, { method: 'POST' })
}

// ---- Export API ----

export async function exportProxies(): Promise<string> {
  const headers: Record<string, string> = {}
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`
  }
  const res = await fetch('/api/export', {
    headers,
    credentials: 'include',
  })
  if (!res.ok) throw new ApiError('导出失败', res.status)
  return res.text()
}

// ---- Import API ----

export async function importNodes(content: string): Promise<{ message: string; imported: number; errors?: string[] }> {
  return request('/api/import', {
    method: 'POST',
    body: JSON.stringify({ content }),
  })
}
