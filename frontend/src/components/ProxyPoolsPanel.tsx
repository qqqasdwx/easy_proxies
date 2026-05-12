import { useEffect, useMemo, useState } from 'react'
import type { ConfigNodeConfig, ProxyPool, ProxyPoolPayload, SettingsData } from '../types'
import {
  createProxyPool,
  deleteProxyPool,
  fetchConfigNodes,
  fetchProxyPools,
  fetchSettings,
  refreshGeoIPDatabase,
  triggerReload,
  updateProxyPool,
  updateSettings,
} from '../api/client'

const emptyPool: ProxyPoolPayload = {
  name: '新代理池',
  enabled: true,
  listen_address: '0.0.0.0',
  listen_port: 2323,
  protocol: 'mixed',
  username: '',
  password: '',
  mode: 'sequential',
  failure_threshold: 3,
  blacklist_duration: '24h0m0s',
  all_nodes: true,
  node_ids: [],
}

const inputClass = 'input input-md w-full bg-base-200/60 focus:bg-base-100 border-base-300/70 focus:border-primary/60'
const selectClass = 'select select-md w-full bg-base-200/60 focus:bg-base-100 border-base-300/70 focus:border-primary/60'

function poolToPayload(pool: ProxyPool): ProxyPoolPayload {
  return {
    name: pool.name,
    enabled: pool.enabled,
    listen_address: pool.listen_address,
    listen_port: pool.listen_port,
    protocol: pool.protocol,
    username: pool.username || '',
    password: pool.password || '',
    mode: pool.mode,
    failure_threshold: pool.failure_threshold,
    blacklist_duration: pool.blacklist_duration,
    all_nodes: pool.all_nodes,
    node_ids: pool.node_ids || [],
  }
}

function formatUpdatedAt(value: string) {
  if (!value) return '未下载'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

export default function ProxyPoolsPanel() {
  const [pools, setPools] = useState<ProxyPool[]>([])
  const [nodes, setNodes] = useState<ConfigNodeConfig[]>([])
  const [settings, setSettings] = useState<SettingsData | null>(null)
  const [editingID, setEditingID] = useState<number | null>(null)
  const [form, setForm] = useState<ProxyPoolPayload>(emptyPool)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [savingGeoIP, setSavingGeoIP] = useState(false)
  const [geoIPDirty, setGeoIPDirty] = useState(false)
  const [refreshingGeoIP, setRefreshingGeoIP] = useState(false)
  const [needReload, setNeedReload] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')

  const activeNodes = useMemo(() => nodes.filter(node => !node.disabled && node.id), [nodes])
  const selectedNodeIDs = useMemo(() => new Set(form.node_ids), [form.node_ids])

  const load = async () => {
    setError('')
    try {
      const [poolRes, nodeRes, settingsRes] = await Promise.all([
        fetchProxyPools(),
        fetchConfigNodes(),
        fetchSettings(),
      ])
      setPools(poolRes.proxy_pools || [])
      setNodes(nodeRes.nodes || [])
      setSettings(settingsRes)
      setGeoIPDirty(false)
      if (editingID === null && (poolRes.proxy_pools || []).length > 0) {
        const first = poolRes.proxy_pools[0]
        setEditingID(first.id)
        setForm(poolToPayload(first))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载代理池失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (!success) return
    const timer = setTimeout(() => setSuccess(''), 5000)
    return () => clearTimeout(timer)
  }, [success])

  const updateField = <K extends keyof ProxyPoolPayload>(key: K, value: ProxyPoolPayload[K]) => {
    setForm(prev => ({ ...prev, [key]: value }))
  }

  const selectPool = (pool: ProxyPool) => {
    setEditingID(pool.id)
    setForm(poolToPayload(pool))
    setError('')
  }

  const collectUsedPorts = (excludePoolID?: number) => {
    const used = new Set<number>()
    for (const pool of pools) {
      if (pool.id !== excludePoolID && pool.listen_port > 0) used.add(pool.listen_port)
    }
    for (const node of nodes) {
      const port = node.port || 0
      if (port > 0) used.add(port)
    }
    if (settings?.geoip_enabled && (settings.geoip_port || 0) > 0) used.add(settings.geoip_port)
    return used
  }

  const listenerPortConflict = (port: number, excludePoolID?: number) => {
    const pool = pools.find(item => item.id !== excludePoolID && item.listen_port === port)
    if (pool) return `监听端口 ${port} 已被代理池「${pool.name}」使用`
    const node = nodes.find(item => item.port === port)
    if (node) return `监听端口 ${port} 已被节点「${node.name}」使用`
    if (settings?.geoip_enabled && settings.geoip_port === port) return `监听端口 ${port} 已被 GeoIP 路由使用`
    return ''
  }

  const createDraft = () => {
    const usedPorts = collectUsedPorts()
    let nextPort = 2323
    while (usedPorts.has(nextPort)) nextPort++
    setEditingID(null)
    setForm({ ...emptyPool, listen_port: nextPort, name: `代理池 ${pools.length + 1}` })
    setError('')
  }

  const toggleNode = (nodeID: number) => {
    setForm(prev => {
      const next = new Set(prev.node_ids)
      if (next.has(nodeID)) next.delete(nodeID)
      else next.add(nodeID)
      return { ...prev, node_ids: Array.from(next).sort((a, b) => a - b) }
    })
  }

  const savePool = async () => {
    if (!form.name.trim()) { setError('代理池名称不能为空'); return }
    if (form.listen_port < 1 || form.listen_port > 65535) { setError('监听端口必须在 1-65535 之间'); return }
    const conflict = listenerPortConflict(form.listen_port, editingID ?? undefined)
    if (conflict) { setError(conflict); return }
    if (!form.all_nodes && form.node_ids.length === 0) { setError('请选择节点或启用全部节点'); return }
    setSaving(true)
    setError('')
    setSuccess('')
    try {
      const payload = { ...form, name: form.name.trim() }
      const res = editingID
        ? await updateProxyPool(editingID, payload)
        : await createProxyPool(payload)
      setSuccess(res.message || '代理池已保存')
      setNeedReload(true)
      await load()
      if (res.proxy_pool) {
        setEditingID(res.proxy_pool.id)
        setForm(poolToPayload(res.proxy_pool))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存代理池失败')
    } finally {
      setSaving(false)
    }
  }

  const removePool = async (pool: ProxyPool) => {
    if (!confirm(`删除代理池「${pool.name}」？`)) return
    setError('')
    try {
      const res = await deleteProxyPool(pool.id)
      setSuccess(res.message || '代理池已删除')
      setNeedReload(true)
      setEditingID(null)
      setForm(emptyPool)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除代理池失败')
    }
  }

  const updateGeoIPField = <K extends keyof SettingsData>(key: K, value: SettingsData[K]) => {
    setSettings(prev => prev ? { ...prev, [key]: value } : prev)
    setGeoIPDirty(true)
  }

  const saveGeoIPSettings = async () => {
    if (!settings) return
    setSavingGeoIP(true)
    setError('')
    setSuccess('')
    try {
      const res = await updateSettings(settings)
      setSuccess(res.message || 'GeoIP 设置已保存')
      setNeedReload(res.need_reload ?? true)
      setGeoIPDirty(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存 GeoIP 设置失败')
    } finally {
      setSavingGeoIP(false)
    }
  }

  const refreshGeoIP = async () => {
    setRefreshingGeoIP(true)
    setError('')
    try {
      const res = await refreshGeoIPDatabase()
      setSettings(prev => prev ? {
        ...prev,
        geoip_database_path: res.path || prev.geoip_database_path,
        geoip_database_updated_at: res.database_updated_at || prev.geoip_database_updated_at,
      } : prev)
      setSuccess(res.message || 'GeoIP 数据库已重新下载')
      if (res.need_reload) setNeedReload(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '刷新 GeoIP 数据库失败')
    } finally {
      setRefreshingGeoIP(false)
    }
  }

  const reload = async () => {
    try {
      const res = await triggerReload()
      setSuccess(res.message || '重载成功')
      setNeedReload(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '重载失败')
    }
  }

  if (loading) {
    return <div className="flex items-center justify-center h-64"><span className="loading loading-spinner loading-lg text-primary"></span></div>
  }

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      <div className="sticky top-0 z-30 bg-base-100/90 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 max-w-[1600px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <span className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center text-primary border border-primary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M4 7h16M4 12h16M4 17h16" />
                </svg>
              </span>
              代理池管理
            </h2>
            <p className="text-sm text-base-content/50 mt-1.5 ml-[3.25rem]">独立配置监听、调度和成员节点</p>
          </div>
          <div className="flex items-center gap-2">
            <button className="btn btn-sm lg:btn-md btn-primary" onClick={createDraft}>添加代理池</button>
            {needReload && <button className="btn btn-warning btn-sm lg:btn-md" onClick={reload}>重载配置</button>}
          </div>
        </div>
      </div>

      <div className="p-4 lg:p-8 max-w-[1600px] mx-auto w-full flex-1 space-y-5">
        {error && <div role="alert" className="alert alert-error alert-soft text-sm"><span>{error}</span></div>}
        {success && <div role="alert" className="alert alert-success alert-soft text-sm"><span>{success}</span></div>}
        {needReload && <div role="alert" className="alert alert-warning alert-soft text-sm"><span>代理入口配置已变化，请重载后生效。</span></div>}

        <section className="border border-base-300/60 rounded-lg bg-base-100 p-4 md:p-5">
          <div className="flex items-center justify-between gap-4 mb-4">
            <div>
              <h3 className="font-bold text-lg">GeoIP</h3>
              <p className="text-sm text-base-content/50 mt-1">启用后，所有代理池都会生成地域路由入口。</p>
            </div>
            <div className="flex items-center gap-3">
              <button className="btn btn-sm btn-primary" onClick={saveGeoIPSettings} disabled={!settings || !geoIPDirty || savingGeoIP}>
                {savingGeoIP ? <span className="loading loading-spinner loading-xs"></span> : '保存'}
              </button>
              <input
                type="checkbox"
                className="toggle toggle-primary"
                checked={settings?.geoip_enabled || false}
                onChange={e => updateGeoIPField('geoip_enabled', e.target.checked)}
              />
            </div>
          </div>
          {settings?.geoip_enabled && (
            <div className="grid grid-cols-1 xl:grid-cols-[minmax(0,1fr)_13rem_9rem_12rem] gap-4 items-end">
              <fieldset className="fieldset">
                <legend className="fieldset-legend">数据库文件</legend>
                <div className="min-h-12 rounded-lg border border-base-300/70 bg-base-200/40 px-3 py-2">
                  <div className="font-mono text-sm break-all">{settings.geoip_database_path || '-'}</div>
                  <div className="text-xs text-base-content/50 mt-1">最近更新时间：{formatUpdatedAt(settings.geoip_database_updated_at)}</div>
                </div>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">路由监听地址</legend>
                <input
                  className={inputClass}
                  value={settings.geoip_listen}
                  placeholder="0.0.0.0"
                  onChange={e => updateGeoIPField('geoip_listen', e.target.value)}
                />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">路由端口</legend>
                <input
                  type="number"
                  className={inputClass}
                  min={1}
                  max={65535}
                  value={settings.geoip_port}
                  onChange={e => updateGeoIPField('geoip_port', parseInt(e.target.value) || 1221)}
                />
              </fieldset>
              <button className="btn btn-ghost border border-base-300" onClick={refreshGeoIP} disabled={refreshingGeoIP}>
                {refreshingGeoIP ? <span className="loading loading-spinner loading-sm"></span> : '刷新数据库'}
              </button>
              <label className="flex items-center justify-between gap-3 rounded-lg border border-base-300/70 px-3 py-2">
                <span className="font-semibold text-sm">自动更新</span>
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-sm"
                  checked={settings.geoip_auto_update_enabled}
                  onChange={e => updateGeoIPField('geoip_auto_update_enabled', e.target.checked)}
                />
              </label>
              {settings.geoip_auto_update_enabled && (
                <fieldset className="fieldset xl:col-span-2">
                  <legend className="fieldset-legend">更新间隔</legend>
                  <input className={inputClass} value={settings.geoip_auto_update_interval} onChange={e => updateGeoIPField('geoip_auto_update_interval', e.target.value)} />
                </fieldset>
              )}
            </div>
          )}
        </section>

        <div className="grid grid-cols-1 xl:grid-cols-[320px_1fr] gap-5">
          <aside className="border border-base-300/60 rounded-lg bg-base-100 overflow-hidden">
            <div className="px-4 py-3 border-b border-base-300/60 font-bold">代理池</div>
            {pools.length === 0 ? (
              <div className="p-4 text-sm text-base-content/50">暂无代理池</div>
            ) : pools.map(pool => (
              <button
                key={pool.id}
                className={`w-full text-left px-4 py-3 border-b border-base-300/40 last:border-b-0 hover:bg-base-200/70 ${editingID === pool.id ? 'bg-primary/10 text-primary' : ''}`}
                onClick={() => selectPool(pool)}
              >
                <div className="flex items-center gap-2">
                  <span className="font-bold truncate">{pool.name}</span>
                  {!pool.enabled && <span className="badge badge-ghost badge-xs">停用</span>}
                </div>
                <div className="text-xs opacity-60 mt-1 font-mono">{pool.protocol} {pool.listen_address}:{pool.listen_port}</div>
              </button>
            ))}
          </aside>

          <main className="border border-base-300/60 rounded-lg bg-base-100 px-4 md:px-6 py-5 min-w-0">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <fieldset className="fieldset">
                <legend className="fieldset-legend">名称</legend>
                <input className={inputClass} value={form.name} onChange={e => updateField('name', e.target.value)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">状态</legend>
                <label className="flex h-12 items-center justify-between rounded-lg border border-base-300/70 px-3">
                  <span className="font-semibold">{form.enabled ? '启用' : '停用'}</span>
                  <input type="checkbox" className="toggle toggle-primary" checked={form.enabled} onChange={e => updateField('enabled', e.target.checked)} />
                </label>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">监听地址</legend>
                <input className={inputClass} value={form.listen_address} onChange={e => updateField('listen_address', e.target.value)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">监听端口</legend>
                <input type="number" className={inputClass} min={1} max={65535} value={form.listen_port} onChange={e => updateField('listen_port', parseInt(e.target.value) || 0)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">监听协议</legend>
                <select className={selectClass} value={form.protocol} onChange={e => updateField('protocol', e.target.value)}>
                  <option value="mixed">mixed (HTTP + SOCKS5)</option>
                  <option value="http">http</option>
                  <option value="socks5">socks5</option>
                </select>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">调度模式</legend>
                <select className={selectClass} value={form.mode} onChange={e => updateField('mode', e.target.value)}>
                  <option value="sequential">sequential - 顺序轮询</option>
                  <option value="random">random - 随机</option>
                  <option value="balance">balance - 最小连接数</option>
                </select>
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">用户名</legend>
                <input className={inputClass} placeholder="可选" value={form.username} onChange={e => updateField('username', e.target.value)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">密码</legend>
                <input className={inputClass} placeholder="可选" value={form.password} onChange={e => updateField('password', e.target.value)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">失败阈值</legend>
                <input type="number" className={inputClass} min={1} value={form.failure_threshold} onChange={e => updateField('failure_threshold', parseInt(e.target.value) || 1)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">黑名单持续时间</legend>
                <input className={inputClass} value={form.blacklist_duration} onChange={e => updateField('blacklist_duration', e.target.value)} />
              </fieldset>
            </div>

            <section className="mt-6 border-t border-base-300/60 pt-5">
              <label className="flex items-center justify-between gap-4 rounded-lg border border-base-300/70 px-4 py-3">
                <div>
                  <span className="font-bold block">使用全部启用节点</span>
                  <span className="text-xs text-base-content/55">订阅刷新新增的节点会自动加入该代理池。</span>
                </div>
                <input type="checkbox" className="toggle toggle-primary" checked={form.all_nodes} onChange={e => updateField('all_nodes', e.target.checked)} />
              </label>

              {!form.all_nodes && (
                <div className="mt-4 max-h-72 overflow-auto rounded-lg border border-base-300/70 divide-y divide-base-300/50">
                  {activeNodes.length === 0 ? (
                    <div className="p-4 text-sm text-base-content/50">暂无可选节点</div>
                  ) : activeNodes.map(node => (
                    <label key={node.id} className="flex items-center gap-3 px-4 py-3 hover:bg-base-200/60 cursor-pointer">
                      <input
                        type="checkbox"
                        className="checkbox checkbox-sm"
                        checked={selectedNodeIDs.has(node.id!)}
                        onChange={() => toggleNode(node.id!)}
                      />
                      <span className="font-medium truncate">{node.name}</span>
                      <span className="badge badge-ghost badge-xs ml-auto">{node.source === 'subscription' ? '订阅' : '手动'}</span>
                    </label>
                  ))}
                </div>
              )}
            </section>

            <div className="mt-6 flex items-center justify-end gap-2">
              {editingID && pools.length > 1 && (
                <button className="btn btn-ghost text-error" onClick={() => {
                  const pool = pools.find(item => item.id === editingID)
                  if (pool) removePool(pool)
                }}>
                  删除
                </button>
              )}
              <button className="btn btn-primary" onClick={savePool} disabled={saving}>
                {saving ? <span className="loading loading-spinner loading-sm"></span> : '保存代理池'}
              </button>
            </div>
          </main>
        </div>
      </div>
    </div>
  )
}
