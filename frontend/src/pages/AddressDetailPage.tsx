import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api } from '../api/client'
import { buildOklinkTxUrl, shortHash } from '../utils/explorer-url'

const TABS = [
  { key: 'activity', label: '全部活动' },
  { key: 'transactions', label: 'Transactions' },
  { key: 'token_transfers', label: 'Token Transfers' },
  { key: 'nft_transfers', label: 'NFT Transfers' },
  { key: 'logs', label: 'Logs' },
]

export default function AddressDetailPage() {
  const { chain = 'bsc', address = '' } = useParams()
  const [tab, setTab] = useState('activity')
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [cursor, setCursor] = useState('')
  const [meta, setMeta] = useState<{ case_names: string[]; tag_names: string[]; total_activity_count?: number; last_data_block?: number; last_sync_at?: string } | null>(null)

  useEffect(() => {
    api.explorerAddresses('chain=' + chain + '&q=' + address + '&limit=5').then((d) => {
      const hit = (d.addresses ?? []).find((a: any) => a.normalized_address === address.toLowerCase())
      if (hit) setMeta(hit as never)
    }).catch(() => {})
  }, [chain, address])

  async function query(nextCursor = '') {
    const resp = await api.explorerQuery({
      chain, addresses: [address], dataset: tab,
      cursor: nextCursor, limit: 100,
    })
    setRows(resp.rows)
    setCursor(resp.next_cursor ?? '')
  }

  useEffect(() => { query('') }, [tab, address, chain])

  const cols = rows.length > 0 ? Object.keys(rows[0]).filter((c) => !['matched_addresses', 'case_names', 'tag_names'].includes(c)) : []

  return (
    <div className="page">
      <div className="row between">
        <div>
          <h1>{chain.toUpperCase()} · {address}</h1>
          <p className="sub">
            案件：{meta?.case_names?.join('、') || '—'} ｜ 标签：{meta?.tag_names?.map((t) => <span key={t} className="badge b-RUNNING">{t}</span>) || '—'}
          </p>
          <p className="muted">
            本地数据：更新至 Block {meta?.last_data_block ?? '—'} · 最后同步 {meta?.last_sync_at ? new Date(meta.last_sync_at).toLocaleString() : '—'}
          </p>
        </div>
        <Link to="/data-explorer" className="ghost">← 数据浏览</Link>
      </div>

      <div className="card">
        <div className="row gap wrap">
          {TABS.map((t) => (
            <button key={t.key} className={tab === t.key ? 'primary' : 'ghost'} onClick={() => setTab(t.key)}>{t.label}</button>
          ))}
        </div>
      </div>

      <div className="card">
        <div style={{ overflowX: 'auto' }}>
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
                    if (c === 'tx_hash' && text.startsWith('0x')) {
                      const url = buildOklinkTxUrl(chain, text)
                      return (
                        <td key={c} className="mono">
                          {url
                            ? <a className="link" href={url} target="_blank" rel="noopener noreferrer">{shortHash(text)} ↗</a>
                            : text.slice(0, 16) + '…'}
                        </td>
                      )
                    }
                    if ((c === 'from_address' || c === 'to_address' || c === 'address' || c === 'token_address') && text.startsWith('0x')) {
                      return <td key={c} className="mono" title={text}>{text.slice(0, 10)}…</td>
                    }
                    return <td key={c} className={c.includes('hash') ? 'mono' : ''} title={text.length > 60 ? text : undefined}>{text.length > 44 ? text.slice(0, 22) + '…' + text.slice(-8) : text}</td>
                  })}
                </tr>
              ))}
              {rows.length === 0 && <tr><td colSpan={Math.max(cols.length, 1)} className="muted center">无数据（可用「数据浏览」页对该地址执行更新）</td></tr>}
            </tbody>
          </table>
        </div>
        {cursor && <button className="ghost" onClick={() => query(cursor)}>下一页</button>}
      </div>
    </div>
  )
}
