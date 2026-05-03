import { useEffect, useMemo, useState } from 'react'
import type { ConfigNodePayload } from '../types'
import { parseNodeURI } from '../api/client'

type EditorTab = 'form' | 'json' | 'inbound'

type OutboundForm = {
  type: string
  server: string
  server_port: number
  server_ports: string
  uuid: string
  password: string
  username: string
  method: string
  plugin: string
  plugin_opts: string
  security: string
  alter_id: number
  global_padding: boolean
  authenticated_length: boolean
  version: string
  network: string
  http_path: string
  tls_enabled: boolean
  tls_server_name: string
  tls_insecure: boolean
  tls_alpn: string
  tls_min_version: string
  tls_max_version: string
  transport_type: string
  transport_path: string
  transport_host: string
  transport_method: string
  flow: string
  packet_encoding: string
  obfs_type: string
  obfs_password: string
  up_mbps: number
  down_mbps: number
  hop_interval: string
  hop_interval_max: string
  bbr_profile: string
  brutal_debug: boolean
  congestion_control: string
  udp_relay_mode: string
  udp_over_stream: boolean
  zero_rtt_handshake: boolean
  heartbeat: string
  idle_session_check_interval: string
  idle_session_timeout: string
  min_idle_session: number
}

const protocolOptions = [
  'socks',
  'http',
  'shadowsocks',
  'vmess',
  'vless',
  'trojan',
  'hysteria2',
  'tuic',
  'anytls',
]

const networkOptions = ['', 'tcp', 'udp']
const socksVersionOptions = ['5', '4', '4a']
const shadowsocksMethodOptions = [
  '2022-blake3-aes-128-gcm',
  '2022-blake3-aes-256-gcm',
  '2022-blake3-chacha20-poly1305',
  'none',
  'aes-128-gcm',
  'aes-192-gcm',
  'aes-256-gcm',
  'chacha20-ietf-poly1305',
  'xchacha20-ietf-poly1305',
  'aes-128-ctr',
  'aes-192-ctr',
  'aes-256-ctr',
  'aes-128-cfb',
  'aes-192-cfb',
  'aes-256-cfb',
  'rc4-md5',
  'chacha20-ietf',
  'xchacha20',
]
const shadowsocksPluginOptions = ['', 'obfs-local', 'v2ray-plugin']
const vmessSecurityOptions = ['auto', 'none', 'zero', 'aes-128-gcm', 'chacha20-poly1305', 'aes-128-ctr']
const packetEncodingOptions = ['', 'packetaddr', 'xudp']
const flowOptions = ['', 'xtls-rprx-vision']
const transportOptions = ['', 'http', 'ws', 'quic', 'grpc', 'httpupgrade']
const tlsVersionOptions = ['', '1.0', '1.1', '1.2', '1.3']
const tuicCongestionOptions = ['cubic', 'new_reno', 'bbr']
const tuicRelayModeOptions = ['', 'native', 'quic']
const obfsTypeOptions = ['', 'salamander']
const bbrProfileOptions = ['', 'conservative', 'standard', 'aggressive']

const defaultOutboundForm: OutboundForm = {
  type: 'socks',
  server: '',
  server_port: 0,
  server_ports: '',
  uuid: '',
  password: '',
  username: '',
  method: '2022-blake3-aes-128-gcm',
  plugin: '',
  plugin_opts: '',
  security: 'auto',
  alter_id: 0,
  global_padding: false,
  authenticated_length: true,
  version: '5',
  network: '',
  http_path: '',
  tls_enabled: false,
  tls_server_name: '',
  tls_insecure: false,
  tls_alpn: '',
  tls_min_version: '',
  tls_max_version: '',
  transport_type: '',
  transport_path: '',
  transport_host: '',
  transport_method: '',
  flow: '',
  packet_encoding: '',
  obfs_type: '',
  obfs_password: '',
  up_mbps: 0,
  down_mbps: 0,
  hop_interval: '',
  hop_interval_max: '',
  bbr_profile: '',
  brutal_debug: false,
  congestion_control: 'cubic',
  udp_relay_mode: '',
  udp_over_stream: false,
  zero_rtt_handshake: false,
  heartbeat: '',
  idle_session_check_interval: '',
  idle_session_timeout: '',
  min_idle_session: 0,
}

function parseOutboundForm(raw: string): OutboundForm | null {
  if (!raw.trim()) return null
  try {
    const obj = JSON.parse(raw) as Record<string, any>
    const transport = obj.transport || {}
    const hostHeader = transport.headers?.Host || transport.headers?.host
    const transportHost = Array.isArray(hostHeader) ? hostHeader[0] : (transport.host || '')
    return {
      ...defaultOutboundForm,
      type: obj.type || defaultOutboundForm.type,
      server: obj.server || '',
      server_port: Number(obj.server_port || 0),
      server_ports: Array.isArray(obj.server_ports) ? obj.server_ports.join(',') : (obj.server_ports || ''),
      uuid: obj.uuid || '',
      password: obj.password || '',
      username: obj.username || '',
      method: obj.method || defaultOutboundForm.method,
      plugin: obj.plugin || '',
      plugin_opts: obj.plugin_opts || '',
      security: obj.security || defaultOutboundForm.security,
      alter_id: Number(obj.alter_id || 0),
      global_padding: !!obj.global_padding,
      authenticated_length: obj.authenticated_length ?? defaultOutboundForm.authenticated_length,
      version: obj.version || defaultOutboundForm.version,
      network: obj.network || '',
      http_path: obj.path || '',
      tls_enabled: !!obj.tls?.enabled,
      tls_server_name: obj.tls?.server_name || '',
      tls_insecure: !!obj.tls?.insecure,
      tls_alpn: Array.isArray(obj.tls?.alpn) ? obj.tls.alpn.join(',') : '',
      tls_min_version: obj.tls?.min_version || '',
      tls_max_version: obj.tls?.max_version || '',
      transport_type: transport.type || '',
      transport_path: transport.path || transport.service_name || '',
      transport_host: Array.isArray(transportHost) ? transportHost[0] : transportHost,
      transport_method: transport.method || '',
      flow: obj.flow || '',
      packet_encoding: obj.packet_encoding || '',
      obfs_type: obj.obfs?.type || '',
      obfs_password: obj.obfs?.password || '',
      up_mbps: Number(obj.up_mbps || 0),
      down_mbps: Number(obj.down_mbps || 0),
      hop_interval: obj.hop_interval || '',
      hop_interval_max: obj.hop_interval_max || '',
      bbr_profile: obj.bbr_profile || '',
      brutal_debug: !!obj.brutal_debug,
      congestion_control: obj.congestion_control || defaultOutboundForm.congestion_control,
      udp_relay_mode: obj.udp_relay_mode || '',
      udp_over_stream: !!obj.udp_over_stream,
      zero_rtt_handshake: !!obj.zero_rtt_handshake,
      heartbeat: obj.heartbeat || '',
      idle_session_check_interval: obj.idle_session_check_interval || '',
      idle_session_timeout: obj.idle_session_timeout || '',
      min_idle_session: Number(obj.min_idle_session || 0),
    }
  } catch {
    return null
  }
}

function parseOutboundObject(raw: string): Record<string, any> | null {
  if (!raw.trim()) return null
  try {
    const parsed = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
    return parsed as Record<string, any>
  } catch {
    return null
  }
}

function validateOutboundJSON(raw: string): string {
  if (!raw.trim()) return ''
  const parsed = parseOutboundObject(raw)
  if (!parsed) return 'JSON 格式无效'
  return parsed.type ? '' : 'JSON 缺少 type 字段'
}

function ensureObject(parent: Record<string, any>, key: string): Record<string, any> {
  const value = parent[key]
  if (value && typeof value === 'object' && !Array.isArray(value)) return value
  const next: Record<string, any> = {}
  parent[key] = next
  return next
}

function removeIfEmptyObject(parent: Record<string, any>, key: string) {
  const value = parent[key]
  if (value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === 0) {
    delete parent[key]
  }
}

function setStringField(obj: Record<string, any>, key: string, value: string) {
  const next = value.trim()
  if (next) obj[key] = next
  else delete obj[key]
}

function setNumberField(obj: Record<string, any>, key: string, value: number) {
  if (value > 0) obj[key] = value
  else delete obj[key]
}

function setBooleanField(obj: Record<string, any>, key: string, value: boolean) {
  if (value) obj[key] = true
  else delete obj[key]
}

function setArrayField(obj: Record<string, any>, key: string, value: string) {
  const parts = value.split(',').map(item => item.trim()).filter(Boolean)
  if (parts.length > 0) obj[key] = parts
  else delete obj[key]
}

function setTransportHost(transport: Record<string, any>, transportType: string, value: string) {
  const next = value.trim()
  if (!next) {
    delete transport.host
    if (transport.headers && typeof transport.headers === 'object' && !Array.isArray(transport.headers)) {
      delete transport.headers.Host
      delete transport.headers.host
      if (Object.keys(transport.headers).length === 0) delete transport.headers
    }
    return
  }
  if (transportType === 'http') {
    transport.host = [next]
    if (transport.headers && typeof transport.headers === 'object' && !Array.isArray(transport.headers)) {
      delete transport.headers.Host
      delete transport.headers.host
      if (Object.keys(transport.headers).length === 0) delete transport.headers
    }
    return
  }
  if (transportType === 'httpupgrade') {
    transport.host = next
    if (transport.headers && typeof transport.headers === 'object' && !Array.isArray(transport.headers)) {
      delete transport.headers.Host
      delete transport.headers.host
      if (Object.keys(transport.headers).length === 0) delete transport.headers
    }
    return
  }
  delete transport.host
  const headers = ensureObject(transport, 'headers')
  headers.Host = [next]
}

function patchOutboundJSON(raw: string, nextForm: OutboundForm, patch: Partial<OutboundForm>, tag: string): string {
  const outbound = parseOutboundObject(raw) || { type: nextForm.type || 'socks', tag: tag || 'node' }

  if ('type' in patch) setStringField(outbound, 'type', nextForm.type)
  if ('server' in patch) setStringField(outbound, 'server', nextForm.server)
  if ('server_port' in patch) setNumberField(outbound, 'server_port', nextForm.server_port)
  if ('server_ports' in patch) setArrayField(outbound, 'server_ports', nextForm.server_ports)
  if ('uuid' in patch) setStringField(outbound, 'uuid', nextForm.uuid)
  if ('password' in patch) setStringField(outbound, 'password', nextForm.password)
  if ('username' in patch) setStringField(outbound, 'username', nextForm.username)
  if ('method' in patch) setStringField(outbound, 'method', nextForm.method)
  if ('plugin' in patch) setStringField(outbound, 'plugin', nextForm.plugin)
  if ('plugin_opts' in patch) setStringField(outbound, 'plugin_opts', nextForm.plugin_opts)
  if ('security' in patch) setStringField(outbound, 'security', nextForm.security)
  if ('alter_id' in patch) setNumberField(outbound, 'alter_id', nextForm.alter_id)
  if ('global_padding' in patch) setBooleanField(outbound, 'global_padding', nextForm.global_padding)
  if ('authenticated_length' in patch) outbound.authenticated_length = nextForm.authenticated_length
  if ('version' in patch) setStringField(outbound, 'version', nextForm.version)
  if ('network' in patch) setStringField(outbound, 'network', nextForm.network)
  if ('http_path' in patch) setStringField(outbound, 'path', nextForm.http_path)
  if ('flow' in patch) setStringField(outbound, 'flow', nextForm.flow)
  if ('packet_encoding' in patch) setStringField(outbound, 'packet_encoding', nextForm.packet_encoding)
  if ('up_mbps' in patch) setNumberField(outbound, 'up_mbps', nextForm.up_mbps)
  if ('down_mbps' in patch) setNumberField(outbound, 'down_mbps', nextForm.down_mbps)
  if ('hop_interval' in patch) setStringField(outbound, 'hop_interval', nextForm.hop_interval)
  if ('hop_interval_max' in patch) setStringField(outbound, 'hop_interval_max', nextForm.hop_interval_max)
  if ('bbr_profile' in patch) setStringField(outbound, 'bbr_profile', nextForm.bbr_profile)
  if ('brutal_debug' in patch) setBooleanField(outbound, 'brutal_debug', nextForm.brutal_debug)
  if ('congestion_control' in patch) setStringField(outbound, 'congestion_control', nextForm.congestion_control)
  if ('udp_relay_mode' in patch) setStringField(outbound, 'udp_relay_mode', nextForm.udp_relay_mode)
  if ('udp_over_stream' in patch) setBooleanField(outbound, 'udp_over_stream', nextForm.udp_over_stream)
  if ('zero_rtt_handshake' in patch) setBooleanField(outbound, 'zero_rtt_handshake', nextForm.zero_rtt_handshake)
  if ('heartbeat' in patch) setStringField(outbound, 'heartbeat', nextForm.heartbeat)
  if ('idle_session_check_interval' in patch) setStringField(outbound, 'idle_session_check_interval', nextForm.idle_session_check_interval)
  if ('idle_session_timeout' in patch) setStringField(outbound, 'idle_session_timeout', nextForm.idle_session_timeout)
  if ('min_idle_session' in patch) setNumberField(outbound, 'min_idle_session', nextForm.min_idle_session)

  if ('tls_enabled' in patch || 'tls_server_name' in patch || 'tls_insecure' in patch) {
    const tls = ensureObject(outbound, 'tls')
    if ('tls_enabled' in patch) tls.enabled = nextForm.tls_enabled
    if ('tls_server_name' in patch) setStringField(tls, 'server_name', nextForm.tls_server_name)
    if ('tls_insecure' in patch) tls.insecure = nextForm.tls_insecure
    if ('tls_alpn' in patch) setArrayField(tls, 'alpn', nextForm.tls_alpn)
    if ('tls_min_version' in patch) setStringField(tls, 'min_version', nextForm.tls_min_version)
    if ('tls_max_version' in patch) setStringField(tls, 'max_version', nextForm.tls_max_version)
    removeIfEmptyObject(outbound, 'tls')
  }

  if ('transport_type' in patch || 'transport_path' in patch || 'transport_host' in patch) {
    const transport = ensureObject(outbound, 'transport')
    if ('transport_type' in patch) setStringField(transport, 'type', nextForm.transport_type)
    if ('transport_method' in patch) setStringField(transport, 'method', nextForm.transport_method)
    if ('transport_path' in patch) {
      if (nextForm.transport_type === 'grpc') {
        setStringField(transport, 'service_name', nextForm.transport_path)
        delete transport.path
      } else {
        setStringField(transport, 'path', nextForm.transport_path)
        delete transport.service_name
      }
    }
    if ('transport_host' in patch) setTransportHost(transport, nextForm.transport_type, nextForm.transport_host)
    removeIfEmptyObject(outbound, 'transport')
  }

  if ('obfs_type' in patch || 'obfs_password' in patch) {
    const obfs = ensureObject(outbound, 'obfs')
    if ('obfs_type' in patch) setStringField(obfs, 'type', nextForm.obfs_type)
    if ('obfs_password' in patch) setStringField(obfs, 'password', nextForm.obfs_password)
    removeIfEmptyObject(outbound, 'obfs')
  }

  return JSON.stringify(outbound, null, 2)
}

function isV2RayLike(type: string) {
  return type === 'vmess' || type === 'vless' || type === 'trojan'
}

function supportsNetwork(type: string) {
  return ['socks', 'shadowsocks', 'vmess', 'vless', 'trojan', 'hysteria2', 'tuic'].includes(type)
}

function supportsTLS(type: string) {
  return ['http', 'vmess', 'vless', 'trojan', 'hysteria2', 'tuic', 'anytls'].includes(type)
}

function isSupportedURI(value: string) {
  const lower = value.trim().toLowerCase()
  return [
    'vmess://',
    'vless://',
    'trojan://',
    'ss://',
    'shadowsocks://',
    'hysteria2://',
    'hy2://',
    'tuic://',
    'socks5://',
    'socks://',
    'http://',
    'https://',
    'anytls://',
  ].some(prefix => lower.startsWith(prefix))
}

interface Props {
  open: boolean
  editingName: string | null
  readOnly: boolean
  form: ConfigNodePayload
  formError: string
  submitting: boolean
  onClose: () => void
  onSubmit: (e: React.FormEvent) => void
  onChange: (form: ConfigNodePayload) => void
}

export default function NodeEditorModal({
  open,
  editingName,
  readOnly,
  form,
  formError,
  submitting,
  onClose,
  onSubmit,
  onChange,
}: Props) {
  const [tab, setTab] = useState<EditorTab>('form')
  const [jsonError, setJsonError] = useState('')
  const [uriError, setUriError] = useState('')
  const [uriParsing, setUriParsing] = useState(false)
  const [outboundForm, setOutboundForm] = useState<OutboundForm>(defaultOutboundForm)

  useEffect(() => {
    if (!open) return
    setTab(editingName ? 'form' : 'json')
    setJsonError(validateOutboundJSON(form.outbound_json || ''))
    setUriError('')
    setUriParsing(false)
    setOutboundForm(parseOutboundForm(form.outbound_json || '') || defaultOutboundForm)
  }, [open, editingName])

  const jsonPreview = useMemo(() => form.outbound_json || '', [form.outbound_json])

  if (!open) return null

  const updateOutboundForm = (patch: Partial<OutboundForm>) => {
    const next = { ...outboundForm, ...patch }
    setOutboundForm(next)
    const outboundJSON = patchOutboundJSON(form.outbound_json || '', next, patch, form.name)
    onChange({ ...form, outbound_json: outboundJSON })
    setJsonError('')
  }

  const handleJSONChange = (value: string) => {
    onChange({ ...form, outbound_json: value })
    setJsonError(validateOutboundJSON(value))
    const nextForm = parseOutboundForm(value)
    if (nextForm) setOutboundForm(nextForm)
  }

  const handleParseURI = async () => {
    if (readOnly) return
    const uri = form.uri.trim()
    if (!uri) {
      setUriError('URI 不能为空')
      return
    }
    if (!isSupportedURI(uri)) {
      setUriError('不支持的代理 URI')
      return
    }
    setUriParsing(true)
    setUriError('')
    try {
      const parsed = await parseNodeURI(uri, form.name)
      const nextName = form.name.trim() ? form.name : parsed.name
      onChange({
        ...form,
        name: nextName,
        uri: parsed.uri,
        outbound_json: parsed.outbound_json,
      })
      setOutboundForm(parseOutboundForm(parsed.outbound_json) || defaultOutboundForm)
      setJsonError('')
    } catch (err) {
      setUriError(err instanceof Error ? err.message : 'URI 解析失败')
    } finally {
      setUriParsing(false)
    }
  }

  const title = readOnly ? `查看节点: ${editingName}` : editingName ? `编辑节点: ${editingName}` : '添加节点'

  return (
    <div className="modal modal-open">
      <div className="modal-box max-w-4xl">
        <h3 className="font-bold text-xl mb-4">{title}</h3>
        <form onSubmit={onSubmit}>
          {formError && <div className="alert alert-error mb-3 py-2 text-sm"><span>{formError}</span></div>}
          {jsonError && tab === 'json' && <div className="alert alert-warning mb-3 py-2 text-sm"><span>{jsonError}</span></div>}
          {uriError && tab === 'json' && <div className="alert alert-warning mb-3 py-2 text-sm"><span>{uriError}</span></div>}

          <div role="tablist" className="tabs tabs-boxed mb-4 bg-base-200/70">
            <button type="button" role="tab" className={`tab ${tab === 'form' ? 'tab-active' : ''}`} onClick={() => setTab('form')}>Form</button>
            <button type="button" role="tab" className={`tab ${tab === 'json' ? 'tab-active' : ''}`} onClick={() => setTab('json')}>JSON</button>
            <button type="button" role="tab" className={`tab ${tab === 'inbound' ? 'tab-active' : ''}`} onClick={() => setTab('inbound')}>入站</button>
          </div>

          {tab === 'form' && (
            <div className="space-y-4">
              <div className="grid md:grid-cols-2 gap-3">
                <fieldset className="fieldset md:col-span-2">
                  <legend className="fieldset-legend">名称 *</legend>
                  <input className="input input-sm w-full" value={form.name} disabled={readOnly || !!editingName} onChange={(e) => onChange({ ...form, name: e.target.value })} />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">类型</legend>
                  <select className="select select-sm w-full" value={outboundForm.type} disabled={readOnly} onChange={(e) => updateOutboundForm({ type: e.target.value })}>
                    {protocolOptions.map(p => <option key={p} value={p}>{p}</option>)}
                  </select>
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">服务器</legend>
                  <input className="input input-sm w-full" value={outboundForm.server} disabled={readOnly} onChange={(e) => updateOutboundForm({ server: e.target.value })} />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">服务器端口</legend>
                  <input type="number" min={0} max={65535} className="input input-sm w-full" value={outboundForm.server_port || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ server_port: parseInt(e.target.value) || 0 })} />
                </fieldset>
                {supportsNetwork(outboundForm.type) && (
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Network</legend>
                    <select className="select select-sm w-full" value={outboundForm.network} disabled={readOnly} onChange={(e) => updateOutboundForm({ network: e.target.value })}>
                      {networkOptions.map(value => <option key={value || 'default'} value={value}>{value || '默认'}</option>)}
                    </select>
                  </fieldset>
                )}
              </div>

              <div className="divider my-1 text-xs">协议</div>
              <div className="grid md:grid-cols-2 gap-3">
                {outboundForm.type === 'socks' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Version</legend>
                      <select className="select select-sm w-full" value={outboundForm.version} disabled={readOnly} onChange={(e) => updateOutboundForm({ version: e.target.value })}>
                        {socksVersionOptions.map(value => <option key={value} value={value}>{value}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游用户名</legend>
                      <input className="input input-sm w-full" value={outboundForm.username} disabled={readOnly} onChange={(e) => updateOutboundForm({ username: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游密码</legend>
                      <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                    </fieldset>
                  </>
                )}

                {outboundForm.type === 'http' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游用户名</legend>
                      <input className="input input-sm w-full" value={outboundForm.username} disabled={readOnly} onChange={(e) => updateOutboundForm({ username: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游密码</legend>
                      <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset md:col-span-2">
                      <legend className="fieldset-legend">Path</legend>
                      <input className="input input-sm w-full" value={outboundForm.http_path} disabled={readOnly} onChange={(e) => updateOutboundForm({ http_path: e.target.value })} />
                    </fieldset>
                  </>
                )}

                {outboundForm.type === 'shadowsocks' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Method</legend>
                      <select className="select select-sm w-full" value={outboundForm.method} disabled={readOnly} onChange={(e) => updateOutboundForm({ method: e.target.value })}>
                        {shadowsocksMethodOptions.map(value => <option key={value} value={value}>{value}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游密码</legend>
                      <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Plugin</legend>
                      <select className="select select-sm w-full" value={outboundForm.plugin} disabled={readOnly} onChange={(e) => updateOutboundForm({ plugin: e.target.value })}>
                        {shadowsocksPluginOptions.map(value => <option key={value || 'none'} value={value}>{value || '无'}</option>)}
                      </select>
                    </fieldset>
                    {outboundForm.plugin && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Plugin Options</legend>
                        <input className="input input-sm w-full" value={outboundForm.plugin_opts} disabled={readOnly} onChange={(e) => updateOutboundForm({ plugin_opts: e.target.value })} />
                      </fieldset>
                    )}
                  </>
                )}

                {outboundForm.type === 'vmess' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">UUID</legend>
                      <input className="input input-sm w-full font-mono text-xs" value={outboundForm.uuid} disabled={readOnly} onChange={(e) => updateOutboundForm({ uuid: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Security</legend>
                      <select className="select select-sm w-full" value={outboundForm.security} disabled={readOnly} onChange={(e) => updateOutboundForm({ security: e.target.value })}>
                        {vmessSecurityOptions.map(value => <option key={value} value={value}>{value}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Alter ID</legend>
                      <input type="number" min={0} className="input input-sm w-full" value={outboundForm.alter_id || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ alter_id: parseInt(e.target.value) || 0 })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Packet Encoding</legend>
                      <select className="select select-sm w-full" value={outboundForm.packet_encoding} disabled={readOnly} onChange={(e) => updateOutboundForm({ packet_encoding: e.target.value })}>
                        {packetEncodingOptions.map(value => <option key={value || 'none'} value={value}>{value || '默认'}</option>)}
                      </select>
                    </fieldset>
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.global_padding} disabled={readOnly} onChange={(e) => updateOutboundForm({ global_padding: e.target.checked })} />
                      <span className="label-text">Global Padding</span>
                    </label>
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.authenticated_length} disabled={readOnly} onChange={(e) => updateOutboundForm({ authenticated_length: e.target.checked })} />
                      <span className="label-text">Authenticated Length</span>
                    </label>
                  </>
                )}

                {outboundForm.type === 'vless' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">UUID</legend>
                      <input className="input input-sm w-full font-mono text-xs" value={outboundForm.uuid} disabled={readOnly} onChange={(e) => updateOutboundForm({ uuid: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Flow</legend>
                      <select className="select select-sm w-full" value={outboundForm.flow} disabled={readOnly} onChange={(e) => updateOutboundForm({ flow: e.target.value })}>
                        {flowOptions.map(value => <option key={value || 'none'} value={value}>{value || '无'}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Packet Encoding</legend>
                      <select className="select select-sm w-full" value={outboundForm.packet_encoding} disabled={readOnly} onChange={(e) => updateOutboundForm({ packet_encoding: e.target.value })}>
                        {packetEncodingOptions.map(value => <option key={value || 'none'} value={value}>{value || '默认'}</option>)}
                      </select>
                    </fieldset>
                  </>
                )}

                {(outboundForm.type === 'trojan' || outboundForm.type === 'anytls') && (
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">上游密码</legend>
                    <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                  </fieldset>
                )}

                {outboundForm.type === 'hysteria2' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游密码</legend>
                      <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Server Ports</legend>
                      <input className="input input-sm w-full" value={outboundForm.server_ports} disabled={readOnly} onChange={(e) => updateOutboundForm({ server_ports: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Obfs</legend>
                      <select className="select select-sm w-full" value={outboundForm.obfs_type} disabled={readOnly} onChange={(e) => updateOutboundForm({ obfs_type: e.target.value })}>
                        {obfsTypeOptions.map(value => <option key={value || 'none'} value={value}>{value || '无'}</option>)}
                      </select>
                    </fieldset>
                    {outboundForm.obfs_type && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Obfs Password</legend>
                        <input className="input input-sm w-full" value={outboundForm.obfs_password} disabled={readOnly} onChange={(e) => updateOutboundForm({ obfs_password: e.target.value })} />
                      </fieldset>
                    )}
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Up Mbps</legend>
                      <input type="number" min={0} className="input input-sm w-full" value={outboundForm.up_mbps || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ up_mbps: parseInt(e.target.value) || 0 })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Down Mbps</legend>
                      <input type="number" min={0} className="input input-sm w-full" value={outboundForm.down_mbps || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ down_mbps: parseInt(e.target.value) || 0 })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Hop Interval</legend>
                      <input className="input input-sm w-full" value={outboundForm.hop_interval} disabled={readOnly} onChange={(e) => updateOutboundForm({ hop_interval: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Hop Interval Max</legend>
                      <input className="input input-sm w-full" value={outboundForm.hop_interval_max} disabled={readOnly} onChange={(e) => updateOutboundForm({ hop_interval_max: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">BBR Profile</legend>
                      <select className="select select-sm w-full" value={outboundForm.bbr_profile} disabled={readOnly} onChange={(e) => updateOutboundForm({ bbr_profile: e.target.value })}>
                        {bbrProfileOptions.map(value => <option key={value || 'none'} value={value}>{value || '默认'}</option>)}
                      </select>
                    </fieldset>
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.brutal_debug} disabled={readOnly} onChange={(e) => updateOutboundForm({ brutal_debug: e.target.checked })} />
                      <span className="label-text">Brutal Debug</span>
                    </label>
                  </>
                )}

                {outboundForm.type === 'tuic' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">UUID</legend>
                      <input className="input input-sm w-full font-mono text-xs" value={outboundForm.uuid} disabled={readOnly} onChange={(e) => updateOutboundForm({ uuid: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">上游密码</legend>
                      <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Congestion Control</legend>
                      <select className="select select-sm w-full" value={outboundForm.congestion_control} disabled={readOnly} onChange={(e) => updateOutboundForm({ congestion_control: e.target.value })}>
                        {tuicCongestionOptions.map(value => <option key={value} value={value}>{value}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">UDP Relay Mode</legend>
                      <select className="select select-sm w-full" value={outboundForm.udp_relay_mode} disabled={readOnly} onChange={(e) => updateOutboundForm({ udp_relay_mode: e.target.value })}>
                        {tuicRelayModeOptions.map(value => <option key={value || 'default'} value={value}>{value || '默认'}</option>)}
                      </select>
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Heartbeat</legend>
                      <input className="input input-sm w-full" value={outboundForm.heartbeat} disabled={readOnly} onChange={(e) => updateOutboundForm({ heartbeat: e.target.value })} />
                    </fieldset>
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.udp_over_stream} disabled={readOnly} onChange={(e) => updateOutboundForm({ udp_over_stream: e.target.checked })} />
                      <span className="label-text">UDP Over Stream</span>
                    </label>
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.zero_rtt_handshake} disabled={readOnly} onChange={(e) => updateOutboundForm({ zero_rtt_handshake: e.target.checked })} />
                      <span className="label-text">0-RTT Handshake</span>
                    </label>
                  </>
                )}

                {outboundForm.type === 'anytls' && (
                  <>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Idle Check Interval</legend>
                      <input className="input input-sm w-full" value={outboundForm.idle_session_check_interval} disabled={readOnly} onChange={(e) => updateOutboundForm({ idle_session_check_interval: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Idle Timeout</legend>
                      <input className="input input-sm w-full" value={outboundForm.idle_session_timeout} disabled={readOnly} onChange={(e) => updateOutboundForm({ idle_session_timeout: e.target.value })} />
                    </fieldset>
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">Min Idle Session</legend>
                      <input type="number" min={0} className="input input-sm w-full" value={outboundForm.min_idle_session || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ min_idle_session: parseInt(e.target.value) || 0 })} />
                    </fieldset>
                  </>
                )}
              </div>

              {supportsTLS(outboundForm.type) && (
                <>
                  <div className="divider my-1 text-xs">TLS</div>
                  <div className="grid md:grid-cols-2 gap-3">
                    <label className="label cursor-pointer justify-start gap-3">
                      <input type="checkbox" className="toggle toggle-sm" checked={outboundForm.tls_enabled} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_enabled: e.target.checked })} />
                      <span className="label-text">启用 TLS</span>
                    </label>
                    {outboundForm.tls_enabled && (
                      <>
                        <fieldset className="fieldset">
                          <legend className="fieldset-legend">SNI</legend>
                          <input className="input input-sm w-full" value={outboundForm.tls_server_name} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_server_name: e.target.value })} />
                        </fieldset>
                        <fieldset className="fieldset">
                          <legend className="fieldset-legend">ALPN</legend>
                          <input className="input input-sm w-full" value={outboundForm.tls_alpn} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_alpn: e.target.value })} placeholder="h2,http/1.1" />
                        </fieldset>
                        <fieldset className="fieldset">
                          <legend className="fieldset-legend">Min Version</legend>
                          <select className="select select-sm w-full" value={outboundForm.tls_min_version} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_min_version: e.target.value })}>
                            {tlsVersionOptions.map(value => <option key={value || 'default'} value={value}>{value || '默认'}</option>)}
                          </select>
                        </fieldset>
                        <fieldset className="fieldset">
                          <legend className="fieldset-legend">Max Version</legend>
                          <select className="select select-sm w-full" value={outboundForm.tls_max_version} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_max_version: e.target.value })}>
                            {tlsVersionOptions.map(value => <option key={value || 'default'} value={value}>{value || '默认'}</option>)}
                          </select>
                        </fieldset>
                        <label className="label cursor-pointer justify-start gap-3">
                          <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.tls_insecure} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_insecure: e.target.checked })} />
                          <span className="label-text">跳过证书校验</span>
                        </label>
                      </>
                    )}
                  </div>
                </>
              )}

              {isV2RayLike(outboundForm.type) && (
                <>
                  <div className="divider my-1 text-xs">传输</div>
                  <div className="grid md:grid-cols-2 gap-3">
                    <fieldset className="fieldset">
                      <legend className="fieldset-legend">传输类型</legend>
                      <select className="select select-sm w-full" value={outboundForm.transport_type} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_type: e.target.value })}>
                        {transportOptions.map(value => <option key={value || 'default'} value={value}>{value || '默认'}</option>)}
                      </select>
                    </fieldset>
                    {outboundForm.transport_type === 'http' && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Method</legend>
                        <input className="input input-sm w-full" value={outboundForm.transport_method} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_method: e.target.value })} />
                      </fieldset>
                    )}
                    {(outboundForm.transport_type === 'http' || outboundForm.transport_type === 'ws' || outboundForm.transport_type === 'httpupgrade') && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Path</legend>
                        <input className="input input-sm w-full" value={outboundForm.transport_path} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_path: e.target.value })} />
                      </fieldset>
                    )}
                    {outboundForm.transport_type === 'grpc' && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Service Name</legend>
                        <input className="input input-sm w-full" value={outboundForm.transport_path} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_path: e.target.value })} />
                      </fieldset>
                    )}
                    {(outboundForm.transport_type === 'http' || outboundForm.transport_type === 'ws' || outboundForm.transport_type === 'httpupgrade') && (
                      <fieldset className="fieldset">
                        <legend className="fieldset-legend">Host</legend>
                        <input className="input input-sm w-full" value={outboundForm.transport_host} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_host: e.target.value })} />
                      </fieldset>
                    )}
                  </div>
                </>
              )}
            </div>
          )}

          {tab === 'json' && (
            <div className="space-y-3">
              <fieldset className="fieldset">
                <legend className="fieldset-legend">URI</legend>
                <div className="join w-full">
                  <input
                    className="input input-sm join-item w-full font-mono text-xs"
                    value={form.uri}
                    disabled={readOnly}
                    onChange={(e) => {
                      setUriError('')
                      onChange({ ...form, uri: e.target.value })
                    }}
                    placeholder="trojan://password@example.com:443?sni=example.com#节点名称"
                  />
                  <button
                    type="button"
                    className="btn btn-sm join-item"
                    disabled={readOnly || uriParsing || !form.uri.trim()}
                    onClick={handleParseURI}
                  >
                    {uriParsing ? <span className="loading loading-spinner loading-xs"></span> : '解析'}
                  </button>
                </div>
              </fieldset>
              <textarea
                className="textarea textarea-bordered w-full min-h-[380px] font-mono text-xs"
                value={jsonPreview}
                disabled={readOnly}
                onChange={(e) => handleJSONChange(e.target.value)}
                placeholder={'{\n  "type": "socks",\n  "tag": "node",\n  "server": "127.0.0.1",\n  "server_port": 1080\n}'}
              />
            </div>
          )}

          {tab === 'inbound' && (
            <div className="grid md:grid-cols-2 gap-3">
              <fieldset className="fieldset">
                <legend className="fieldset-legend">本地代理端口</legend>
                <input type="number" className="input input-sm w-full" min={0} max={65535} value={form.port || ''} disabled={readOnly} onChange={(e) => onChange({ ...form, port: parseInt(e.target.value) || 0 })} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">本地协议</legend>
                <select className="select select-sm w-full" value={form.inbound_protocol || ''} disabled={readOnly} onChange={(e) => onChange({ ...form, inbound_protocol: e.target.value })}>
                  <option value="">使用系统默认</option>
                  <option value="mixed">mixed</option>
                  <option value="http">http</option>
                  <option value="socks5">socks5</option>
                </select>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">本地用户名</legend>
                <input className="input input-sm w-full" value={form.username} disabled={readOnly} onChange={(e) => onChange({ ...form, username: e.target.value })} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">本地密码</legend>
                <input className="input input-sm w-full" value={form.password} disabled={readOnly} onChange={(e) => onChange({ ...form, password: e.target.value })} />
              </fieldset>
            </div>
          )}

          <div className="modal-action">
            <button type="button" className="btn btn-ghost" onClick={onClose}>{readOnly ? '关闭' : '取消'}</button>
            {!readOnly && (
              <button type="submit" className="btn btn-primary" disabled={submitting || uriParsing || !!jsonError}>
                {submitting ? <span className="loading loading-spinner loading-xs"></span> : (editingName ? '更新' : '添加')}
              </button>
            )}
          </div>
        </form>
      </div>
      <form method="dialog" className="modal-backdrop" onClick={() => !submitting && onClose()}>
        <button>close</button>
      </form>
    </div>
  )
}
