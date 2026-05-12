// ---- Node & Snapshot types (maps to monitor.Snapshot) ----

export interface NodeInfo {
  tag: string
  name: string
  uri: string
  mode: string
  listen_address?: string
  port?: number
  region?: string
  country?: string
}

export interface TimelineEvent {
  time: string
  success: boolean
  latency_ms: number
  error?: string
  destination?: string
}

export interface NodeSnapshot extends NodeInfo {
  failure_count: number
  success_count: number
  blacklisted: boolean
  blacklisted_until: string
  active_connections: number
  last_error?: string
  last_failure?: string
  last_success?: string
  last_probe_latency?: number
  last_latency_ms: number
  available: boolean
  initial_check_done: boolean
  total_upload: number
  total_download: number
  timeline?: TimelineEvent[]
}

// ---- API Response types ----

export interface NodesResponse {
  nodes: NodeSnapshot[]
  total_nodes: number
  total_upload: number
  total_download: number
  upload_speed?: number
  download_speed?: number
  traffic_sampled?: string
  region_stats: Record<string, number>
  region_healthy: Record<string, number>
}

export interface DebugNode {
  tag: string
  name: string
  mode: string
  port: number
  failure_count: number
  success_count: number
  active_connections: number
  last_latency_ms: number
  last_success: string
  last_failure: string
  last_error: string
  blacklisted: boolean
  total_upload: number
  total_download: number
  timeline: TimelineEvent[]
}

export interface DebugResponse {
  nodes: DebugNode[]
  total_calls: number
  total_success: number
  success_rate: number
}

export interface LogsResponse {
  logs: string
}

export interface SettingsData {
  // Global
  log_level: string
  external_ip: string
  skip_cert_verify: boolean

  // Multi-port
  multi_port_address: string
  multi_port_base_port: number
  multi_port_protocol: string
  multi_port_username: string
  multi_port_password: string

  // DNS
  dns_enabled: boolean
  dns_server: string
  dns_fallback_servers: string[]
  dns_port: number
  dns_strategy: string

  // Management
  management_probe_target: string

  // GeoIP
  geoip_enabled: boolean
  geoip_database_path: string
  geoip_database_updated_at: string
  geoip_listen: string
  geoip_port: number
  geoip_auto_update_enabled: boolean
  geoip_auto_update_interval: string

  // Health check
  health_check_interval: string
  health_check_timeout: string
  health_check_concurrency: number

}

export interface SettingsUpdateResponse {
  message: string
  need_reload: boolean
}

// ---- Auth types ----

export interface AuthResponse {
  message: string
  token?: string
  no_password?: boolean
}

export interface ErrorResponse {
  error: string
}

// ---- Config Node CRUD types ----

export interface ConfigNodePayload {
  name: string
  uri: string
  outbound_json: string
  port: number
  inbound_protocol: string
  username: string
  password: string
}

export interface ConfigNodeConfig {
  id?: number
  name: string
  uri: string
  outbound_json?: string
  port: number
  inbound_protocol?: string
  username: string
  password: string
  source?: string
  disabled?: boolean
}

export interface ConfigNodesResponse {
  nodes: ConfigNodeConfig[]
}

export interface ConfigNodeMutationResponse {
  node?: ConfigNodeConfig
  message: string
}

export interface NodeURIParseResponse {
  name: string
  uri: string
  outbound_json: string
}

// ---- Proxy pool types ----

export interface ProxyPool {
  id: number
  name: string
  enabled: boolean
  listen_address: string
  listen_port: number
  protocol: string
  username: string
  password: string
  mode: string
  failure_threshold: number
  blacklist_duration: string
  all_nodes: boolean
  node_ids: number[]
  created_at?: string
  updated_at?: string
}

export interface ProxyPoolPayload {
  name: string
  enabled: boolean
  listen_address: string
  listen_port: number
  protocol: string
  username: string
  password: string
  mode: string
  failure_threshold: number
  blacklist_duration: string
  all_nodes: boolean
  node_ids: number[]
}

export interface ProxyPoolsResponse {
  proxy_pools: ProxyPool[]
}

// ---- Subscription types ----

export interface SubscriptionStatus {
  enabled: boolean
  has_subscriptions?: boolean
  last_refresh?: string
  next_refresh?: string
  node_count?: number
  last_error?: string
  refresh_count?: number
  is_refreshing?: boolean
  message?: string
}

export interface SubscriptionSource {
  id: number
  name: string
  url: string
  enabled: boolean
  auto_update: boolean
  interval: string
  last_refresh?: string
  next_refresh?: string
  node_count: number
  last_error?: string
  created_at?: string
  updated_at?: string
}

export interface SubscriptionSourcePayload {
  name: string
  url: string
  enabled: boolean
  auto_update: boolean
  interval: string
}

export interface SubscriptionRefreshSettings {
  timeout: string
  health_check_timeout: string
  drain_timeout: string
  min_available_nodes: number
}

// ---- SSE Probe types ----

export interface ProbeSSEStart {
  type: 'start'
  total: number
}

export interface ProbeSSEProgress {
  type: 'progress'
  tag: string
  name: string
  latency: number
  status: 'success' | 'error'
  error: string
  current: number
  total: number
  progress: number
}

export interface ProbeSSEComplete {
  type: 'complete'
  total: number
  success: number
  failed: number
}

export type ProbeSSEEvent = ProbeSSEStart | ProbeSSEProgress | ProbeSSEComplete

// ---- SSE Traffic stream types ----

export interface TrafficStreamNode {
  tag: string
  upload_speed: number
  download_speed: number
  total_upload: number
  total_download: number
}

export interface TrafficStreamEvent {
  type: 'traffic'
  node_count: number
  total_upload: number
  total_download: number
  upload_speed: number
  download_speed: number
  sampled_at: string
  nodes: TrafficStreamNode[]
}
