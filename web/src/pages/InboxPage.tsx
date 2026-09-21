import { useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { request, ApiError } from '../api/client'
import type { AccountSummary, Alias, FullMessage, InboxResult, InboxMessage, MailFolderScope } from '../api/types'
import AsyncState from '../components/AsyncState'
import Dialog from '../components/Dialog'
import ConfirmDialog from '../components/ConfirmDialog'
import { useToast } from '../components/ToastProvider'
import { IconKey, IconMail, IconTrash } from '../components/icons'

function formatDate(raw: string): string {
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(d)
}

function folderScope(value: string | null): MailFolderScope {
  return value === 'inbox' || value === 'junk' ? value : 'all'
}

export default function InboxPage() {
  const [searchParams, setSearchParams] = useSearchParams()
  const [folder, setFolder] = useState<MailFolderScope>(() => folderScope(searchParams.get('folder')))
  const [accounts, setAccounts] = useState<AccountSummary[]>([])
  const [aliases, setAliases] = useState<Alias[]>([])
  const [accountId, setAccountId] = useState('')
  const [alias, setAlias] = useState('')
  const [limit, setLimit] = useState(20)
  const [days, setDays] = useState(7)

  const [result, setResult] = useState<InboxResult | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [retryKey, setRetryKey] = useState(0)
  const [detail, setDetail] = useState<FullMessage | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)
  const [deleteFor, setDeleteFor] = useState<InboxMessage | null>(null)
  const [deleting, setDeleting] = useState(false)

  const abortRef = useRef<AbortController | null>(null)
  const { show } = useToast()

  async function openMessage(message: InboxMessage) {
    setDetailLoading(true)
    try {
      const data = await request<FullMessage>(`/api/inbox/${encodeURIComponent(message.id)}?account_id=${encodeURIComponent(accountId)}&folder=${message.folder ?? 'inbox'}`)
      setDetail(data)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '读取邮件详情失败')
    } finally {
      setDetailLoading(false)
    }
  }

  async function deleteMessage() {
    if (!deleteFor) return
    setDeleting(true)
    try {
      await request(`/api/inbox/${encodeURIComponent(deleteFor.id)}?account_id=${encodeURIComponent(accountId)}&folder=${deleteFor.folder ?? 'inbox'}`, { method: 'DELETE' })
      setDeleteFor(null)
      setDetail(null)
      show('邮件已删除')
      setRetryKey((key) => key + 1)
    } catch (err) {
      show(err instanceof ApiError ? err.message : '删除邮件失败')
    } finally {
      setDeleting(false)
    }
  }

  // 加载账号列表并初始化筛选状态。
  useEffect(() => {
    let cancelled = false
    request<AccountSummary[]>('/api/accounts')
      .then((data) => {
        if (cancelled) return
        setAccounts(data)
        const queryId = searchParams.get('account_id')
        const valid = data.find((a) => a.id === queryId)
        const target = valid ? valid.id : data[0]?.id ?? ''
        setAccountId(target)
        if (target) {
          const next: Record<string, string> = { account_id: target, folder }
          const qAlias = searchParams.get('alias')
          if (qAlias) {
            setAlias(qAlias)
            next.alias = qAlias
          }
          const qLimit = searchParams.get('limit')
          if (qLimit && ['1', '20', '100'].includes(qLimit)) { setLimit(Number(qLimit)); next.limit = qLimit }
          const qDays = searchParams.get('days')
          if (qDays && ['1', '7', '30', '90'].includes(qDays)) { setDays(Number(qDays)); next.days = qDays }
          setSearchParams(next, { replace: true })
        }
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 账号变化时加载别名列表(供筛选)
  useEffect(() => {
    if (!accountId) return
    let cancelled = false
    request<{ account_id: string; count: number; aliases: Alias[] }>(
      `/api/aliases?account_id=${encodeURIComponent(accountId)}`,
    )
      .then((data) => {
        if (cancelled) return
        setAliases(data.aliases ?? [])
      })
      .catch(() => {
        if (cancelled) return
        setAliases([])
      })
    return () => {
      cancelled = true
    }
  }, [accountId])

  // 查询收件箱;账号变化时清空旧邮件并中止旧请求
  useEffect(() => {
    if (!accountId) return
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    let cancelled = false
    const params = new URLSearchParams({ account_id: accountId, folder })
    if (alias) params.set('alias', alias)
    params.set('limit', String(limit))
    params.set('days', String(days))
    request<InboxResult>(`/api/inbox?${params.toString()}`, {
      signal: controller.signal,
    })
      .then((data) => {
        if (cancelled) return
        setResult(data)
        setError('')
      })
      .catch((err) => {
        if (cancelled || err instanceof ApiError && err.status === 0) return
        setError(err instanceof ApiError ? err.message : '网络连接失败，请检查服务状态')
        setResult(null)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [accountId, alias, folder, limit, days, retryKey])

  const qAlias = useMemo(() => alias, [alias])

  function handleSearch() {
    const next: Record<string, string> = { account_id: accountId, folder }
    if (qAlias) next.alias = qAlias
    next.limit = String(limit)
    next.days = String(days)
    setSearchParams(next, { replace: true })
    setRetryKey((k) => k + 1)
  }

  function handleAccountChange(id: string) {
    setAccountId(id)
    setAlias('')
    setResult(null)
    setSearchParams({ account_id: id, folder, limit: String(limit), days: String(days) }, { replace: true })
  }

  function handleFolderChange(value: string) {
    const nextFolder = folderScope(value)
    setFolder(nextFolder)
    setResult(null)
    setError('')
    setLoading(true)
    const next = new URLSearchParams(searchParams)
    next.set('folder', nextFolder)
    setSearchParams(next, { replace: true })
  }

  const methodText = result?.method === 'imap' ? 'IMAP' : 'Web API'

  return (
    <section>
      <div className="page-header">
        <div className="page-title">
          <h2>邮件摘要</h2>
          <p>查看收件箱和垃圾邮件中的纯文本摘要，保留邮件原有分类</p>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 12 }}>
          <div className="form-field" style={{ marginBottom: 0 }}>
            <label htmlFor="inbox-account">账号</label>
            <select
              id="inbox-account"
              value={accountId}
              onChange={(e) => handleAccountChange(e.target.value)}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </div>
          <div className="form-field" style={{ marginBottom: 0 }}>
            <label htmlFor="inbox-folder">邮件范围</label>
            <select id="inbox-folder" value={folder} disabled={!accountId} onChange={(e) => handleFolderChange(e.target.value)}>
              <option value="all">收件箱＋垃圾邮件</option>
              <option value="inbox">收件箱</option>
              <option value="junk">垃圾邮件</option>
            </select>
          </div>
          <div className="form-field" style={{ marginBottom: 0 }}>
            <label htmlFor="inbox-alias">别名</label>
            <select
              id="inbox-alias"
              value={alias}
              onChange={(e) => setAlias(e.target.value)}
            >
              <option value="">全部</option>
              {aliases.map((a) => (
                <option key={a.anonymousId} value={a.email}>
                  {a.email}
                </option>
              ))}
            </select>
          </div>
          <div className="form-field" style={{ marginBottom: 0 }}>
            <label htmlFor="inbox-limit">每页</label>
            <select
              id="inbox-limit"
              value={limit}
              onChange={(e) => setLimit(Number(e.target.value))}
            >
              <option value={1}>1</option>
              <option value={20}>20</option>
              <option value={100}>100</option>
            </select>
          </div>
          <div className="form-field" style={{ marginBottom: 0 }}>
            <label htmlFor="inbox-days">时间范围</label>
            <select
              id="inbox-days"
              value={days}
              onChange={(e) => setDays(Number(e.target.value))}
            >
              <option value={1}>1 天</option>
              <option value={7}>7 天</option>
              <option value={30}>30 天</option>
              <option value={90}>90 天</option>
            </select>
          </div>
          <div style={{ display: 'flex', alignItems: 'flex-end' }}>
            <button className="primary" onClick={handleSearch}>
              查询
            </button>
          </div>
        </div>
      </div>

      {result?.method === 'web_api' && alias && <p className="hint">按别名筛选可能遗漏收件人信息不完整的邮件；找不到时，请将“别名”切换为“全部”。</p>}
      <AsyncState
        loading={loading}
        error={error}
        empty={!result || result.messages.length === 0}
        emptyText="暂无邮件"
        onRetry={() => {
          setLoading(true)
          setRetryKey((k) => k + 1)
        }}
      >
        {result && result.messages.length > 0 && (
          <>
            <p className="hint" style={{ marginBottom: 8, display: 'flex', alignItems: 'center', gap: 8 }}>
              <span>共 {result.count} 封</span>
              <span className={result.method === 'imap' ? 'badge badge-info' : 'badge badge-neutral'}>
                {result.method === 'imap' ? <IconKey size={12} /> : <IconMail size={12} />}
                读取方式：{methodText}
              </span>
            </p>
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>来源</th>
                    <th>主题</th>
                    <th>发件人</th>
                    <th>收件人</th>
                    <th>日期</th>
                    <th>摘要</th>
                  </tr>
                </thead>
                <tbody>
                  {result.messages.map((m) => (
                    <tr key={`${m.folder ?? 'inbox'}:${m.id}`}>
                      <td><span className={m.folder === 'junk' ? 'badge badge-pending' : 'badge badge-neutral'}>{m.folder === 'junk' ? '垃圾邮件' : '收件箱'}</span></td>
                      <td>{result.method === 'imap' ? <button className="link-button" onClick={() => void openMessage(m)}>{m.subject || '（无主题）'}</button> : (m.subject || '（无主题）')}</td>
                      <td>{m.from}</td>
                      <td>{m.to}</td>
                      <td>{formatDate(m.date)}</td>
                      <td>{m.preview || '—'} {result.method === 'imap' && <button className="icon-button danger" aria-label="删除邮件" title="删除邮件" onClick={() => setDeleteFor(m)}><IconTrash size={14} /></button>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </AsyncState>
      <Dialog title={detail?.subject || '邮件详情'} open={detail !== null || detailLoading} onClose={() => setDetail(null)}>
        {detailLoading && <p className="hint">读取中…</p>}
        {detail && <>
          <p className="hint">发件人：{detail.from}</p>
          <p className="hint">收件人：{detail.to}</p>
          <p className="hint">日期：{formatDate(detail.date)}</p>
          <pre className="mail-body">{detail.body || '无正文'}</pre>
          <div className="form-actions"><button className="danger" onClick={() => setDeleteFor(detail)}>删除邮件</button><button onClick={() => setDetail(null)}>关闭</button></div>
        </>}
      </Dialog>
      {deleteFor && <ConfirmDialog title="删除邮件" message={`邮件将从${deleteFor.folder === 'junk' ? '垃圾邮件' : '收件箱'}中永久删除。`} open busy={deleting} onClose={() => setDeleteFor(null)} onConfirm={() => void deleteMessage()} />}
    </section>
  )
}
