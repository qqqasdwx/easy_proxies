import { useState, useEffect, type ReactNode } from 'react'
import type { SettingsData } from '../types'
import { fetchSettings, updateSettings, triggerReload } from '../api/client'

type SettingsSection = 'network' | 'health' | 'system'

const defaultSettings: SettingsData = {
  log_level: 'info',
  sing_box_version: 'unknown',
  external_ip: '',
  skip_cert_verify: false,

  multi_port_address: '0.0.0.0',
  multi_port_base_port: 24000,
  multi_port_protocol: 'mixed',
  multi_port_username: '',
  multi_port_password: '',

  dns_enabled: false,
  dns_server: '223.5.5.5',
  dns_fallback_servers: ['8.8.8.8', '1.1.1.1'],
  dns_port: 53,
  dns_strategy: 'prefer_ipv4',

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

const sections: Array<{ id: SettingsSection; title: string; description: string }> = [
  { id: 'network', title: '网络', description: 'DNS 解析与地址策略' },
  { id: 'health', title: '健康检查', description: '探测目标、超时和并发' },
  { id: 'system', title: '系统', description: '诊断与全局开关' },
]

const inputClass = 'input input-md w-full bg-base-200/60 focus:bg-base-100 border-base-300/70 focus:border-primary/60'
const selectClass = 'select select-md w-full bg-base-200/60 focus:bg-base-100 border-base-300/70 focus:border-primary/60'
const textareaClass = 'textarea textarea-md w-full min-h-24 bg-base-200/60 focus:bg-base-100 border-base-300/70 focus:border-primary/60 font-mono text-sm'

function SectionTitle({ title, description }: { title: string; description: string }) {
  return (
    <div className="pb-5 border-b border-base-300/60">
      <h3 className="text-xl font-bold text-base-content">{title}</h3>
      <p className="text-sm text-base-content/55 mt-1">{description}</p>
    </div>
  )
}

function Group({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="space-y-4 py-6 border-b border-base-300/50 last:border-b-0 last:pb-0">
      <h4 className="text-sm font-bold text-base-content/70">{title}</h4>
      {children}
    </section>
  )
}

export default function SettingsPanel() {
  const [settings, setSettings] = useState<SettingsData>(defaultSettings)
  const [savedSettings, setSavedSettings] = useState<SettingsData>(defaultSettings)
  const [activeSection, setActiveSection] = useState<SettingsSection>('network')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [reloading, setReloading] = useState(false)
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
    if (!success) return
    const timer = setTimeout(() => setSuccess(''), 5000)
    return () => clearTimeout(timer)
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

  const updateField = <K extends keyof SettingsData>(key: K, value: SettingsData[K]) => {
    setSettings(s => {
      const updated = { ...s, [key]: value }
      setIsDirty(JSON.stringify(updated) !== JSON.stringify(savedSettings))
      return updated
    })
  }

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

  const activeMeta = sections.find(section => section.id === activeSection) || sections[0]

  const updateFallbackServers = (value: string) => {
    updateField(
      'dns_fallback_servers',
      value
        .split(/[\n,]+/)
        .map(item => item.trim())
        .filter(Boolean)
    )
  }

  const renderNetworkSection = () => (
    <>
      <SectionTitle title="网络" description="配置 DNS 解析和地址选择策略。" />

      <Group title="DNS">
        <label className="flex items-center justify-between gap-4 border border-base-300/70 rounded-lg px-4 py-3">
          <span className="font-semibold">启用自定义 DNS</span>
          <input type="checkbox" className="toggle toggle-primary" checked={settings.dns_enabled} onChange={e => updateField('dns_enabled', e.target.checked)} />
        </label>

        {settings.dns_enabled && (
          <div className="space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-[1fr_9rem] gap-4">
              <fieldset className="fieldset">
                <legend className="fieldset-legend">主 DNS 服务器</legend>
                <input className={inputClass} placeholder="223.5.5.5" value={settings.dns_server} onChange={e => updateField('dns_server', e.target.value)} />
              </fieldset>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">端口</legend>
                <input type="number" className={inputClass} value={settings.dns_port} min={1} max={65535} onChange={e => updateField('dns_port', parseInt(e.target.value) || 53)} />
              </fieldset>
            </div>

            <fieldset className="fieldset">
              <legend className="fieldset-legend">IP 策略</legend>
              <select className={selectClass} value={settings.dns_strategy} onChange={e => updateField('dns_strategy', e.target.value)}>
                <option value="as_is">as_is</option>
                <option value="prefer_ipv4">prefer_ipv4</option>
                <option value="prefer_ipv6">prefer_ipv6</option>
                <option value="ipv4_only">ipv4_only</option>
                <option value="ipv6_only">ipv6_only</option>
              </select>
            </fieldset>

            <fieldset className="fieldset">
              <legend className="fieldset-legend">备用 DNS 服务器</legend>
              <textarea className={textareaClass} placeholder={'8.8.8.8\n1.1.1.1'} value={settings.dns_fallback_servers.join('\n')} onChange={e => updateFallbackServers(e.target.value)} />
            </fieldset>
          </div>
        )}
      </Group>

    </>
  )

  const renderHealthSection = () => (
    <>
      <SectionTitle title="健康检查" description="统一控制周期探测、批量探测和探测目标。" />

      <Group title="探测目标">
        <fieldset className="fieldset">
          <legend className="fieldset-legend">目标地址</legend>
          <input className={inputClass} placeholder="www.apple.com:80" value={settings.management_probe_target} onChange={e => updateField('management_probe_target', e.target.value)} />
        </fieldset>
      </Group>

      <Group title="探测参数">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <fieldset className="fieldset">
            <legend className="fieldset-legend">检查间隔</legend>
            <input className={`${inputClass} font-mono`} placeholder="5m" value={settings.health_check_interval} onChange={e => updateField('health_check_interval', e.target.value)} />
          </fieldset>
          <fieldset className="fieldset">
            <legend className="fieldset-legend">探测超时</legend>
            <input className={`${inputClass} font-mono`} placeholder="10s" value={settings.health_check_timeout} onChange={e => updateField('health_check_timeout', e.target.value)} />
          </fieldset>
          <fieldset className="fieldset">
            <legend className="fieldset-legend">并发数</legend>
            <input
              type="number"
              className={inputClass}
              min={1}
              value={settings.health_check_concurrency}
              onChange={e => updateField('health_check_concurrency', parseInt(e.target.value) || 1)}
            />
          </fieldset>
        </div>
      </Group>
    </>
  )

  const renderSystemSection = () => (
    <>
      <SectionTitle title="系统" description="配置诊断输出、导出地址和全局连接选项。" />

      <Group title="诊断">
        <fieldset className="fieldset">
          <legend className="fieldset-legend">sing-box 核心版本</legend>
          <div className="input input-md w-full bg-base-200/60 border-base-300/70 font-mono text-sm items-center">
            {settings.sing_box_version}
          </div>
        </fieldset>

        <fieldset className="fieldset">
          <legend className="fieldset-legend">sing-box 日志级别</legend>
          <select className={selectClass} value={settings.log_level} onChange={e => updateField('log_level', e.target.value)}>
            <option value="debug">debug - 临时排查连接问题</option>
            <option value="info">info - 默认</option>
            <option value="warn">warn - 仅警告和错误</option>
            <option value="error">error - 仅错误</option>
          </select>
          <p className="label text-base-content/50 mt-1">日志内容在左侧“日志控制台”查看，文件输出和轮转由部署环境管理。</p>
        </fieldset>
      </Group>

      <Group title="全局选项">
        <fieldset className="fieldset">
          <legend className="fieldset-legend">外部 IP 地址</legend>
          <input className={inputClass} placeholder="例如: 1.2.3.4" value={settings.external_ip} onChange={e => updateField('external_ip', e.target.value)} />
        </fieldset>

        <label className="flex items-center justify-between gap-4 border border-base-300/70 rounded-lg px-4 py-3">
          <div>
            <span className="font-semibold block">跳过 SSL 证书验证</span>
            <span className="text-xs text-base-content/55">全局跳过上游代理的 SSL 证书验证</span>
          </div>
          <input type="checkbox" className="toggle toggle-primary" checked={settings.skip_cert_verify} onChange={e => updateField('skip_cert_verify', e.target.checked)} />
        </label>
      </Group>
    </>
  )

  const renderActiveSection = () => {
    switch (activeSection) {
      case 'network':
        return renderNetworkSection()
      case 'health':
        return renderHealthSection()
      case 'system':
        return renderSystemSection()
      default:
        return renderNetworkSection()
    }
  }

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      <div className="sticky top-0 z-30 bg-base-100/90 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col xl:flex-row justify-between items-start xl:items-center gap-4 max-w-[1440px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <span className="w-10 h-10 rounded-lg bg-primary/10 flex items-center justify-center text-primary shrink-0 border border-primary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
                </svg>
              </span>
              系统设置
            </h2>
            <p className="text-sm text-base-content/50 mt-1.5 ml-[3.25rem]">{activeMeta.description}</p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <span className={`badge badge-sm ${isDirty ? 'badge-warning' : needReload ? 'badge-info' : 'badge-ghost'}`}>
              {isDirty ? '有未保存更改' : needReload ? '等待重载' : '已保存'}
            </span>
            <button
              className={`btn btn-sm lg:btn-md gap-2 ${isDirty ? 'btn-primary' : 'btn-ghost border border-base-300'}`}
              onClick={handleSave}
              disabled={saving || !isDirty}
            >
              {saving ? <span className="loading loading-spinner loading-sm"></span> : '保存设置'}
            </button>
            {needReload && (
              <button className="btn btn-warning btn-sm lg:btn-md gap-2" onClick={handleReload} disabled={reloading}>
                {reloading ? <span className="loading loading-spinner loading-sm"></span> : '重载配置'}
              </button>
            )}
          </div>
        </div>
      </div>

      <div className="p-4 lg:p-8 max-w-[1440px] mx-auto w-full flex-1">
        {(error || success || needReload) && (
          <div className="space-y-3 mb-5">
            {error && (
              <div role="alert" className="alert alert-error alert-soft text-sm">
                <span>{error}</span>
              </div>
            )}
            {success && (
              <div role="alert" className="alert alert-success alert-soft text-sm">
                <span>{success}</span>
              </div>
            )}
            {needReload && (
              <div role="alert" className="alert alert-warning alert-soft text-sm">
                <span>配置已保存，DNS、健康检查和 sing-box 日志级别等运行配置可能需要重载后完全生效。</span>
              </div>
            )}
          </div>
        )}

        <div className="lg:hidden mb-4 overflow-x-auto">
          <div className="tabs tabs-box bg-base-200/70 w-max">
            {sections.map(section => (
              <button
                key={section.id}
                type="button"
                className={`tab whitespace-nowrap ${activeSection === section.id ? 'tab-active' : ''}`}
                onClick={() => setActiveSection(section.id)}
              >
                {section.title}
              </button>
            ))}
          </div>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-[260px_1fr] gap-6">
          <aside className="hidden lg:block">
            <nav className="sticky top-28 border border-base-300/60 rounded-lg bg-base-100 overflow-hidden">
              {sections.map(section => (
                <button
                  key={section.id}
                  type="button"
                  className={`w-full text-left px-4 py-3 border-b border-base-300/50 last:border-b-0 transition-colors ${
                    activeSection === section.id
                      ? 'bg-primary/10 text-primary'
                      : 'hover:bg-base-200/70 text-base-content/80'
                  }`}
                  onClick={() => setActiveSection(section.id)}
                >
                  <span className="block font-bold text-sm">{section.title}</span>
                  <span className="block text-xs opacity-65 mt-0.5">{section.description}</span>
                </button>
              ))}
            </nav>
          </aside>

          <main className="border border-base-300/60 rounded-lg bg-base-100 px-4 md:px-8 py-6 md:py-8 min-w-0">
            {renderActiveSection()}
          </main>
        </div>
      </div>
    </div>
  )
}
