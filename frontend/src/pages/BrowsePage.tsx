import { useState } from 'react'
import { api } from '../api/client'
import { CHAINS, fmtBytes } from '../types'

const TITLES: Record<string, string> = {
  transactions: 'Transactions',
  token_transfers: 'Token Transfers',
  nft_transfers: 'NFT Transfers',
  logs: 'Logs',
}

const LIMIT = 100

export default function BrowsePage({ dataset }: { dataset: string }) {
  const [chain, setChain] = useState('bsc')
  const [subject, setSubject] = useState('')
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [count, setCount] = useState(0)
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [exporting, setExporting] = useState(false)

  const cols = rows.length > 0 ? Object.keys(rows[0]) : []

  async function query(nextOffset = 0) {
    if (!subject.trim()) return
    setLoading(true)
    setError('')
    try {
      const resp = await api.queryRows(dataset, chain, subject.trim().toLowerCase(), LIMIT, nextOffset)
      setRows(resp.rows)
      setCount(resp.count)
      setOffset(nextOffset)
    } catch (e) {
      setError(String(e))
    } finally {
      setLoading(false)
    }
  }

  async function exportCsv() {
    if (!subject.trim()) return
    setExporting(true)
    try {
      const exp = await api.createExportBySubject(dataset, subject.trim().toLowerCase(), chain)
      const timer = setInterval(async () => {
        const cur = await api.getExport(exp.id)
        if (cur.status === 'COMPLETED') {
          clearInterval(timer)
          setExporting(false)
          window.open('/api/v1/exports/' + exp.id + '/download', '_blank')
        } else if (cur.status === 'FAILED') {
          clearInterval(timer)
          setExporting(false)
          alert('导出失败: ' + cur.error)
        }
      }, 1000)
    } catch (e) {
      setExporting(false)
      alert(String(e))
    }
  }

  return (
    <div className="page">
      <h1>{TITLES[dataset] ?? dataset}</h1>
      <p className="sub">统一数据面查询（ClickHouse FINAL 去重）· 支持按地址过滤与 CSV 导出</p>

      <div className="card">
        <div className="row gap">
          <select style={{ maxWidth: 160 }} value={chain} onChange={(e) => setChain(e.target.value)}>
            {CHAINS.map((c) => <option key={c} value={c}>{c.toUpperCase()}</option>)}
          </select>
          <input className="flex-2 mono" placeholder="地址（0x…，必填）" value={subject}
            onChange={(e) => setSubject(e.target.value)} />
          <button className="primary" disabled={loading || !subject.trim()} onClick={() => query(0)}>查询</button>
          <button disabled={exporting || !subject.trim()} onClick={exportCsv}>{exporting ? '导出中…' : '导出 CSV'}</button>
        </div>
      </div>

      {error && <div className="error">{error}</div>}

      <div className="card">
        <div className="row between">
          <div className="card-title" style={{ marginBottom: 0 }}>
            {count > 0 ? count.toLocaleString() + ' 行（当前页 ' + (offset + 1) + ' - ' + Math.min(offset + LIMIT, count) + '）' : '结果'}
          </div>
          <div className="row gap">
            <button className="sm" disabled={offset === 0 || loading} onClick={() => query(offset - LIMIT)}>上一页</button>
            <button className="sm" disabled={rows.length < LIMIT || loading} onClick={() => query(offset + LIMIT)}>下一页</button>
          </div>
        </div>
        <div style={{ overflowX: 'auto', marginTop: 8 }}>
          <table className="table">
            <thead>
              <tr>{cols.map((c) => <th key={c}>{c}</th>)}</tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>
                  {cols.map((c) => {
                    const v = r[c]
                    let text = v === null || v === undefined ? '' : String(v)
                    if (c === 'downloaded_bytes' || c === 'bytes') text = fmtBytes(Number(v) || 0)
                    return (
                      <td key={c} className={(c === 'tx_hash' || c === 'address' || c === 'block_hash') ? 'mono' : ''}
                        title={text.length > 60 ? text : undefined}>
                        {text.length > 40 ? text.slice(0, 20) + '…' + text.slice(-12) : text}
                      </td>
                    )
                  })}
                </tr>
              ))}
              {rows.length === 0 && (
                <tr><td colSpan={Math.max(cols.length, 1)} className="muted center">
                  {loading ? '查询中…' : '输入地址后点击查询'}
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
