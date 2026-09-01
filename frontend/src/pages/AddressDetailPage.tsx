import { useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api } from '../api/client'
import TokenFilterDropdown, { type TokenFilterOption } from '../components/TokenFilterDropdown'
import MethodCell from '../features/data-explorer/components/MethodCell'
import TransactionDetailDrawer from '../features/data-explorer/components/TransactionDetailDrawer'
import { calculateDetailStats, EMPTY_DETAIL_FILTERS, exportDetailRows, filterPayload, hasActiveFilters, rowKey, type DetailFilters } from '../utils/detail-table'
import { buildOklinkTxUrl, shortHash } from '../utils/explorer-url'
import { fieldLabel } from '../utils/field-labels'
import { orderedFields } from '../utils/field-order'
import { cleanTokenSymbol, formatDateTime, formatDecimal } from '../utils/value-format'

const TABS = [
  { key: 'activity', label: '全部活动' },
  { key: 'transactions', label: '交易' },
  { key: 'token_transfers', label: '代币转账' },
  { key: 'nft_transfers', label: 'NFT 转账' },
  { key: 'logs', label: '事件日志' },
]

const COLUMN_CLASS: Record<string, string> = {
  direction: 'col-direction', activity_type: 'col-activity', amount: 'col-amount', token_symbol: 'col-symbol',
  counterparty: 'col-address', from_address: 'col-address', to_address: 'col-address', token_address: 'col-address',
  contract_address: 'col-address', address: 'col-address', block_number: 'col-block', timestamp: 'col-time',
  tx_hash: 'col-hash', value_raw: 'col-raw', token_id: 'col-token-id', topics: 'col-topics', data: 'col-data',
}

const DIRECTION_LABEL: Record<string, string> = { in: '转入', out: '转出', self: '自转' }
const ACTIVITY_LABEL: Record<string, string> = { transfer: 'Token 转账', transaction: '链上交易' }
const FILTERABLE_AMOUNT = new Set(['activity', 'token_transfers'])
const FILTERABLE_TOKEN = new Set(['activity', 'token_transfers', 'nft_transfers', 'logs'])
const FILTERABLE_PARTIES = new Set(['activity', 'transactions', 'token_transfers', 'nft_transfers'])
// Method 筛选只对 transactions 有效：Method 字段存在于交易表（§28）
const FILTERABLE_METHOD = new Set(['transactions'])
const FLOW_COLUMNS = ['tx_hash', 'method_display', 'timestamp', 'from_display', 'to_display', 'amount_display', 'token_display']
const FLOW_LABELS: Record<string, string> = {
  tx_hash: '交易哈希', method_display: '方法', timestamp: '时间', from_display: '发送方',
  to_display: '接收方', amount_display: '数量', token_display: '代币',
}

type MetadataOption = { id: string; name: string; color?: string }
type AddressMeta = { case_names: string[]; tag_names: string[]; total_activity_count?: number; last_data_block?: number; last_sync_at?: string }

function FlowTokenIcon({ row }: { row: Record<string, unknown> }) {
  const logo = String(row.token_logo ?? '')
  const symbol = cleanTokenSymbol(String(row.token_symbol ?? row.token_name ?? '?'))
  if (logo) return <img className="flow-token-icon" src={logo} alt="" />
  const hue = [...symbol].reduce((sum, char) => sum + char.charCodeAt(0), 0) % 360
  return <span className="flow-token-icon flow-token-fallback" style={{ background: `hsl(${hue} 58% 46%)` }}>{symbol.slice(0, 1)}</span>
}

export default function AddressDetailPage() {
  const { chain = 'bsc', address = '' } = useParams()
  const [tab, setTab] = useState('activity')
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [allFilteredRows, setAllFilteredRows] = useState<Record<string, unknown>[]>([])
  const [cursor, setCursor] = useState('')
  const [loading, setLoading] = useState(false)
  const [statsLoading, setStatsLoading] = useState(false)
  const [error, setError] = useState('')
  const [filters, setFilters] = useState<DetailFilters>(EMPTY_DETAIL_FILTERS)
  const [amountMode, setAmountMode] = useState<'gte' | 'lte' | 'between'>('gte')
  const [partyField, setPartyField] = useState<'fromAddress' | 'toAddress'>('fromAddress')
  const [showTokenStats, setShowTokenStats] = useState(false)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [tokenCatalog, setTokenCatalog] = useState<TokenFilterOption[]>([])
  const [selectedRows, setSelectedRows] = useState<Map<string, Record<string, unknown>>>(new Map())
  const [exportScope, setExportScope] = useState<'all' | 'filtered' | 'selected'>('filtered')
  const [exportFormat, setExportFormat] = useState<'csv' | 'xlsx' | 'json'>('xlsx')
  const [exporting, setExporting] = useState(false)
  const [meta, setMeta] = useState<AddressMeta | null>(null)
  const [metadataEditorOpen, setMetadataEditorOpen] = useState(false)
  const [metadataLoading, setMetadataLoading] = useState(false)
  const [metadataSaving, setMetadataSaving] = useState(false)
  const [metadataError, setMetadataError] = useState('')
  const [caseOptions, setCaseOptions] = useState<MetadataOption[]>([])
  const [tagOptions, setTagOptions] = useState<MetadataOption[]>([])
  const [selectedCaseID, setSelectedCaseID] = useState('')
  const [selectedTagIDs, setSelectedTagIDs] = useState<string[]>([])
  // V1.3：交易详情抽屉（Method / Decoded Input / Logs，§42）
  const [detailTx, setDetailTx] = useState('')
  const refreshID = useRef(0)

  useEffect(() => {
    api.explorerAddresses('chain=' + chain + '&q=' + address + '&limit=5').then((data) => {
      const hit = (data.addresses ?? []).find((item: any) => item.normalized_address === address.toLowerCase())
      if (hit) setMeta(hit as never)
    }).catch(() => {})
  }, [chain, address])

  async function openMetadataEditor() {
    setMetadataEditorOpen(true)
    setMetadataLoading(true)
    setMetadataError('')
    try {
      const [caseData, tagData, catalogData] = await Promise.all([
        api.listCases(), api.listTags(), api.explorerAddresses('chain=' + chain + '&q=' + address + '&limit=5'),
      ])
      const latestMeta = (catalogData.addresses ?? []).find((item: any) => item.normalized_address === address.toLowerCase()) as AddressMeta | undefined
      if (latestMeta) setMeta(latestMeta)
      setCaseOptions(caseData.cases)
      setTagOptions(tagData.tags)
      setSelectedCaseID(caseData.cases.find((item) => latestMeta?.case_names?.includes(item.name))?.id ?? '')
      setSelectedTagIDs(tagData.tags.filter((item) => latestMeta?.tag_names?.includes(item.name)).map((item) => item.id))
    } catch (reason) {
      setMetadataError(`加载案件和标签失败：${String(reason)}`)
    } finally {
      setMetadataLoading(false)
    }
  }

  function toggleMetadataTag(id: string) {
    setSelectedTagIDs((current) => current.includes(id) ? current.filter((item) => item !== id) : [...current, id])
  }

  async function saveMetadata() {
    setMetadataSaving(true)
    setMetadataError('')
    try {
      const currentCaseIDs = caseOptions.filter((item) => meta?.case_names?.includes(item.name)).map((item) => item.id)
      const currentTagIDs = tagOptions.filter((item) => meta?.tag_names?.includes(item.name)).map((item) => item.id)
      const response = await api.updateAddressMetadata(chain, address, {
        set_primary_case: Boolean(selectedCaseID),
        add_cases: selectedCaseID ? [selectedCaseID] : [],
        remove_cases: currentCaseIDs.filter((id) => id !== selectedCaseID),
        add_tags: selectedTagIDs.filter((id) => !currentTagIDs.includes(id)),
        remove_tags: currentTagIDs.filter((id) => !selectedTagIDs.includes(id)),
      })
      setMeta(response.address)
      setMetadataEditorOpen(false)
    } catch (reason) {
      setMetadataError(`保存失败：${String(reason)}`)
    } finally {
      setMetadataSaving(false)
    }
  }

  async function fetchAll(activeFilters: DetailFilters) {
    const collected: Record<string, unknown>[] = []
    const seenCursors = new Set<string>()
    let nextCursor = ''
    do {
      const response = await api.explorerQuery({
        chain, addresses: [address], dataset: tab, filters: filterPayload(activeFilters), cursor: nextCursor, limit: 500,
      })
      collected.push(...response.rows)
      nextCursor = response.next_cursor ?? ''
      if (nextCursor && seenCursors.has(nextCursor)) throw new Error('分页游标重复，已停止统计以避免重复数据')
      if (nextCursor) seenCursors.add(nextCursor)
    } while (nextCursor)
    return collected
  }

  async function queryPage(nextCursor = '') {
    setLoading(true)
    setError('')
    try {
      const response = await api.explorerQuery({
        chain, addresses: [address], dataset: tab, filters: filterPayload(filters), cursor: nextCursor, limit: 100,
      })
      setRows(response.rows)
      setCursor(response.next_cursor ?? '')
    } catch (reason) {
      setRows([])
      setCursor('')
      setError(String(reason))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    const id = ++refreshID.current
    const timer = window.setTimeout(async () => {
      setLoading(true)
      setStatsLoading(true)
      setError('')
      try {
        const filteredRows = await fetchAll(filters)
        if (id !== refreshID.current) return
        setAllFilteredRows(filteredRows)
        setRows(filteredRows.slice(0, 100))
        const firstPage = await api.explorerQuery({
          chain, addresses: [address], dataset: tab, filters: filterPayload(filters), cursor: '', limit: 100,
        })
        if (id !== refreshID.current) return
        setCursor(firstPage.next_cursor ?? '')
      } catch (reason) {
        if (id !== refreshID.current) return
        setRows([])
        setAllFilteredRows([])
        setCursor('')
        setError(String(reason))
      } finally {
        if (id === refreshID.current) {
          setLoading(false)
          setStatsLoading(false)
        }
      }
    }, 350)
    return () => window.clearTimeout(timer)
  }, [tab, address, chain, filters, refreshNonce])

  const cols = orderedFields(rows[0] ?? allFilteredRows[0], tab)
  const flowLayout = tab !== 'logs'
  const tableCols = flowLayout ? FLOW_COLUMNS : cols
  const stats = useMemo(() => calculateDetailStats(allFilteredRows), [allFilteredRows])
  const pageKeys = rows.map(rowKey)
  const allPageSelected = pageKeys.length > 0 && pageKeys.every((key) => selectedRows.has(key))
  const tokenOptions = useMemo(() => {
    const options = new Map<string, TokenFilterOption>()
    for (const row of allFilteredRows) {
      const contract = String(row.token_address ?? row.contract_address ?? row.address ?? '')
      const symbol = cleanTokenSymbol(String(row.token_symbol ?? row.token_name ?? ''))
      const name = String(row.token_name ?? '')
      const logo = String(row.token_logo ?? '')
      const value = contract || symbol
      if (!value) continue
      const current = options.get(value)
      options.set(value, { value, symbol: symbol || '未知代币', name, address: contract, logo, count: (current?.count ?? 0) + 1 })
    }
    return [...options.values()].sort((left, right) => right.count - left.count)
  }, [allFilteredRows])

  useEffect(() => {
    if (!filters.token && tokenOptions.length > 0) setTokenCatalog(tokenOptions)
  }, [filters.token, tokenOptions])

  function updateFilter(field: keyof DetailFilters, value: string) {
    setFilters((current) => ({ ...current, [field]: value }))
  }

  function switchTab(nextTab: string) {
    setTab(nextTab)
    setFilters(EMPTY_DETAIL_FILTERS)
    setSelectedRows(new Map())
    setExportScope('filtered')
    setAmountMode('gte')
    setPartyField('fromAddress')
    setTokenCatalog([])
  }

  function toggleRow(row: Record<string, unknown>, checked: boolean) {
    setSelectedRows((current) => {
      const next = new Map(current)
      if (checked) next.set(rowKey(row), row)
      else next.delete(rowKey(row))
      return next
    })
  }

  function togglePage(checked: boolean) {
    setSelectedRows((current) => {
      const next = new Map(current)
      for (const row of rows) {
        if (checked) next.set(rowKey(row), row)
        else next.delete(rowKey(row))
      }
      return next
    })
  }

  function flowDirection(row: Record<string, unknown>) {
    const explicit = String(row.direction ?? '')
    if (explicit) return explicit
    const from = String(row.from_address ?? '').toLowerCase()
    const to = String(row.to_address ?? '').toLowerCase()
    if (from === address.toLowerCase() && to === address.toLowerCase()) return 'self'
    return from === address.toLowerCase() ? 'out' : 'in'
  }

  function flowParties(row: Record<string, unknown>) {
    const from = String(row.from_address ?? '')
    const to = String(row.to_address ?? '')
    if (from || to) return { from, to }
    const direction = flowDirection(row)
    const counterparty = String(row.counterparty ?? '')
    return direction === 'out' ? { from: address, to: counterparty } : direction === 'in' ? { from: counterparty, to: address } : { from: address, to: address }
  }

  function renderFlowAddress(value: string, label: string) {
    if (!value) return <span>—</span>
    const own = value.toLowerCase() === address.toLowerCase()
    return <span className="flow-address"><Link className={own ? 'own-address' : 'link'} to={`/data-explorer/${chain}/${value}`} title={value}>{value.slice(0, 7)}…{value.slice(-8)}</Link><button aria-label={`复制${label}`} title="复制完整地址" onClick={() => navigator.clipboard.writeText(value)}>▣</button></span>
  }

  function renderFlowCell(row: Record<string, unknown>, column: string) {
    const direction = flowDirection(row)
    const parties = flowParties(row)
    if (column === 'tx_hash') {
      const hash = String(row.tx_hash ?? '')
      const url = buildOklinkTxUrl(chain, hash)
      return <td key={column} className="mono flow-hash">{url ? <a className="link" href={url} target="_blank" rel="noopener noreferrer" title={hash}>{shortHash(hash)} ↗</a> : shortHash(hash)}</td>
    }
    if (column === 'method_display') {
      // §26/§27：Method 列不留空，Unknown 原样显示 Selector，Hover 展示来源与验证状态
      const hash = String(row.tx_hash ?? '')
      return (
        <td key={column} className="method-column">
          <MethodCell row={row} onOpenDetail={hash ? () => setDetailTx(hash) : undefined} />
        </td>
      )
    }
    if (column === 'timestamp') return <td key={column} className="flow-time" title={String(row.timestamp ?? '')}>{formatDateTime(String(row.timestamp ?? ''))}</td>
    if (column === 'from_display') return <td key={column}>{renderFlowAddress(parties.from, '发送方')}</td>
    if (column === 'to_display') return <td key={column}><span className="flow-recipient"><span className={`flow-direction flow-direction-${direction}`}>{direction === 'out' ? 'Out' : direction === 'in' ? 'In' : 'Self'}</span>{renderFlowAddress(parties.to, '接收方')}</span></td>
    if (column === 'amount_display') {
      const amount = String(row.amount ?? row.value_raw ?? row.amount_raw ?? '')
      const signed = amount && amount !== '0' ? `${direction === 'out' ? '-' : direction === 'in' ? '+' : ''}${formatDecimal(amount.replace(/^[-+]/, ''))}` : formatDecimal(amount || '0')
      const usd = String(row.amount_usd ?? '')
      return <td key={column} className="flow-amount" title={`完整数量：${amount}`}><b>{signed}</b>{usd && <small>${formatDecimal(usd)}</small>}</td>
    }
    const symbol = cleanTokenSymbol(String(row.token_symbol ?? (tab === 'transactions' ? chain.toUpperCase() : '未知代币')))
    const name = String(row.token_name ?? (tab === 'transactions' ? `${chain.toUpperCase()} 原生币` : '名称未收录'))
    const contract = String(row.token_address ?? row.contract_address ?? '')
    return <td key={column}><span className="flow-token" title={contract}><FlowTokenIcon row={{ ...row, token_symbol: symbol }} /><span><b>{symbol}</b><small>{name}</small></span></span></td>
  }

  async function doExport() {
    setExporting(true)
    setError('')
    try {
      const exportRows = exportScope === 'all'
        ? await fetchAll(EMPTY_DETAIL_FILTERS)
        : exportScope === 'selected' ? [...selectedRows.values()] : allFilteredRows
      if (exportRows.length === 0) throw new Error('当前导出范围没有数据')
      const suffix = exportScope === 'all' ? '全部' : exportScope === 'filtered' ? '筛选结果' : '已勾选'
      await exportDetailRows(exportRows, tab, exportFormat, `${chain}-${address.slice(0, 10)}-${tab}-${suffix}`)
    } catch (reason) {
      setError(String(reason))
    } finally {
      setExporting(false)
    }
  }

  return (
    <div className="page">
      <div className="row between detail-head">
        <div>
          <h1>{chain.toUpperCase()} · {address}</h1>
          <div className="sub address-meta-line">
            <span>案件：{meta?.case_names?.join('、') || '—'} ｜ 标签：{meta?.tag_names?.length ? meta.tag_names.map((tag) => <span key={tag} className="badge b-RUNNING">{tag}</span>) : '—'}</span>
            <button className="metadata-edit-button" aria-label="修改案件和标签" title="修改案件和标签" onClick={openMetadataEditor}>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 16.5V20h3.5L18.7 8.8l-3.5-3.5L4 16.5Zm17.4-10.4a1 1 0 0 0 0-1.4l-2.1-2.1a1 1 0 0 0-1.4 0l-1.6 1.6 3.5 3.5 1.6-1.6Z" /></svg>
            </button>
          </div>
          <p className="muted">本地数据：更新至 Block {meta?.last_data_block ?? '—'} · 最后同步 {meta?.last_sync_at ? new Date(meta.last_sync_at).toLocaleString() : '—'}</p>
        </div>
        <Link to="/data-explorer" className="ghost">← 数据浏览</Link>
      </div>

      <div className="card detail-tabs"><div className="row gap wrap">{TABS.map((item) => <button key={item.key} className={tab === item.key ? 'primary' : 'ghost'} onClick={() => switchTab(item.key)}>{item.label}</button>)}</div></div>

      <div className="card detail-card">
        <section className="detail-tools compact-filter-bar" aria-label="数据筛选与导出">
          <div className="compact-filter-controls">
            {FILTERABLE_TOKEN.has(tab) && <TokenFilterDropdown value={filters.token} options={tokenCatalog} onConfirm={(value) => updateFilter('token', value)} />}
            {FILTERABLE_AMOUNT.has(tab) && <label className="compact-pill amount-pill"><span>数量:</span><select aria-label="数量条件" value={amountMode} onChange={(event) => { const mode = event.target.value as typeof amountMode; setAmountMode(mode); setFilters((current) => ({ ...current, minAmount: '', maxAmount: '' })) }}><option value="gte">≥</option><option value="lte">≤</option><option value="between">区间</option></select><input aria-label="最小数量" inputMode="decimal" placeholder={amountMode === 'between' ? '最小' : '数量'} value={amountMode === 'lte' ? filters.maxAmount : filters.minAmount} onChange={(event) => updateFilter(amountMode === 'lte' ? 'maxAmount' : 'minAmount', event.target.value)} />{amountMode === 'between' && <><i>—</i><input aria-label="最大数量" inputMode="decimal" placeholder="最大" value={filters.maxAmount} onChange={(event) => updateFilter('maxAmount', event.target.value)} /></>}</label>}
            <label className="compact-pill date-pill"><input aria-label="开始时间" type="date" value={filters.fromTime} onChange={(event) => updateFilter('fromTime', event.target.value)} /><i>→</i><input aria-label="结束时间" type="date" value={filters.toTime} onChange={(event) => updateFilter('toTime', event.target.value)} /></label>
            {FILTERABLE_PARTIES.has(tab) && <label className="compact-pill party-pill"><select aria-label="地址类型" value={partyField} onChange={(event) => { const field = event.target.value as typeof partyField; setPartyField(field); setFilters((current) => ({ ...current, fromAddress: '', toAddress: '' })) }}><option value="fromAddress">发送方</option><option value="toAddress">接收方</option></select><input aria-label={partyField === 'fromAddress' ? '发送方' : '接收方'} className="mono" placeholder="输入地址搜索" value={filters[partyField]} onChange={(event) => updateFilter(partyField, event.target.value)} /></label>}
            {FILTERABLE_METHOD.has(tab) && <label className="compact-pill method-pill"><span>Method:</span><input aria-label="Method 筛选" className="mono" placeholder="transfer / 0xa9059cbb / unknown" value={filters.method} onChange={(event) => updateFilter('method', event.target.value)} /></label>}
            <button className="compact-refresh" aria-label="刷新筛选结果" title="刷新" onClick={() => setRefreshNonce((value) => value + 1)}>↻</button>
            {hasActiveFilters(filters) && <button className="compact-reset" onClick={() => setFilters(EMPTY_DETAIL_FILTERS)}>清除</button>}
          </div>
          <label className="token-stats-toggle"><input type="checkbox" checked={showTokenStats} onChange={(event) => setShowTokenStats(event.target.checked)} /><span className="toggle-track" /><b>代币统计数据</b></label>
        </section>

        <section className="detail-summary-bar" aria-label="筛选统计与导出">
          <div className="detail-stats">
            <div><span>结果数量</span><strong>{statsLoading ? '统计中…' : stats.count.toLocaleString()}</strong></div>
            <div><span>转入 / 转出</span><strong>{stats.incoming.toLocaleString()} / {stats.outgoing.toLocaleString()}</strong></div>
            {showTokenStats && <><div><span>代币种类</span><strong>{stats.tokenCount.toLocaleString()}</strong></div><div className="token-stat-list"><span>按代币合计</span><div>{stats.tokens.length === 0 ? <b>—</b> : stats.tokens.slice(0, 3).map((token) => <b key={token.key} title={token.address}>{token.symbol} {formatDecimal(token.total)} <small>({token.count}笔)</small></b>)}{stats.tokens.length > 3 && <em>另有 {stats.tokens.length - 3} 种</em>}</div></div></>}
          </div>
          <div className="detail-export">
            <span className="selected-count">已选 {selectedRows.size.toLocaleString()} 条</span>
            <select aria-label="导出范围" value={exportScope} onChange={(event) => setExportScope(event.target.value as typeof exportScope)}><option value="all">导出全部</option><option value="filtered">导出筛选结果</option><option value="selected">导出勾选数据</option></select>
            <select aria-label="导出格式" value={exportFormat} onChange={(event) => setExportFormat(event.target.value as typeof exportFormat)}><option value="xlsx">Excel (.xlsx)</option><option value="csv">CSV (.csv)</option><option value="json">JSON (.json)</option></select>
            <button className="primary" disabled={exporting || (exportScope === 'selected' && selectedRows.size === 0)} onClick={doExport}>{exporting ? '正在导出…' : '导出'}</button>
          </div>
        </section>

        {error && <div className="error">{error}</div>}
        <div className="detail-table-wrap">
          <table className={`table detail-table ${flowLayout ? 'flow-table' : ''}`}>
            <colgroup><col className="col-select" />{tableCols.map((column) => <col key={column} className={flowLayout ? `flow-col-${column}` : (COLUMN_CLASS[column] ?? '')} />)}</colgroup>
            <thead><tr><th className="select-cell"><input aria-label="勾选当前页" type="checkbox" checked={allPageSelected} onChange={(event) => togglePage(event.target.checked)} /></th>{tableCols.map((column) => <th key={column}>{flowLayout ? FLOW_LABELS[column] : fieldLabel(column)}</th>)}</tr></thead>
            <tbody>
              {rows.map((row, index) => <tr key={rowKey(row) || index} className={selectedRows.has(rowKey(row)) ? 'selected-row' : ''}>
                <td className="select-cell"><input aria-label={`勾选第 ${index + 1} 行`} type="checkbox" checked={selectedRows.has(rowKey(row))} onChange={(event) => toggleRow(row, event.target.checked)} /></td>
                {flowLayout ? tableCols.map((column) => renderFlowCell(row, column)) : cols.map((column) => {
                  const value = row[column]
                  const text = value === null || value === undefined ? '' : String(value)
                  if (column === 'amount') { const raw = String(row.amount_raw ?? ''); return <td key={column} className="amount-value" title={raw ? `完整金额：${text}\n原始值：${raw}` : `完整金额：${text}`}>{formatDecimal(text)}</td> }
                  if (column === 'timestamp' || column === 'source_ingested_at') return <td key={column} className="time-value" title={text}>{formatDateTime(text)}</td>
                  if (column === 'direction') return <td key={column}><span className={'direction direction-' + text}>{DIRECTION_LABEL[text] ?? text}</span></td>
                  if (column === 'activity_type') return <td key={column}>{ACTIVITY_LABEL[text] ?? text}</td>
                  if (column === 'token_symbol') return <td key={column} className="token-symbol" title={text}>{cleanTokenSymbol(text)}</td>
                  if (column === 'tx_hash' && text.startsWith('0x')) { const url = buildOklinkTxUrl(chain, text); return <td key={column} className="mono">{url ? <a className="link" href={url} target="_blank" rel="noopener noreferrer">{shortHash(text)} ↗</a> : text.slice(0, 16) + '…'}</td> }
                  if (['from_address', 'to_address', 'counterparty', 'address', 'token_address', 'contract_address'].includes(column) && text.startsWith('0x')) return <td key={column} className="mono" title={text}>{text.slice(0, 10)}…</td>
                  return <td key={column} className={column.includes('hash') ? 'mono' : ''} title={text.length > 60 ? text : undefined}>{text.length > 44 ? text.slice(0, 22) + '…' + text.slice(-8) : text}</td>
                })}
              </tr>)}
              {rows.length === 0 && <tr><td colSpan={Math.max(tableCols.length + 1, 1)} className="muted center">{loading ? '加载中…' : '没有符合筛选条件的数据'}</td></tr>}
            </tbody>
          </table>
        </div>
        {cursor && <div className="detail-pagination row between"><span className="muted">当前显示本页 100 条，统计与导出覆盖全部筛选结果</span><button className="ghost" disabled={loading} onClick={() => queryPage(cursor)}>下一页</button></div>}
      </div>

      {detailTx && <TransactionDetailDrawer chain={chain} txHash={detailTx} onClose={() => setDetailTx('')} />}
      {metadataEditorOpen && <div className="metadata-dialog-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !metadataSaving) setMetadataEditorOpen(false) }}>
        <section className="metadata-dialog" role="dialog" aria-modal="true" aria-label="修改案件和标签">
          <div className="metadata-dialog-head row between">
            <div><h2>修改案件和标签</h2><p>为当前地址选择所属案件和标签</p></div>
            <button className="metadata-dialog-close" aria-label="关闭" disabled={metadataSaving} onClick={() => setMetadataEditorOpen(false)}>×</button>
          </div>
          {metadataLoading ? <div className="metadata-dialog-loading">正在加载…</div> : <div className="metadata-dialog-body">
            <label className="metadata-case-field"><span>案件</span><select aria-label="案件" value={selectedCaseID} onChange={(event) => setSelectedCaseID(event.target.value)}><option value="">不关联案件</option>{caseOptions.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
            <fieldset className="metadata-tag-field"><legend>标签</legend><div className="metadata-tag-list">{tagOptions.map((item) => <label key={item.id} className={selectedTagIDs.includes(item.id) ? 'selected' : ''}><input type="checkbox" aria-label={`标签 ${item.name}`} checked={selectedTagIDs.includes(item.id)} onChange={() => toggleMetadataTag(item.id)} /><span className="metadata-tag-dot" style={{ background: item.color || '#2563eb' }} /><b>{item.name}</b></label>)}{tagOptions.length === 0 && <span className="muted">暂无可选标签</span>}</div></fieldset>
          </div>}
          {metadataError && <div className="metadata-dialog-error">{metadataError}</div>}
          <div className="metadata-dialog-actions"><button disabled={metadataSaving} onClick={() => setMetadataEditorOpen(false)}>取消</button><button className="primary" disabled={metadataLoading || metadataSaving || Boolean(metadataError && caseOptions.length === 0 && tagOptions.length === 0)} onClick={saveMetadata}>{metadataSaving ? '保存中…' : '保存'}</button></div>
        </section>
      </div>}
    </div>
  )
}
