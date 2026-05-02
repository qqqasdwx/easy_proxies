import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchLogs } from '../api/client'

function formatLogContent(logs: string): string {
  if (!logs.trim()) return '暂无日志'
  return logs
}

export default function LogsPanel() {
  const [logs, setLogs] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [autoRefresh, setAutoRefresh] = useState(true)
  const preRef = useRef<HTMLPreElement | null>(null)

  const loadLogs = useCallback(async () => {
    try {
      setError('')
      const res = await fetchLogs()
      setLogs(res.logs || '')
      requestAnimationFrame(() => {
        const el = preRef.current
        if (el) el.scrollTop = el.scrollHeight
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载日志失败')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    loadLogs()
  }, [loadLogs])

  useEffect(() => {
    if (!autoRefresh) return
    const timer = setInterval(loadLogs, 3000)
    return () => clearInterval(timer)
  }, [autoRefresh, loadLogs])

  return (
    <div className="flex flex-col min-h-full animate-in fade-in duration-500">
      <div className="sticky top-0 z-30 bg-base-100/80 backdrop-blur-xl px-4 lg:px-8 py-4 border-b border-base-300/60 shadow-sm">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 max-w-[1600px] mx-auto w-full">
          <div>
            <h2 className="text-2xl font-bold flex items-center gap-3">
              <div className="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center text-primary shrink-0 border border-primary/20">
                <svg xmlns="http://www.w3.org/2000/svg" className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2.5" d="M4 7h16M4 12h16M4 17h10" />
                </svg>
              </div>
              日志控制台
            </h2>
            <p className="text-sm text-base-content/50 mt-1.5 ml-[3.25rem]">查看最近 1000 行运行日志</p>
          </div>
          <div className="flex items-center gap-3">
            <label className="flex items-center gap-2 cursor-pointer bg-base-200/50 px-3 py-1.5 rounded-lg border border-base-300/50">
              <span className="text-sm font-medium">自动刷新</span>
              <input
                type="checkbox"
                className="toggle toggle-primary toggle-sm"
                checked={autoRefresh}
                onChange={(e) => setAutoRefresh(e.target.checked)}
              />
            </label>
            <button className="btn btn-sm lg:btn-md btn-primary shadow-sm gap-2" onClick={loadLogs} disabled={loading}>
              {loading ? <span className="loading loading-spinner loading-sm"></span> : null}
              刷新
            </button>
          </div>
        </div>
      </div>

      <div className="p-4 lg:p-8 flex-1 max-w-[1600px] mx-auto w-full min-h-0">
        {error && (
          <div role="alert" className="alert alert-error alert-soft text-sm mb-4">
            <span>{error}</span>
          </div>
        )}
        <pre
          ref={preRef}
          className="h-[calc(100vh-12rem)] overflow-auto rounded-xl border border-base-300/60 bg-neutral text-neutral-content p-4 text-xs leading-relaxed whitespace-pre-wrap break-words shadow-inner"
        >
          {formatLogContent(logs)}
        </pre>
      </div>
    </div>
  )
}
