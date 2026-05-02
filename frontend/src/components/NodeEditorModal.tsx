import { useEffect, useMemo, useState } from 'react'
import type { ConfigNodePayload } from '../types'

type EditorTab = 'form' | 'json' | 'inbound'

type OutboundForm = {
  type: string
  server: string
  server_port: number
  uuid: string
  password: string
  username: string
  method: string
  security: string
  version: string
  network: string
  tls_enabled: boolean
  tls_server_name: string
  tls_insecure: boolean
  transport_type: string
  transport_path: string
  transport_host: string
  flow: string
  packet_encoding: string
  obfs_type: string
  obfs_password: string
  up_mbps: number
  down_mbps: number
  congestion_control: string
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

const defaultOutboundForm: OutboundForm = {
  type: 'socks',
  server: '',
  server_port: 0,
  uuid: '',
  password: '',
  username: '',
  method: '2022-blake3-aes-128-gcm',
  security: 'auto',
  version: '5',
  network: '',
  tls_enabled: false,
  tls_server_name: '',
  tls_insecure: false,
  transport_type: '',
  transport_path: '',
  transport_host: '',
  flow: '',
  packet_encoding: '',
  obfs_type: '',
  obfs_password: '',
  up_mbps: 0,
  down_mbps: 0,
  congestion_control: '',
}

function compactObject<T extends Record<string, unknown>>(obj: T): T {
  const next: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(obj)) {
    if (value === '' || value === 0 || value === false || value == null) continue
    if (Array.isArray(value) && value.length === 0) continue
    next[key] = value
  }
  return next as T
}

function buildTransport(form: OutboundForm) {
  if (!form.transport_type) return undefined
  const base: Record<string, unknown> = { type: form.transport_type }
  if (form.transport_path) base.path = form.transport_path
  if (form.transport_type === 'grpc' && form.transport_path) {
    delete base.path
    base.service_name = form.transport_path
  }
  if (form.transport_host) {
    if (form.transport_type === 'http') base.host = [form.transport_host]
    else if (form.transport_type === 'httpupgrade') base.host = form.transport_host
    else base.headers = { Host: [form.transport_host] }
  }
  return compactObject(base)
}

function buildOutboundJSON(form: OutboundForm, tag: string): string {
  const outbound: Record<string, unknown> = {
    type: form.type,
    tag: tag || 'node',
    server: form.server,
    server_port: form.server_port,
  }

  switch (form.type) {
    case 'socks':
      outbound.version = form.version || '5'
      outbound.username = form.username
      outbound.password = form.password
      break
    case 'http':
      outbound.username = form.username
      outbound.password = form.password
      break
    case 'shadowsocks':
      outbound.method = form.method
      outbound.password = form.password
      break
    case 'vmess':
      outbound.uuid = form.uuid
      outbound.security = form.security || 'auto'
      outbound.packet_encoding = form.packet_encoding
      break
    case 'vless':
      outbound.uuid = form.uuid
      outbound.flow = form.flow
      outbound.packet_encoding = form.packet_encoding
      break
    case 'trojan':
    case 'hysteria2':
    case 'anytls':
      outbound.password = form.password
      break
    case 'tuic':
      outbound.uuid = form.uuid
      outbound.password = form.password
      outbound.congestion_control = form.congestion_control
      break
  }

  if (form.network) outbound.network = form.network
  if (form.tls_enabled) {
    outbound.tls = compactObject({
      enabled: true,
      server_name: form.tls_server_name,
      insecure: form.tls_insecure,
    })
  }
  const transport = buildTransport(form)
  if (transport) outbound.transport = transport
  if (form.type === 'hysteria2') {
    if (form.obfs_type) outbound.obfs = compactObject({ type: form.obfs_type, password: form.obfs_password })
    if (form.up_mbps > 0) outbound.up_mbps = form.up_mbps
    if (form.down_mbps > 0) outbound.down_mbps = form.down_mbps
  }

  return JSON.stringify(compactObject(outbound), null, 2)
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
      uuid: obj.uuid || '',
      password: obj.password || '',
      username: obj.username || '',
      method: obj.method || defaultOutboundForm.method,
      security: obj.security || defaultOutboundForm.security,
      version: obj.version || defaultOutboundForm.version,
      network: obj.network || '',
      tls_enabled: !!obj.tls?.enabled,
      tls_server_name: obj.tls?.server_name || '',
      tls_insecure: !!obj.tls?.insecure,
      transport_type: transport.type || '',
      transport_path: transport.path || transport.service_name || '',
      transport_host: Array.isArray(transportHost) ? transportHost[0] : transportHost,
      flow: obj.flow || '',
      packet_encoding: obj.packet_encoding || '',
      obfs_type: obj.obfs?.type || '',
      obfs_password: obj.obfs?.password || '',
      up_mbps: Number(obj.up_mbps || 0),
      down_mbps: Number(obj.down_mbps || 0),
      congestion_control: obj.congestion_control || '',
    }
  } catch {
    return null
  }
}

function isV2RayLike(type: string) {
  return type === 'vmess' || type === 'vless' || type === 'trojan'
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
  const [outboundForm, setOutboundForm] = useState<OutboundForm>(defaultOutboundForm)

  useEffect(() => {
    if (!open) return
    setTab('form')
    setJsonError('')
    setOutboundForm(parseOutboundForm(form.outbound_json || '') || defaultOutboundForm)
  }, [open, editingName])

  const jsonPreview = useMemo(() => form.outbound_json || '', [form.outbound_json])

  if (!open) return null

  const updateOutboundForm = (patch: Partial<OutboundForm>) => {
    const next = { ...outboundForm, ...patch }
    setOutboundForm(next)
    onChange({ ...form, outbound_json: buildOutboundJSON(next, form.name) })
  }

  const handleJSONChange = (value: string) => {
    onChange({ ...form, outbound_json: value })
    try {
      const parsed = JSON.parse(value)
      setJsonError(parsed?.type ? '' : 'JSON 缺少 type 字段')
      const nextForm = parseOutboundForm(value)
      if (nextForm) setOutboundForm(nextForm)
    } catch {
      setJsonError('JSON 格式无效')
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

          <div role="tablist" className="tabs tabs-boxed mb-4 bg-base-200/70">
            <button type="button" role="tab" className={`tab ${tab === 'form' ? 'tab-active' : ''}`} onClick={() => setTab('form')}>Form</button>
            <button type="button" role="tab" className={`tab ${tab === 'json' ? 'tab-active' : ''}`} onClick={() => setTab('json')}>JSON</button>
            <button type="button" role="tab" className={`tab ${tab === 'inbound' ? 'tab-active' : ''}`} onClick={() => setTab('inbound')}>入站</button>
          </div>

          {tab === 'form' && (
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
              {(outboundForm.type === 'vmess' || outboundForm.type === 'vless' || outboundForm.type === 'tuic') && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">UUID</legend>
                  <input className="input input-sm w-full font-mono text-xs" value={outboundForm.uuid} disabled={readOnly} onChange={(e) => updateOutboundForm({ uuid: e.target.value })} />
                </fieldset>
              )}
              {(outboundForm.type === 'socks' || outboundForm.type === 'http') && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">上游用户名</legend>
                  <input className="input input-sm w-full" value={outboundForm.username} disabled={readOnly} onChange={(e) => updateOutboundForm({ username: e.target.value })} />
                </fieldset>
              )}
              {outboundForm.type === 'shadowsocks' && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">加密方法</legend>
                  <input className="input input-sm w-full" value={outboundForm.method} disabled={readOnly} onChange={(e) => updateOutboundForm({ method: e.target.value })} />
                </fieldset>
              )}
              {outboundForm.type === 'vmess' && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Security</legend>
                  <input className="input input-sm w-full" value={outboundForm.security} disabled={readOnly} onChange={(e) => updateOutboundForm({ security: e.target.value })} />
                </fieldset>
              )}
              {outboundForm.type === 'vless' && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Flow</legend>
                  <input className="input input-sm w-full" value={outboundForm.flow} disabled={readOnly} onChange={(e) => updateOutboundForm({ flow: e.target.value })} />
                </fieldset>
              )}
              {outboundForm.type !== 'vless' && outboundForm.type !== 'vmess' && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">上游密码</legend>
                  <input className="input input-sm w-full" value={outboundForm.password} disabled={readOnly} onChange={(e) => updateOutboundForm({ password: e.target.value })} />
                </fieldset>
              )}
              {(outboundForm.type === 'vmess' || outboundForm.type === 'vless') && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Packet Encoding</legend>
                  <input className="input input-sm w-full" value={outboundForm.packet_encoding} disabled={readOnly} onChange={(e) => updateOutboundForm({ packet_encoding: e.target.value })} />
                </fieldset>
              )}
              <fieldset className="fieldset">
                <legend className="fieldset-legend">Network</legend>
                <select className="select select-sm w-full" value={outboundForm.network} disabled={readOnly} onChange={(e) => updateOutboundForm({ network: e.target.value })}>
                  <option value="">默认</option>
                  <option value="tcp">tcp</option>
                  <option value="udp">udp</option>
                </select>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">TLS</legend>
                <label className="label cursor-pointer justify-start gap-3">
                  <input type="checkbox" className="toggle toggle-sm" checked={outboundForm.tls_enabled} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_enabled: e.target.checked })} />
                  <span className="label-text">启用</span>
                </label>
              </fieldset>
              {outboundForm.tls_enabled && (
                <>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">SNI</legend>
                    <input className="input input-sm w-full" value={outboundForm.tls_server_name} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_server_name: e.target.value })} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">跳过证书校验</legend>
                    <input type="checkbox" className="checkbox checkbox-sm" checked={outboundForm.tls_insecure} disabled={readOnly} onChange={(e) => updateOutboundForm({ tls_insecure: e.target.checked })} />
                  </fieldset>
                </>
              )}
              {isV2RayLike(outboundForm.type) && (
                <>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">传输类型</legend>
                    <select className="select select-sm w-full" value={outboundForm.transport_type} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_type: e.target.value })}>
                      <option value="">tcp</option>
                      <option value="ws">ws</option>
                      <option value="http">http</option>
                      <option value="grpc">grpc</option>
                      <option value="httpupgrade">httpupgrade</option>
                    </select>
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Path / Service</legend>
                    <input className="input input-sm w-full" value={outboundForm.transport_path} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_path: e.target.value })} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Host</legend>
                    <input className="input input-sm w-full" value={outboundForm.transport_host} disabled={readOnly} onChange={(e) => updateOutboundForm({ transport_host: e.target.value })} />
                  </fieldset>
                </>
              )}
              {outboundForm.type === 'hysteria2' && (
                <>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Obfs</legend>
                    <input className="input input-sm w-full" value={outboundForm.obfs_type} disabled={readOnly} onChange={(e) => updateOutboundForm({ obfs_type: e.target.value })} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Obfs Password</legend>
                    <input className="input input-sm w-full" value={outboundForm.obfs_password} disabled={readOnly} onChange={(e) => updateOutboundForm({ obfs_password: e.target.value })} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Up Mbps</legend>
                    <input type="number" className="input input-sm w-full" value={outboundForm.up_mbps || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ up_mbps: parseInt(e.target.value) || 0 })} />
                  </fieldset>
                  <fieldset className="fieldset">
                    <legend className="fieldset-legend">Down Mbps</legend>
                    <input type="number" className="input input-sm w-full" value={outboundForm.down_mbps || ''} disabled={readOnly} onChange={(e) => updateOutboundForm({ down_mbps: parseInt(e.target.value) || 0 })} />
                  </fieldset>
                </>
              )}
              {outboundForm.type === 'tuic' && (
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">Congestion Control</legend>
                  <input className="input input-sm w-full" value={outboundForm.congestion_control} disabled={readOnly} onChange={(e) => updateOutboundForm({ congestion_control: e.target.value })} />
                </fieldset>
              )}
              <fieldset className="fieldset md:col-span-2">
                <legend className="fieldset-legend">URI</legend>
                <input className="input input-sm w-full font-mono text-xs" value={form.uri} disabled={readOnly} onChange={(e) => onChange({ ...form, uri: e.target.value })} />
              </fieldset>
            </div>
          )}

          {tab === 'json' && (
            <textarea
              className="textarea textarea-bordered w-full min-h-[420px] font-mono text-xs"
              value={jsonPreview}
              disabled={readOnly}
              onChange={(e) => handleJSONChange(e.target.value)}
              placeholder={'{\n  "type": "socks",\n  "tag": "node",\n  "server": "127.0.0.1",\n  "server_port": 1080\n}'}
            />
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
              <button type="submit" className="btn btn-primary" disabled={submitting || !!jsonError}>
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
