import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import { CHAINS, DATASETS } from '../types'

interface CatalogRow {
  chain: string
  address: string
  normalized_address: string
  display_name?: string
  case_names: string[]
  tag_names: string[]
  transaction_count: number
  token_transfer_count: number
  nft_transfer_count: number
  log_count: number
  total_activity_count: number
  last_data_block?: number
  last_sync_at?: string
  sync_status: string
}

export default function DataExplorerPage() {
  const nav = useNavigate()
  const [chain, setChain] = useState('bsc')
  const [q, setQ] = useState('')
  const [caseIds, setCaseIds] = useState<string[]>([])
  const [tagIds, setTagIds] = useState<string[]>([])
  const [tagMode, setTagMode] = useState<'any' | 'all'>('any')
  const [rows, setRows] = useState<CatalogRow[]>([])
  const [cursor, setCursor] = useState('')
  const [cases, setCases] = useState<{ id: string; name: string }[]>([])
  const [tags, setTags] = useState<{ id: string; name: string }[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [preview, setPreview] = useState<Record<string, unknown> | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => { api.listCases().then((d) => setCases(d.cases)).catch(() => {}) }, [])
  useEffect(() => { api.listTags().then((d) => setTags(d.tags)).catch(() => {}) }, [])

  async function load(nextCursor = '') {
    const params = new URLSearchParams({ chain, limit: '100' })
    if (q) params.set('q', q)
    if (caseIds.length) params.set('case_ids', caseIds.join(','))
    if (tagIds.length) params.set('tag_ids', tagIds.join(','))
    if (tagMode === 'all') params.set('tag_mode', 'all')
    if (nextCursor) params.set('cursor', nextCursor)
    setBusy(true)
    try {
      const d = await api.explorerAddresses(params.toString())
      setRows((d.addresses ?? []) as CatalogRow[])
      setCursor(d.next_cursor ?? '')
    } finally {
      setBusy(false)
    }
  }

  // 搜索 debounce 250ms（§10）
  useEffect(() => {
    const t = setTimeout(() => load(''), 250)
    return () => clearTimeout(t)
  }, [chain, q, caseIds, tagIds, tagMode])

  const selectedAddrs = useMemo(() => rows.filter((r) => selected.has(r.normalized_address)).map((r) => r.normalized_address), [rows, selected])

  function toggle(addr: string) {
    setSelected((cur) => {
      const next = new Set(cur)
      if (next.has(addr)) next.delete(addr); else next.add(addr)
      return next
    })
  }

  async function doUpdatePreview() {
    setPreview(null)
    const resp = await api.updatePreview({ chain, addresses: selectedAddrs, preview_page_size: 500 })
    setPreview(resp)
  }

  async function confirmUpdate() {
    if (!preview?.preview_id) return
    const d = await api.updateConfirm(String(preview.preview_id))
    setPreview(null)
    nav('/jobs?status=RUNNING')
    void d
  }

  async function doExport() {
    const exp = await api.exportV2({
      chain, format: 'csv', include_meta: true,
      addresses: selectedAddrs, datasets: ['transactions', 'token_transfers'],
    })
    const timer = setInterval(async () => {
      const cur = await api.getExport(exp.id)
      if (cur.status === 'COMPLETED') {
        clearInterval(timer)
        window.open('/api/v1/exports/' + exp.id + '/download', '_blank')
      } else if (cur.status === 'FAILED') {
        clearInterval(timer)
        alert('导出失败: ' + cur.error)
      }
    }, 1500)
  }

  const summary = preview?.summary as Record<string, number> | undefined

  return (
    <div className="page">
      <h1>数据浏览</h1>
      <p className="sub">浏览、筛选、更新和导出本地链上数据</p>

      <div className="card">
        <div className="row gap wrap">
          <select style={{ maxWidth: 140 }} value={chain} onChange={(e) => setChain(e.target.value)}>
            {CHAINS.map((c) => <option key={c} value={c}>{c.toUpperCase()}</option>)}
          </select>
          <input className="flex-2 mono" placeholder="搜索地址 / 前缀 / 后缀 / 名称 / 案件 / 标签" value={q} onChange={(e) => setQ(e.target.value)} />
          <select style={{ maxWidth: 150 }} value={caseIds[0] ?? ''} onChange={(e) => setCaseIds(e.target.value ? [e.target.value] : [])}>
            <option value="">全部案件</option>
            {cases.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <select style={{ maxWidth: 150 }} value={tagIds[0] ?? ''} onChange={(e) => setTagIds(e.target.value ? [e.target.value] : [])}>
            <option value="">全部标签</option>
            {tags.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <select style={{ maxWidth: 110 }} value={tagMode} onChange={(e) => setTagMode(e.target.value as 'any' | 'all')}>
            <option value="any">标签 ANY</option>
            <option value="all">标签 ALL</option>
          </select>
        </div>
      </div>

      <div className="row between" style={{ marginBottom: 10 }}>
        <span className="muted">已显示 {rows.length} 个地址 · 已选 {selected.size}</span>
        <div className="row gap">
          <button disabled={selected.size === 0 || busy} onClick={doUpdatePreview}>更新</button>
          <button disabled={selected.size === 0} onClick={doExport}>导出 CSV</button>
          {selected.size > 0 && <button className="ghost" onClick={() => setSelected(new Set())}>清除</button>}
        </div>
      </div>

      <div className="card">
        <table className="table">
          <thead>
            <tr>
              <th></th><th>地址</th><th>案件</th><th>标签</th><th>交易</th><th>转账</th><th>NFT</th><th>Logs</th><th>总量</th><th>状态</th><th>最后同步</th><th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.normalized_address}>
                <td><input type="checkbox" checked={selected.has(r.normalized_address)} onChange={() => toggle(r.normalized_address)} /></td>
                <td>
                  <Link className="mono link" to={'/data-explorer/' + r.chain + '/' + r.normalized_address}>{r.normalized_address.slice(0, 10)}…</Link>
                  {r.display_name && <div className="muted">{r.display_name}</div>}
                </td>
                <td>{r.case_names?.join('、') || '—'}</td>
                <td>{r.tag_names?.map((t) => <span key={t} className="badge b-RUNNING" style={{ marginRight: 4 }}>{t}</span>) || '—'}</td>
                <td>{r.transaction_count.toLocaleString()}</td>
                <td>{r.token_transfer_count.toLocaleString()}</td>
                <td>{r.nft_transfer_count.toLocaleString()}</td>
                <td>{r.log_count.toLocaleString()}</td>
                <td>{r.total_activity_count.toLocaleString()}</td>
                <td><span className={'badge b-' + (r.sync_status === 'current' ? 'COMPLETED' : 'PAUSED')}>{r.sync_status}</span></td>
                <td className="muted">{r.last_sync_at ? new Date(r.last_sync_at).toLocaleString() : '—'}</td>
                <td><Link className="link" to={'/data-explorer/' + r.chain + '/' + r.normalized_address}>详情</Link></td>
              </tr>
            ))}
            {rows.length === 0 && <tr><td colSpan={12} className="muted center">{busy ? '加载中…' : '目录为空，去「地址下载」页下载或导入地址'}</td></tr>}
          </tbody>
        </table>
        {cursor && <button className="ghost" onClick={() => load(cursor)}>加载更多</button>}
      </div>

      {preview && (
        <div className="card">
          <div className="card-title">更新预览 {preview.target_block ? '· 检查至 Block ' + preview.target_block : ''}</div>
          <p>
            有新增 {summary?.addresses ?? 0} · 已是最新 {summary?.up_to_date ?? 0} · 精确新增 {summary?.exact_rows ?? 0} · 估算新增 ≈{summary?.estimated_rows ?? 0} · 预计 {summary?.estimated_bytes ?? 0} 字节
          </p>
          <div className="row gap">
            <button className="primary" onClick={confirmUpdate}>确认更新</button>
            <button className="ghost" onClick={() => setPreview(null)}>取消</button>
          </div>
        </div>
      )}
    </div>
  )
}
