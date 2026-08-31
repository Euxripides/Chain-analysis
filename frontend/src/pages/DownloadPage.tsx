import { useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import * as XLSX from 'xlsx'
import { api } from '../api/client'
import { CHAINS, DATASETS } from '../types'

interface RangeSpec {
  type: 'full' | 'block' | 'time'
  from_block?: number
  to_block?: number
  from?: string
  to?: string
}

const ADDR_RE = /^0x[0-9a-fA-F]{40}$/

export default function DownloadPage() {
  const navigate = useNavigate()
  const [addressText, setAddressText] = useState('')
  const [chain, setChain] = useState('bsc')
  const [rangeType, setRangeType] = useState<'full' | 'block' | 'time'>('full')
  const [fromBlock, setFromBlock] = useState('')
  const [toBlock, setToBlock] = useState('')
  const [fromTime, setFromTime] = useState('')
  const [toTime, setToTime] = useState('')
  const [datasets, setDatasets] = useState<string[]>(['transactions', 'token_transfers', 'nft_transfers', 'logs'])
  const [preview, setPreview] = useState<{ overall_ratio: number; items: { subject: string; dataset: string; ratio: number }[] } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const addresses = useMemo(() => {
    const lines = addressText.split(/[\n,;\s]+/).map((s) => s.trim().toLowerCase()).filter(Boolean)
    const uniq = [...new Set(lines)]
    const valid = uniq.filter((a) => ADDR_RE.test(a))
    const dup = lines.length - uniq.length
    const bad = uniq.length - valid.length
    return { valid, dup, bad }
  }, [addressText])

  function handleFile(file: File) {
    const reader = new FileReader()
    reader.onload = () => {
      try {
        let text = ''
        if (file.name.toLowerCase().endsWith('.csv') || file.name.toLowerCase().endsWith('.txt')) {
          text = String(reader.result ?? '')
        } else {
          const wb = XLSX.read(reader.result, { type: 'array' })
          const ws = wb.Sheets[wb.SheetNames[0]]
          text = XLSX.utils.sheet_to_csv(ws)
        }
        setAddressText((prev) => (prev ? prev + '\n' : '') + text)
      } catch (e) {
        setError('文件解析失败: ' + String(e))
      }
    }
    if (file.name.toLowerCase().endsWith('.xlsx') || file.name.toLowerCase().endsWith('.xls')) {
      reader.readAsArrayBuffer(file)
    } else {
      reader.readAsText(file)
    }
  }

  function buildRange(): RangeSpec {
    if (rangeType === 'block') {
      return { type: 'block', from_block: Number(fromBlock), to_block: Number(toBlock) }
    }
    if (rangeType === 'time') {
      return { type: 'time', from: new Date(fromTime).toISOString(), to: new Date(toTime).toISOString() }
    }
    return { type: 'full' }
  }

  async function doPreview() {
    setError('')
    setBusy(true)
    try {
      const resp = await api.preview({
        subjects: addresses.valid, chain, datasets, range: buildRange(),
      })
      setPreview(resp)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  async function doCreate() {
    setError('')
    setBusy(true)
    try {
      const resp = await api.ensure({
        source: 'user_download', priority: 100,
        subjects: addresses.valid, chain, datasets, range: buildRange(),
        reuse_policy: 'prefer_local',
      })
      if (resp.job_id) navigate('/jobs/' + resp.job_id)
    } catch (e) {
      setError(String(e))
    } finally {
      setBusy(false)
    }
  }

  const toggleDataset = (d: string) =>
    setDatasets((cur) => (cur.includes(d) ? cur.filter((x) => x !== d) : [...cur, d]))

  return (
    <div className="page">
      <h1>地址数据下载</h1>
      <p className="sub">Coverage-aware 下载：已有本地数据自动复用，只请求缺口。Provider：Ankr Advanced API</p>

      <div className="card">
        <div className="card-title">地址</div>
        <textarea
          value={addressText}
          onChange={(e) => setAddressText(e.target.value)}
          placeholder="每行一个地址，或粘贴 CSV/TXT 内容"
          rows={6}
        />
        <div className="row gap" style={{ marginTop: 8 }}>
          <span className="muted">{addresses.valid.length} 个有效 · {addresses.dup} 个重复 · {addresses.bad} 个错误</span>
          <button className="ghost" onClick={() => fileRef.current?.click()}>导入 CSV / TXT / XLSX</button>
          <input ref={fileRef} type="file" accept=".csv,.txt,.xlsx,.xls" hidden
            onChange={(e) => { const f = e.target.files?.[0]; if (f) handleFile(f); e.target.value = '' }} />
        </div>
      </div>

      <div className="row gap">
        <div className="card flex-1">
          <div className="card-title">链</div>
          <select value={chain} onChange={(e) => setChain(e.target.value)}>
            {CHAINS.map((c) => <option key={c} value={c}>{c.toUpperCase()}</option>)}
          </select>
        </div>
        <div className="card flex-2">
          <div className="card-title">范围</div>
          <div className="row gap wrap">
            <label><input type="radio" checked={rangeType === 'full'} onChange={() => setRangeType('full')} /> 全量</label>
            <label><input type="radio" checked={rangeType === 'block'} onChange={() => setRangeType('block')} /> 区块范围</label>
            <label><input type="radio" checked={rangeType === 'time'} onChange={() => setRangeType('time')} /> 时间范围</label>
          </div>
          {rangeType === 'block' && (
            <div className="row gap" style={{ marginTop: 8 }}>
              <input placeholder="from block" value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} />
              <input placeholder="to block" value={toBlock} onChange={(e) => setToBlock(e.target.value)} />
            </div>
          )}
          {rangeType === 'time' && (
            <div className="row gap" style={{ marginTop: 8 }}>
              <input type="datetime-local" value={fromTime} onChange={(e) => setFromTime(e.target.value)} />
              <input type="datetime-local" value={toTime} onChange={(e) => setToTime(e.target.value)} />
            </div>
          )}
        </div>
      </div>

      <div className="card">
        <div className="card-title">Dataset</div>
        <div className="row gap wrap">
          {DATASETS.map((d) => (
            <label key={d} className="chip">
              <input type="checkbox" checked={datasets.includes(d)} onChange={() => toggleDataset(d)} /> {d}
            </label>
          ))}
        </div>
      </div>

      {preview && (
        <div className="card">
          <div className="card-title">本地 Coverage 预览</div>
          <div className="meter">
            <div className="meter-fill" style={{ width: (preview.overall_ratio * 100).toFixed(1) + '%' }} />
          </div>
          <div className="row gap between" style={{ marginTop: 6 }}>
            <span>预计复用：{(preview.overall_ratio * 100).toFixed(1)}%</span>
            <span>预计下载：{((1 - preview.overall_ratio) * 100).toFixed(1)}%</span>
          </div>
          <table className="table" style={{ marginTop: 10 }}>
            <thead><tr><th>地址</th><th>Dataset</th><th>复用率</th></tr></thead>
            <tbody>
              {preview.items.slice(0, 50).map((it, i) => (
                <tr key={i}><td className="mono">{it.subject.slice(0, 10)}…</td><td>{it.dataset}</td><td>{(it.ratio * 100).toFixed(1)}%</td></tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {error && <div className="error">{error}</div>}

      <div className="row gap" style={{ marginTop: 16 }}>
        <button onClick={doPreview} disabled={busy || addresses.valid.length === 0}>Coverage 预览</button>
        <button className="primary" onClick={doCreate} disabled={busy || addresses.valid.length === 0 || datasets.length === 0}>
          创建下载任务
        </button>
      </div>
    </div>
  )
}
