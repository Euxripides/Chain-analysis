import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '../api/client'
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

function HashCell({ chain, text }: { chain: string; text: string }) {
  const url = buildOklinkTxUrl(chain, text)
  if (!url) return <span className="mono" title={text}>{shortHash(text)}</span>
  return (
    <a className="link mono" href={url} target="_blank" rel="noopener noreferrer" title={text}>
      {shortHash(text)} ↗
    </a>
  )
}

export default function CombinedDataPage() {
  const [sp] = useSearchParams()
  const chain = sp.get('chain') ?? 'bsc'
  const addresses = (sp.get('addresses') ?? '').split(',').filter(Boolean)
  const [tab, setTab] = useState('activity')
  const [rows, setRows] = useState<Record<string, unknown>[]>([])
  const [cursor, setCursor] = useState('')

  async function query(nextCursor = '') {
    const resp = await api.explorerQuery({ chain, addresses, dataset: tab, cursor: nextCursor, limit: 100 })
    setRows(resp.rows)
    setCursor(resp.next_cursor ?? '')
  }

  useEffect(() => { query('') }, [tab, chain, addresses.join(',')])

  const cols = orderedFields(rows[0], tab, {
    exclude: ['matched_addresses', 'case_names', 'tag_names', 'token_logo', 'token_name'],
    appendRemaining: true,
  })

  return (
    <div className="page">
      <div className="row between">
        <div>
          <h1>组合数据（{addresses.length} 个地址）</h1>
          <p className="sub mono">{chain.toUpperCase()} · {addresses.slice(0, 3).map((a) => a.slice(0, 10)).join(' / ')}{addresses.length > 3 ? ' …' : ''}</p>
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
              <tr>
                <th>匹配地址</th><th>案件</th><th>标签</th>
                {cols.map((c) => <th key={c}>{fieldLabel(c)}</th>)}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr key={i}>
                  <td className="mono">{String((r.matched_addresses as string[])?.map((a) => a.slice(0, 8)).join(', ') || '')}</td>
                  <td>{String((r.case_names as string[])?.join('、') || '—')}</td>
                  <td>{String((r.tag_names as string[])?.join('、') || '—')}</td>
                  {cols.map((c) => {
                    const v = r[c]
                    const text = v === null || v === undefined ? '' : String(v)
                    if (c === 'amount') {
                      return <td key={c} className="amount-value" title={`完整金额：${text}`}>{formatDecimal(text)}</td>
                    }
                    if (c === 'timestamp' || c === 'source_ingested_at') {
                      return <td key={c} className="time-value" title={text}>{formatDateTime(text)}</td>
                    }
                    if (c === 'tx_hash' && text.startsWith('0x')) {
                      return <td key={c}><HashCell chain={chain} text={text} /></td>
                    }
                    if ((c === 'from_address' || c === 'to_address' || c === 'address' || c === 'token_address' || c === 'contract_address') && text.startsWith('0x')) {
                      return <td key={c} className="mono" title={text}>{text.slice(0, 10)}…</td>
                    }
                    if (c === 'token_symbol' && text) {
                      return <td key={c} className="token-symbol" title={text}>{cleanTokenSymbol(text)}</td>
                    }
                    return <td key={c} title={text.length > 60 ? text : undefined}>{text.length > 44 ? text.slice(0, 22) + '…' + text.slice(-8) : text}</td>
                  })}
                </tr>
              ))}
              {rows.length === 0 && <tr><td colSpan={cols.length + 3} className="muted center">无数据</td></tr>}
            </tbody>
          </table>
        </div>
        {cursor && <button className="ghost" onClick={() => query(cursor)}>下一页</button>}
      </div>
    </div>
  )
}
