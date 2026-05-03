import { useState, useEffect } from 'react'
import type { SettingsData } from '../types'
import { fetchSettings, updateSettings, triggerReload, refreshGeoIPDatabase } from '../api/client'

const defaultSettings: SettingsData = {
  mode: 'pool',
  log_level: 'info',
  external_ip: '',
  skip_cert_verify: false,

  log_output: 'stdout',
  log_file: 'logs/easy_proxies.log',
  log_max_size: 50,
  log_max_backups: 3,
  log_max_age: 7,
  log_compress: false,

  listener_address: '0.0.0.0',
  listener_port: 2323,
  listener_protocol: 'mixed',
  listener_username: '',
  listener_password: '',

  multi_port_address: '0.0.0.0',
  multi_port_base_port: 24000,
  multi_port_protocol: 'mixed',
  multi_port_username: '',
  multi_port_password: '',

  pool_mode: 'sequential',
  pool_failure_threshold: 3,
  pool_blacklist_duration: '24h0m0s',

  dns_enabled: false,
  dns_server: '223.5.5.5',
  dns_fallback_servers: ['8.8.8.8', '1.1.1.1'],
  dns_port: 53,
  dns_strategy: 'prefer_ipv4',

  management_enabled: true,
  management_listen: '0.0.0.0:9091',
  management_probe_target: '',

  geoip_enabled: false,
  geoip_database_path: '',
  geoip_database_updated_at: '',
  geoip_listen: '',
  geoip_port: 1221,
  geoip_auto_update_enabled: false,
  geoip_auto_update_interval: '24h0m0s',

  health_check_interval: '5m0s',
  health_check_timeout: '10s',
  health_check_concurrency: 8,

}

export default function SettingsPanel() {
  const [settings, setSettings] = useState<SettingsData>(defaultSettings)
  const [savedSettings, setSavedSettings] = useState<SettingsData>(defaultSettings)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [reloading, setReloading] = useState(false)
  const [refreshingGeoIP, setRefreshingGeoIP] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [needReload, setNeedReload] = useState(false)
  const [isDirty, setIsDirty] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const settingsData = await fetchSettings()
        const merged = { ...defaultSettings, ...settingsData }
        setSettings(merged)
        setSavedSettings(merged)
        setIsDirty(false)
      } catch (err) {
        setError(err instanceof Error ? err.message : '加载设置失败')
      } finally {
        setLoading(false)
      }
    }
    load()
  }, [])

  useEffect(() => {
    if (success) {
      const timer = setTimeout(() => setSuccess(''), 5000)
      return () => clearTimeout(timer)
    }
  }, [success])

  const handleSave = async () => {
    setSaving(true)
    setError('')
    setSuccess('')
    try {
      const res = await updateSettings(settings)
      setSuccess(res.message || '设置已保存')
      setSavedSettings({ ...settings })
      setIsDirty(false)
      if (res.need_reload) setNeedReload(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const handleReload = async () => {
    setReloading(true)
    setError('')
    try {
      const res = await triggerReload()
      setSuccess(res.message || '重载成功')
      setNeedReload(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '重载失败')
    } finally {
      setReloading(false)
    }
  }

  const handleRefreshGeoIP = async () => {
    setRefreshingGeoIP(true)
    setError('')
    setSuccess('')
    try {
      const res = await refreshGeoIPDatabase()
      setSettings(s => ({
        ...s,
        geoip_database_path: res.path || s.geoip_database_path,
        geoip_database_updated_at: res.database_updated_at || s.geoip_database_updated_at,
      }))
      setSuccess(res.message || 'GeoIP 数据库已重新下载')
      if (res.need_reload) setNeedReload(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'GeoIP 数据库刷新失败')
    } finally {
      setRefreshingGeoIP(false)
    }
  }

  const updateField = <K extends keyof SettingsData>(key: K, value: SettingsData[K]) => {
    setSettings(s => {
      const updated = { ...s, [key]: value }
      setIsDirty(JSON.stringify(updated) !== JSON.stringify(savedSettings))
      return updated
    })
  }

  // Warn before leaving with unsaved changes
  useEffect(() => {
    const handleBeforeUnload = (e: BeforeUnloadEvent) => {
      if (isDirty) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [isDirty])

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg text-primary"></span>
      </div>
    )
  }

  const showPoolConfig = settings.mode === 'pool' || settings.mode === 'hybrid'
  const showMultiPortConfig = settings.mode === 'multi-port' || settings.mode === 'hybrid'
  const formatGeoIPUpdatedAt = (value: string) => {
    if (!value) return '未下载'
    const date = new Date(value)
    if (Number.isNaN(date.getTime())) return value
    return date.toLocaleString()
  }
  const updateFallbackServers = (value: string) => {
    updateField(
      'dns_fallback_servers',
      value
        .split(/[\n,]+/)
        .map(item => item.trim())
        .filter(Boolean)
    )
  }

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      {/* Header - sticky */}
      <div className="sticky top-0 z-30 bg-base-100/80 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 max-w-[1200px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center text-primary shrink-0 border border-primary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                </svg>
              </div>
              系统设置
            </h2>
            <p className="text-sm text-base-content/50 mt-1.5 ml-[3.25rem]">管理系统所有配置项，修改后需保存生效</p>
          </div>
          <div className="flex gap-2">
            <button
              className={`btn btn-sm lg:btn-md gap-2 shadow-sm ${isDirty ? 'btn-primary' : 'btn-ghost border border-base-300'}`}
              onClick={handleSave}
              disabled={saving || !isDirty}
            >
              {saving ? <span className="loading loading-spinner loading-sm"></span> : isDirty ? (
                <>
                  <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8 7H5a2 2 0 00-2 2v9a2 2 0 002 2h14a2 2 0 002-2V9a2 2 0 00-2-2h-3m-1 4l-3 3m0 0l-3-3m3 3V4" />
                  </svg>
                  保存设置
                </>
              ) : '✅ 已保存'}
            </button>
            {needReload && (
              <button
                className="btn btn-warning btn-sm lg:btn-md gap-2 shadow-sm animate-pulse"
                onClick={handleReload}
                disabled={reloading}
              >
                {reloading ? <span className="loading loading-spinner loading-sm"></span> : (
                  <>
                    <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                    </svg>
                    重载配置
                  </>
                )}
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="p-4 lg:p-8 space-y-6 max-w-[1200px] mx-auto w-full pb-10 flex-1">
        {/* Alerts */}
        {error && (
        <div role="alert" className="alert alert-error alert-soft text-sm">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{error}</span>
        </div>
      )}
      {success && (
        <div role="alert" className="alert alert-success alert-soft text-sm">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <span>{success}</span>
        </div>
      )}
      {needReload && (
        <div role="alert" className="alert alert-warning alert-soft text-sm">
          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z" />
          </svg>
          <div>
            <span>配置已保存。</span>
            <span className="font-medium">WebUI 密码、探测目标、外部 IP、SSL 验证</span>
            <span>已立即生效；其他配置（运行模式、监听端口、代理池等）需要点击「重载配置」才能生效。</span>
          </div>
        </div>
      )}

      {/* Settings Cards Grid */}
      <div className="grid gap-5 lg:grid-cols-2">

        {/* ===== 全局设置 ===== */}
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-info/10 flex items-center justify-center text-info shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 6V4m0 2a2 2 0 100 4m0-4a2 2 0 110 4m-6 8a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4m6 6v10m6-2a2 2 0 100-4m0 4a2 2 0 110-4m0 4v2m0-6V4" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">全局设置</h3>
              <p className="text-xs text-base-content/50 font-medium">系统基础运行参数</p>
            </div>
          </div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">运行模式</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.mode}
              onChange={(e) => updateField('mode', e.target.value)}
            >
              <option value="pool">pool - 单端口代理池</option>
              <option value="multi-port">multi-port - 多端口模式</option>
              <option value="hybrid">hybrid - 混合模式</option>
            </select>
            <p className="label text-base-content/50 mt-1">pool: 共享端口 | multi-port: 独立端口 | hybrid: 两者并存</p>
          </fieldset>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">日志级别</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.log_level}
              onChange={(e) => updateField('log_level', e.target.value)}
            >
              <option value="debug">debug</option>
              <option value="info">info</option>
              <option value="warn">warn</option>
              <option value="error">error</option>
            </select>
          </fieldset>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">外部 IP 地址</legend>
            <input
              type="text"
              placeholder="例如: 1.2.3.4"
              className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.external_ip}
              onChange={(e) => updateField('external_ip', e.target.value)}
            />
            <p className="label text-base-content/50 mt-1">用于导出时替换 0.0.0.0</p>
          </fieldset>

          <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
            <div>
              <span className="font-semibold text-base-content/90 block mb-0.5">跳过 SSL 证书验证</span>
              <p className="text-xs text-base-content/50 m-0">全局跳过上游代理的 SSL 证书验证</p>
            </div>
            <input
              type="checkbox"
              className="toggle toggle-primary toggle-md"
              checked={settings.skip_cert_verify}
              onChange={(e) => updateField('skip_cert_verify', e.target.checked)}
            />
          </label>
        </div>

        {/* ===== 日志 ===== */}
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-warning/10 flex items-center justify-center text-warning shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 7h16M4 12h16M4 17h10" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">日志输出</h3>
              <p className="text-xs text-base-content/50 font-medium">运行日志与文件轮转</p>
            </div>
          </div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">输出方式</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.log_output}
              onChange={(e) => updateField('log_output', e.target.value)}
            >
              <option value="stdout">stdout</option>
              <option value="file">file</option>
            </select>
          </fieldset>

          {settings.log_output === 'file' && (
            <div className="space-y-4 pt-2 animate-in fade-in slide-in-from-top-2">
              <fieldset className="fieldset">
                <legend className="fieldset-legend font-semibold text-base-content/80">日志文件</legend>
                <input
                  type="text"
                  className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                  value={settings.log_file}
                  onChange={(e) => updateField('log_file', e.target.value)}
                />
              </fieldset>

              <div className="grid grid-cols-3 gap-4">
                <fieldset className="fieldset">
                  <legend className="fieldset-legend font-semibold text-base-content/80">最大大小 MB</legend>
                  <input
                    type="number"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    value={settings.log_max_size}
                    onChange={(e) => updateField('log_max_size', parseInt(e.target.value) || 1)}
                    min={1}
                  />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend font-semibold text-base-content/80">保留文件数</legend>
                  <input
                    type="number"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    value={settings.log_max_backups}
                    onChange={(e) => updateField('log_max_backups', parseInt(e.target.value) || 1)}
                    min={1}
                  />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend font-semibold text-base-content/80">保留天数</legend>
                  <input
                    type="number"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    value={settings.log_max_age}
                    onChange={(e) => updateField('log_max_age', parseInt(e.target.value) || 1)}
                    min={1}
                  />
                </fieldset>
              </div>

              <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
                <span className="font-semibold text-base-content/90">压缩旧日志</span>
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-md"
                  checked={settings.log_compress}
                  onChange={(e) => updateField('log_compress', e.target.checked)}
                />
              </label>
            </div>
          )}
        </div>

        {/* ===== DNS ===== */}
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-accent/10 flex items-center justify-center text-accent shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M12 21a9 9 0 100-18 9 9 0 000 18z" />
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M3.6 9h16.8M3.6 15h16.8M11.5 3a14.8 14.8 0 000 18M12.5 3a14.8 14.8 0 010 18" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">DNS 解析</h3>
              <p className="text-xs text-base-content/50 font-medium">sing-box 与 GeoIP 域名解析</p>
            </div>
          </div>

          <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
            <span className="font-semibold text-base-content/90">启用自定义 DNS</span>
            <input
              type="checkbox"
              className="toggle toggle-primary toggle-md"
              checked={settings.dns_enabled}
              onChange={(e) => updateField('dns_enabled', e.target.checked)}
            />
          </label>

          {settings.dns_enabled && (
            <div className="space-y-4 pt-2 animate-in fade-in slide-in-from-top-2">
              <div className="grid grid-cols-3 gap-4">
                <fieldset className="fieldset col-span-2">
                  <legend className="fieldset-legend font-semibold text-base-content/80">主 DNS 服务器</legend>
                  <input
                    type="text"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    placeholder="223.5.5.5"
                    value={settings.dns_server}
                    onChange={(e) => updateField('dns_server', e.target.value)}
                  />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend font-semibold text-base-content/80">端口</legend>
                  <input
                    type="number"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    value={settings.dns_port}
                    onChange={(e) => updateField('dns_port', parseInt(e.target.value) || 53)}
                    min={1}
                    max={65535}
                  />
                </fieldset>
              </div>

              <fieldset className="fieldset">
                <legend className="fieldset-legend font-semibold text-base-content/80">IP 策略</legend>
                <select
                  className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                  value={settings.dns_strategy}
                  onChange={(e) => updateField('dns_strategy', e.target.value)}
                >
                  <option value="as_is">as_is</option>
                  <option value="prefer_ipv4">prefer_ipv4</option>
                  <option value="prefer_ipv6">prefer_ipv6</option>
                  <option value="ipv4_only">ipv4_only</option>
                  <option value="ipv6_only">ipv6_only</option>
                </select>
              </fieldset>

              <fieldset className="fieldset">
                <legend className="fieldset-legend font-semibold text-base-content/80">备用 DNS 服务器</legend>
                <textarea
                  className="textarea textarea-md w-full min-h-24 bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50 font-mono text-sm"
                  placeholder="8.8.8.8&#10;1.1.1.1"
                  value={settings.dns_fallback_servers.join('\n')}
                  onChange={(e) => updateFallbackServers(e.target.value)}
                />
              </fieldset>
            </div>
          )}
        </div>

        {/* ===== Pool 监听与调度 ===== */}
        {showPoolConfig && (
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-success/10 flex items-center justify-center text-success shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M8.111 16.404a5.5 5.5 0 017.778 0M12 20h.01m-7.08-7.071c3.904-3.905 10.236-3.905 14.141 0M1.394 9.393c5.857-5.857 15.355-5.857 21.213 0" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">Pool 监听与调度</h3>
              <p className="text-xs text-base-content/50 font-medium">pool / hybrid 模式的共享代理入口</p>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">监听地址</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                value={settings.listener_address}
                onChange={(e) => updateField('listener_address', e.target.value)}
              />
            </fieldset>
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">监听端口</legend>
              <input
                type="number"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                value={settings.listener_port}
                onChange={(e) => updateField('listener_port', parseInt(e.target.value) || 0)}
                min={1}
                max={65535}
              />
            </fieldset>
          </div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">监听协议</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.listener_protocol}
              onChange={(e) => updateField('listener_protocol', e.target.value)}
            >
              <option value="http">http</option>
              <option value="socks5">socks5</option>
              <option value="mixed">mixed (HTTP + SOCKS5)</option>
            </select>
            <p className="label text-base-content/50 mt-1">mixed 表示同端口同时支持 HTTP 与 SOCKS5</p>
          </fieldset>

          <div className="grid grid-cols-2 gap-4 pt-2">
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">代理用户名</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                placeholder="可选，留空表示无验证"
                value={settings.listener_username}
                onChange={(e) => updateField('listener_username', e.target.value)}
              />
            </fieldset>
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">代理密码</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                placeholder="可选，留空表示无验证"
                value={settings.listener_password}
                onChange={(e) => updateField('listener_password', e.target.value)}
              />
            </fieldset>
          </div>

          <div className="divider my-1 text-xs">代理池调度</div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">调度模式</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.pool_mode}
              onChange={(e) => updateField('pool_mode', e.target.value)}
            >
              <option value="sequential">sequential - 顺序轮询</option>
              <option value="random">random - 随机选择</option>
              <option value="balance">balance - 最小连接数负载均衡</option>
            </select>
          </fieldset>

          <div className="grid grid-cols-2 gap-4">
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">失败阈值</legend>
              <input
                type="number"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                value={settings.pool_failure_threshold}
                onChange={(e) => updateField('pool_failure_threshold', parseInt(e.target.value) || 1)}
                min={1}
              />
              <p className="label text-base-content/50 mt-1">连续失败多少次后加入黑名单</p>
            </fieldset>
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">黑名单持续时间</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                placeholder="例如: 24h, 1h30m"
                value={settings.pool_blacklist_duration}
                onChange={(e) => updateField('pool_blacklist_duration', e.target.value)}
              />
              <p className="label text-base-content/50 mt-1">Go duration 格式</p>
            </fieldset>
          </div>
        </div>
        )}

        {/* ===== 多端口配置 ===== */}
        {showMultiPortConfig && (
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-secondary/10 flex items-center justify-center text-secondary shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">多端口配置</h3>
              <p className="text-xs text-base-content/50 font-medium">用于 multi-port 或 hybrid 模式</p>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">监听地址</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                value={settings.multi_port_address}
                onChange={(e) => updateField('multi_port_address', e.target.value)}
              />
            </fieldset>
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">起始端口</legend>
              <input
                type="number"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                value={settings.multi_port_base_port}
                onChange={(e) => updateField('multi_port_base_port', parseInt(e.target.value) || 0)}
                min={1}
                max={65535}
              />
            </fieldset>
          </div>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">监听协议</legend>
            <select
              className="select select-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              value={settings.multi_port_protocol}
              onChange={(e) => updateField('multi_port_protocol', e.target.value)}
            >
              <option value="http">http</option>
              <option value="socks5">socks5</option>
              <option value="mixed">mixed (HTTP + SOCKS5)</option>
            </select>
            <p className="label text-base-content/50 mt-1">应用于 multi-port / hybrid 的每个节点入口</p>
          </fieldset>

          <div className="grid grid-cols-2 gap-4 pt-2">
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">默认用户名</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                placeholder="可选"
                value={settings.multi_port_username}
                onChange={(e) => updateField('multi_port_username', e.target.value)}
              />
            </fieldset>
            <fieldset className="fieldset">
              <legend className="fieldset-legend font-semibold text-base-content/80">默认密码</legend>
              <input
                type="text"
                className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                placeholder="可选"
                value={settings.multi_port_password}
                onChange={(e) => updateField('multi_port_password', e.target.value)}
              />
            </fieldset>
          </div>
        </div>
        )}

        {/* ===== 管理面板 ===== */}
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center text-primary shrink-0">
              <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z" />
              </svg>
            </div>
            <div>
              <h3 className="font-bold text-lg text-base-content">管理面板</h3>
              <p className="text-xs text-base-content/50 font-medium">Web 界面及探针设置</p>
            </div>
          </div>

          <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
            <span className="font-semibold text-base-content/90">启用管理面板</span>
            <input
              type="checkbox"
              className="toggle toggle-primary toggle-md"
              checked={settings.management_enabled}
              onChange={(e) => updateField('management_enabled', e.target.checked)}
            />
          </label>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">监听地址</legend>
            <input
              type="text"
              className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              placeholder="0.0.0.0:9090"
              value={settings.management_listen}
              onChange={(e) => updateField('management_listen', e.target.value)}
            />
          </fieldset>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">探测目标</legend>
            <input
              type="text"
              className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              placeholder="http://www.google.com"
              value={settings.management_probe_target}
              onChange={(e) => updateField('management_probe_target', e.target.value)}
            />
            <p className="label text-base-content/50 mt-1">健康检查的目标地址</p>
          </fieldset>

          <fieldset className="fieldset">
            <legend className="fieldset-legend font-semibold text-base-content/80">健康检查间隔</legend>
            <input
              type="text"
              className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
              placeholder="例如: 2h, 30m, 1h30m"
              value={settings.health_check_interval}
              onChange={(e) => updateField('health_check_interval', e.target.value)}
            />
            <p className="label text-base-content/50 mt-1">Go duration 格式：如 2h、30m、1h30m（修改后立即生效，无需重载）</p>
          </fieldset>

        </div>

        {/* ===== GeoIP ===== */}
        <div className="rounded-2xl border border-base-300/50 bg-base-100 p-6 lg:p-8 space-y-5 shadow-sm transition-shadow hover:shadow-md">
          <div className="flex items-center gap-3 mb-2 border-b border-base-200 pb-4">
            <div className="flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-info/10 flex items-center justify-center text-info shrink-0">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M3.055 11H5a2 2 0 012 2v1a2 2 0 002 2 2 2 0 012 2v2.945M8 3.935V5.5A2.5 2.5 0 0010.5 8h.5a2 2 0 012 2 2 2 0 104 0 2 2 0 012-2h1.064M15 20.488V18a2 2 0 012-2h3.064M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
                </svg>
              </div>
              <div>
                <h3 className="font-bold text-lg text-base-content">GeoIP 地域分区</h3>
                <p className="text-xs text-base-content/50 font-medium">节点地域解析与自动更新</p>
              </div>
            </div>
          </div>

          <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
            <div>
              <span className="font-semibold text-base-content/90 block mb-0.5">启用 GeoIP</span>
              <p className="text-xs text-base-content/50 m-0">按地域自动分组节点</p>
            </div>
            <input
              type="checkbox"
              className="toggle toggle-primary toggle-md"
              checked={settings.geoip_enabled}
              onChange={(e) => updateField('geoip_enabled', e.target.checked)}
            />
          </label>

          {settings.geoip_enabled && (
            <div className="space-y-4 pt-2 animate-in fade-in slide-in-from-top-2">
              <div className="rounded-xl bg-base-200/40 border border-base-200 px-4 py-3">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0 space-y-2">
                    <div>
                      <div className="text-xs font-semibold text-base-content/50 mb-1">数据库文件</div>
                      <div className="font-mono text-sm text-base-content/80 break-all">{settings.geoip_database_path || '-'}</div>
                    </div>
                    <div>
                      <div className="text-xs font-semibold text-base-content/50 mb-1">最近更新时间</div>
                      <div className="text-sm text-base-content/70">{formatGeoIPUpdatedAt(settings.geoip_database_updated_at)}</div>
                    </div>
                  </div>
                  <button
                    type="button"
                    className="btn btn-ghost btn-sm btn-square shrink-0"
                    onClick={handleRefreshGeoIP}
                    disabled={refreshingGeoIP}
                    title="重新下载 GeoIP 数据库"
                  >
                    {refreshingGeoIP ? <span className="loading loading-spinner loading-xs"></span> : (
                      <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                      </svg>
                    )}
                  </button>
                </div>
              </div>

              <label className="flex items-center justify-between cursor-pointer gap-4 bg-base-200/30 p-4 rounded-xl border border-base-200 hover:border-base-300 transition-colors">
                <span className="font-semibold text-base-content/90">自动更新数据库</span>
                <input
                  type="checkbox"
                  className="toggle toggle-primary toggle-md"
                  checked={settings.geoip_auto_update_enabled}
                  onChange={(e) => updateField('geoip_auto_update_enabled', e.target.checked)}
                />
              </label>

              {settings.geoip_auto_update_enabled && (
                <fieldset className="fieldset animate-in fade-in slide-in-from-top-2">
                  <legend className="fieldset-legend font-semibold text-base-content/80">更新间隔</legend>
                  <input
                    type="text"
                    className="input input-md w-full bg-base-200/50 focus:bg-base-100 transition-colors focus:border-primary/50"
                    placeholder="24h"
                    value={settings.geoip_auto_update_interval}
                    onChange={(e) => updateField('geoip_auto_update_interval', e.target.value)}
                  />
                </fieldset>
              )}
            </div>
          )}
        </div>

      </div>

      </div>

    </div>
  )
}
