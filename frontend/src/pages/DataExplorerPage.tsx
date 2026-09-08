import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Copy, ExternalLink, Search } from 'lucide-react'
import { api } from '../api/client'
import ChainSelect from '../components/ChainSelect'
import ConfirmDialog from '../components/ConfirmDialog'
import CopyToast from '../components/CopyToast'
import NoticeDialog from '../components/NoticeDialog'
import { DATASETS } from '../types'
import { copyText } from '../utils/clipboard'
import { buildOklinkAddressUrl } from '../utils/explorer-url'

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
  // §47：Decoded Input 默认关闭，完整参数会显著增加导出文件体积
  const [includeDecodedInput, setIncludeDecodedInput] = useState(false)
	const [error, setError] = useState('')
	const [copyToast, setCopyToast] = useState('')

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
		setError('')
    try {
      const d = await api.explorerAddresses(params.toString())
			const incoming = (d.addresses ?? []) as CatalogRow[]
			setRows((current) => nextCursor ? [...current, ...incoming] : incoming)
			if (!nextCursor) setSelected(new Set())
      setCursor(d.next_cursor ?? '')
		} catch (e) {
			setError(String(e))
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
		setBusy(true)
		setError('')
		try {
			const resp = await api.updatePreview({ chain, addresses: selectedAddrs, preview_page_size: 500 })
			setPreview(resp)
		} catch (e) {
			setError(String(e))
		} finally {
			setBusy(false)
		}
  }

  async function confirmUpdate() {
    if (!preview?.preview_id) return
    const d = await api.updateConfirm(String(preview.preview_id))
    setPreview(null)
    nav('/jobs?status=RUNNING')
    void d
  }

	async function doExport() {
		setBusy(true)
		setError('')
		let exp
		try {
			exp = await api.exportV2({
				chain, format: 'csv', include_meta: true,
				addresses: selectedAddrs, datasets: ['transactions', 'token_transfers'],
				include_decoded_input: includeDecodedInput,
			})
		} catch (e) {
			setError(String(e))
			setBusy(false)
			return
	}

    const timer = setInterval(async () => {
			try {
				const cur = await api.getExport(exp.id)
				if (cur.status === 'COMPLETED') {
					clearInterval(timer)
					setBusy(false)
					window.open('/api/v1/exports/' + exp.id + '/download', '_blank', 'noopener,noreferrer')
				} else if (cur.status === 'FAILED') {
					clearInterval(timer)
					setBusy(false)
					setError('导出失败: ' + cur.error)
				}
			} catch (e) {
				clearInterval(timer)
				setBusy(false)
				setError(String(e))
			}
    }, 1500)
  }

	async function handleCopy(value: string) {
		try {
			await copyText(value)
			setCopyToast('已复制')
		} catch {
			setCopyToast('复制失败')
		}
	}

  const summary = preview?.summary as Record<string, number> | undefined

  return (
    <div className="page">
      <header className="page-header"><div><span className="eyebrow">INVESTIGATION WORKBENCH</span><h1>数据浏览</h1><p>筛选地址资产、活动与案件关系，进入单地址调查视图</p></div></header>

      <section className="workbench-toolbar chain-select-card" aria-label="地址目录筛选">
          <ChainSelect className="chain-select-compact" value={chain} onChange={setChain} />
          <label className="search-field"><Search size={15} aria-hidden="true" /><input aria-label="搜索地址目录" name="catalog-search" autoComplete="off" spellCheck={false} className="mono" placeholder="搜索地址、名称、案件或标签…" value={q} onChange={(e) => setQ(e.target.value)} /></label>
          <select aria-label="案件筛选" value={caseIds[0] ?? ''} onChange={(e) => setCaseIds(e.target.value ? [e.target.value] : [])}>
            <option value="">全部案件</option>
            {cases.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </select>
          <select aria-label="标签筛选" value={tagIds[0] ?? ''} onChange={(e) => setTagIds(e.target.value ? [e.target.value] : [])}>
            <option value="">全部标签</option>
            {tags.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
          </select>
          <select aria-label="标签匹配模式" value={tagMode} onChange={(e) => setTagMode(e.target.value as 'any' | 'all')}>
            <option value="any">标签 ANY</option>
            <option value="all">标签 ALL</option>
          </select>
      </section>

      <div className={selected.size > 0 ? 'bulk-bar explorer-actions' : 'row between explorer-actions'} style={{ marginBottom: 10, marginTop: 10 }}>
        <span>{selected.size > 0 ? `已选 ${selected.size} 个地址` : `已显示 ${rows.length} 个地址`}</span>
        <div className="row gap">
          <button disabled={selected.size === 0 || busy} onClick={doUpdatePreview}>更新</button>
			<button disabled={selected.size === 0} onClick={() => nav('/data-explorer/combined?chain=' + chain + '&addresses=' + selectedAddrs.join(','))}>查看组合数据</button>
			<button disabled={selected.size === 0} onClick={doExport}>导出 CSV 压缩包</button>
			<label className="export-option" title="导出交易的完整解码参数，会显著增加文件体积（§47）">
				<input type="checkbox" checked={includeDecodedInput} onChange={(e) => setIncludeDecodedInput(e.target.checked)} />
				Include Decoded Input
			</label>
          {selected.size > 0 && <button className="ghost" onClick={() => setSelected(new Set())}>清除</button>}
        </div>
      </div>

      <div className="card table-shell">
        <div className="table-scroll">
        <table className="table">
          <thead>
            <tr>
              <th></th><th>地址</th><th>案件</th><th>标签</th><th>交易</th><th>转账</th><th>NFT</th><th>Logs</th><th>总量</th><th>状态</th><th>最后同步</th><th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.normalized_address} data-clickable="true" tabIndex={0} aria-label={`打开地址 ${r.normalized_address}`}
                onClick={(event) => { if (!(event.target as HTMLElement).closest('a,button,input,label,select')) nav('/data-explorer/' + r.chain + '/' + r.normalized_address) }}
                onKeyDown={(event) => { if (event.key === 'Enter') nav('/data-explorer/' + r.chain + '/' + r.normalized_address) }}>
                <td><input aria-label={`选择地址 ${r.normalized_address}`} type="checkbox" checked={selected.has(r.normalized_address)} onChange={() => toggle(r.normalized_address)} /></td>
                <td className="table-address-cell">
                  <div className="table-address-line"><Link className="mono link" to={'/data-explorer/' + r.chain + '/' + r.normalized_address} title={r.normalized_address}>{r.normalized_address}</Link><button className="table-icon-action" aria-label={`复制地址 ${r.normalized_address}`} title="复制完整地址" onClick={() => void handleCopy(r.normalized_address)}><Copy size={13} /></button>{buildOklinkAddressUrl(r.chain, r.normalized_address) && <a className="table-icon-action" aria-label="在 Explorer 查看地址" title="在 Explorer 查看" href={buildOklinkAddressUrl(r.chain, r.normalized_address)!} target="_blank" rel="noopener noreferrer"><ExternalLink size={13} /></a>}</div>
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
        </div>
		{cursor && <button className="ghost" disabled={busy} onClick={() => load(cursor)}>{busy ? '加载中…' : '加载更多'}</button>}
      </div>

		{error && <NoticeDialog title="数据浏览未完成" message={error} onClose={() => setError('')} />}
		{copyToast && <CopyToast message={copyToast} onClose={() => setCopyToast('')} />}

      {preview && (
        <ConfirmDialog eyebrow="UPDATE PREVIEW" title={`更新预览${preview.target_block ? ' · Block ' + preview.target_block : ''}`} confirmLabel="确认更新" onCancel={() => setPreview(null)} onConfirm={confirmUpdate}>
          <p>
			共 {summary?.addresses ?? 0} 个地址 · 有新增 {summary?.with_new_data ?? 0} · 已是最新 {summary?.up_to_date ?? 0} · 精确新增 {summary?.exact_rows ?? 0} · 估算新增 ≈{summary?.estimated_rows ?? 0} · 预计 {summary?.estimated_bytes ?? 0} 字节
          </p>
        </ConfirmDialog>
      )}
    </div>
  )
}
