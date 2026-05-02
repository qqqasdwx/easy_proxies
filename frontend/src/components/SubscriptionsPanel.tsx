import { useCallback, useEffect, useMemo, useState } from 'react'
import type { SubscriptionSource, SubscriptionSourcePayload } from '../types'
import {
  createSubscription,
  deleteSubscription,
  fetchSubscriptions,
  refreshSubscriptionSource,
  updateSubscription,
} from '../api/client'

const emptyForm: SubscriptionSourcePayload = {
  name: '',
  url: '',
  enabled: true,
  auto_update: true,
  interval: '1h',
}

function formatTime(value?: string) {
  if (!value || value.startsWith('0001-')) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString()
}

function toPayload(source: SubscriptionSource): SubscriptionSourcePayload {
  return {
    name: source.name || '',
    url: source.url,
    enabled: source.enabled,
    auto_update: source.auto_update,
    interval: source.interval || '1h',
  }
}

export default function SubscriptionsPanel() {
  const [sources, setSources] = useState<SubscriptionSource[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [modalOpen, setModalOpen] = useState(false)
  const [editing, setEditing] = useState<SubscriptionSource | null>(null)
  const [form, setForm] = useState<SubscriptionSourcePayload>(emptyForm)
  const [refreshingId, setRefreshingId] = useState<number | null>(null)
  const [deletingId, setDeletingId] = useState<number | null>(null)

  const loadData = useCallback(async () => {
    try {
      setError('')
      const res = await fetchSubscriptions()
      setSources(res.subscriptions || [])
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载订阅失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadData()
  }, [loadData])

  useEffect(() => {
    if (!success) return
    const timer = setTimeout(() => setSuccess(''), 4000)
    return () => clearTimeout(timer)
  }, [success])

  const stats = useMemo(() => {
    const active = sources.filter(s => s.enabled).length
    const auto = sources.filter(s => s.auto_update).length
    const nodes = sources.reduce((sum, s) => sum + (s.node_count || 0), 0)
    return { active, auto, nodes }
  }, [sources])

  const openCreate = () => {
    setEditing(null)
    setForm(emptyForm)
    setModalOpen(true)
  }

  const openEdit = (source: SubscriptionSource) => {
    setEditing(source)
    setForm(toPayload(source))
    setModalOpen(true)
  }

  const closeModal = () => {
    if (saving) return
    setModalOpen(false)
    setEditing(null)
    setForm(emptyForm)
  }

  const saveForm = async () => {
    if (!form.url.trim()) {
      setError('订阅 URL 不能为空')
      return
    }
    setSaving(true)
    setError('')
    try {
      const res = editing
        ? await updateSubscription(editing.id, form)
        : await createSubscription(form)
      setSuccess(res.message || '订阅已保存')
      closeModal()
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存订阅失败')
    } finally {
      setSaving(false)
    }
  }

  const quickUpdate = async (source: SubscriptionSource, patch: Partial<SubscriptionSourcePayload>) => {
    setError('')
    try {
      await updateSubscription(source.id, { ...toPayload(source), ...patch })
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存订阅失败')
    }
  }

  const refreshOne = async (source: SubscriptionSource) => {
    setRefreshingId(source.id)
    setError('')
    try {
      const res = await refreshSubscriptionSource(source.id)
      setSuccess(res.message || '订阅刷新成功')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '刷新订阅失败')
    } finally {
      setRefreshingId(null)
    }
  }

  const deleteOne = async (source: SubscriptionSource) => {
    if (!window.confirm(`删除订阅「${source.name || source.url}」？`)) return
    setDeletingId(source.id)
    setError('')
    try {
      const res = await deleteSubscription(source.id)
      setSuccess(res.message || '订阅已删除')
      await loadData()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除订阅失败')
    } finally {
      setDeletingId(null)
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <span className="loading loading-spinner loading-lg text-primary"></span>
      </div>
    )
  }

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      <div className="sticky top-0 z-30 bg-base-100/80 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 max-w-[1600px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <span className="w-10 h-10 rounded-xl bg-secondary/10 flex items-center justify-center text-secondary shrink-0 border border-secondary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
                </svg>
              </span>
              订阅管理
            </h2>
            <div className="text-sm font-medium text-base-content/50 mt-1.5 ml-[3.25rem] flex items-center gap-2">
              <span>共 <strong className="text-base-content/80">{sources.length}</strong> 个订阅</span>
              <span className="badge badge-success badge-xs border-none bg-success/15 text-success">启用 {stats.active}</span>
              <span className="badge badge-info badge-xs border-none bg-info/15 text-info">自动 {stats.auto}</span>
              <span className="badge badge-ghost badge-xs bg-base-200 text-base-content/60">节点 {stats.nodes}</span>
            </div>
          </div>
          <button className="btn btn-primary btn-sm lg:btn-md shadow-sm gap-2" onClick={openCreate}>
            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M12 4v16m8-8H4" />
            </svg>
            添加订阅
          </button>
        </div>
      </div>

      <div className="p-4 lg:p-8 space-y-6 flex-1 max-w-[1600px] mx-auto w-full">
        {error && (
          <div role="alert" className="alert alert-error alert-soft text-sm">
            <span>{error}</span>
            <button className="btn btn-ghost btn-xs" onClick={() => setError('')}>x</button>
          </div>
        )}
        {success && (
          <div role="alert" className="alert alert-success alert-soft text-sm">
            <span>{success}</span>
          </div>
        )}

        <div className="rounded-2xl border border-base-300/50 bg-base-100 shadow-sm overflow-hidden">
          <div className="overflow-x-auto">
            <table className="table table-md">
              <thead>
                <tr className="bg-base-200/50 border-b border-base-300/50 text-base-content/70">
                  <th className="font-semibold">名称</th>
                  <th className="font-semibold">URL</th>
                  <th className="font-semibold">状态</th>
                  <th className="font-semibold">自动更新</th>
                  <th className="font-semibold">间隔</th>
                  <th className="font-semibold">节点</th>
                  <th className="font-semibold">上次刷新</th>
                  <th className="font-semibold">操作</th>
                </tr>
              </thead>
              <tbody>
                {sources.length === 0 ? (
                  <tr>
                    <td colSpan={8} className="h-64 text-center text-base-content/50">暂无订阅</td>
                  </tr>
                ) : sources.map(source => (
                  <tr key={source.id} className="hover:bg-base-200/30">
                    <td>
                      <div className="font-semibold text-base-content">{source.name || `subscription-${source.id}`}</div>
                      {source.last_error && <div className="text-xs text-error mt-1 max-w-56 truncate">{source.last_error}</div>}
                    </td>
                    <td className="max-w-[360px]">
                      <code className="text-xs break-all text-base-content/70">{source.url}</code>
                    </td>
                    <td>
                      <input
                        type="checkbox"
                        className="toggle toggle-success toggle-sm"
                        checked={source.enabled}
                        onChange={e => quickUpdate(source, { enabled: e.target.checked })}
                      />
                    </td>
                    <td>
                      <input
                        type="checkbox"
                        className="toggle toggle-primary toggle-sm"
                        checked={source.auto_update}
                        onChange={e => quickUpdate(source, { auto_update: e.target.checked })}
                      />
                    </td>
                    <td className="font-mono text-sm">{source.interval || '-'}</td>
                    <td className="font-mono text-sm">{source.node_count || 0}</td>
                    <td className="text-sm text-base-content/60 whitespace-nowrap">{formatTime(source.last_refresh)}</td>
                    <td>
                      <div className="flex items-center gap-1">
                        <button className="btn btn-ghost btn-xs" onClick={() => refreshOne(source)} disabled={refreshingId === source.id} title="刷新">
                          {refreshingId === source.id ? <span className="loading loading-spinner loading-xs"></span> : (
                            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                            </svg>
                          )}
                        </button>
                        <button className="btn btn-ghost btn-xs" onClick={() => openEdit(source)} title="编辑">
                          <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                          </svg>
                        </button>
                        <button className="btn btn-ghost btn-xs text-error hover:bg-error/10" onClick={() => deleteOne(source)} disabled={deletingId === source.id} title="删除">
                          {deletingId === source.id ? <span className="loading loading-spinner loading-xs"></span> : (
                            <svg xmlns="http://www.w3.org/2000/svg" className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                            </svg>
                          )}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      {modalOpen && (
        <div className="modal modal-open">
          <div className="modal-box max-w-2xl rounded-2xl">
            <h3 className="font-bold text-lg mb-5">{editing ? '编辑订阅' : '添加订阅'}</h3>
            <div className="space-y-4">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">名称</legend>
                  <input className="input w-full" value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} />
                </fieldset>
                <fieldset className="fieldset">
                  <legend className="fieldset-legend">刷新间隔</legend>
                  <input className="input w-full font-mono" value={form.interval} onChange={e => setForm({ ...form, interval: e.target.value })} />
                </fieldset>
              </div>
              <fieldset className="fieldset">
                <legend className="fieldset-legend">URL</legend>
                <input className="input w-full font-mono" value={form.url} onChange={e => setForm({ ...form, url: e.target.value })} />
              </fieldset>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                <label className="flex items-center justify-between gap-4 bg-base-200/50 p-4 rounded-xl">
                  <span className="font-semibold">启用</span>
                  <input type="checkbox" className="toggle toggle-success" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} />
                </label>
                <label className="flex items-center justify-between gap-4 bg-base-200/50 p-4 rounded-xl">
                  <span className="font-semibold">自动更新</span>
                  <input type="checkbox" className="toggle toggle-primary" checked={form.auto_update} onChange={e => setForm({ ...form, auto_update: e.target.checked })} />
                </label>
              </div>
            </div>
            <div className="modal-action">
              <button className="btn btn-ghost" onClick={closeModal} disabled={saving}>取消</button>
              <button className="btn btn-primary" onClick={saveForm} disabled={saving || !form.url.trim()}>
                {saving ? <span className="loading loading-spinner loading-xs"></span> : '保存'}
              </button>
            </div>
          </div>
          <div className="modal-backdrop" onClick={closeModal}></div>
        </div>
      )}
    </div>
  )
}
