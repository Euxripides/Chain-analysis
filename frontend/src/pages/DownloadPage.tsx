import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api } from '../api/client'
import ChainSelect from '../components/ChainSelect'
import { CHAINS, DATASETS } from '../types'
import { datasetLabel } from '../utils/field-labels'

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

  // V1.2：案件/标签（§22/§24）
  const [cases, setCases] = useState<{ id: string; name: string }[]>([])
  const [tags, setTags] = useState<{ id: string; name: string }[]>([])
  const [caseName, setCaseName] = useState('')
  const [tagNames, setTagNames] = useState<string[]>([])
  const [subjectMeta, setSubjectMeta] = useState<Record<string, { case_name: string; tags: string[] }>>({})
  const [subjectChains, setSubjectChains] = useState<Record<string, string>>({})

  useEffect(() => { api.listCases().then((d) => setCases(d.cases)).catch(() => {}) }, [])
  useEffect(() => { api.listTags().then((d) => setTags(d.tags)).catch(() => {}) }, [])

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
    reader.onload = async () => {
      try {
        const lowerName = file.name.toLowerCase()
        let addrLines: string[] = []
        const meta: Record<string, { case_name: string; tags: string[] }> = {}
        const chains: Record<string, string> = {}
        if (lowerName.endsWith('.txt')) {
          addrLines = String(reader.result ?? '').split(/[\r\n,;\s]+/).map((value) => value.trim().toLowerCase()).filter(Boolean)
        } else {
          // XLSX parser also handles quoted CSV correctly; load it only while importing.
          const XLSX = await import('xlsx')
          const wb = XLSX.read(reader.result, { type: lowerName.endsWith('.csv') ? 'string' : 'array' })
          const ws = wb.Sheets[wb.SheetNames[0]]
          const records = XLSX.utils.sheet_to_json<Record<string, unknown>>(ws, { defval: '', raw: false })
          const hasAddressHeader = records.length > 0 && Object.keys(records[0]).some((key) => key.trim().toLowerCase() === 'address')
          if (hasAddressHeader) {
            for (const raw of records) {
              const record = Object.fromEntries(Object.entries(raw).map(([key, value]) => [key.trim().toLowerCase(), String(value ?? '').trim()]))
              const addr = (record.address ?? '').toLowerCase()
              if (!addr) continue
              addrLines.push(addr)
              const caseN = record.case ?? ''
              const tagList = (record.tags ?? '').split(/[|;；]/).map((value) => value.trim()).filter(Boolean)
              if (caseN || tagList.length) meta[addr] = { case_name: caseN, tags: tagList }
              const rowChain = (record.chain ?? '').toLowerCase()
              if (rowChain && CHAINS.includes(rowChain)) chains[addr] = rowChain
            }
          } else {
            addrLines = XLSX.utils.sheet_to_csv(ws).split(/[\r\n,;\s]+/).map((value) => value.trim().toLowerCase()).filter(Boolean)
          }
        }
        setSubjectMeta((prev) => ({ ...prev, ...meta }))
        setSubjectChains((prev) => ({ ...prev, ...chains }))
        setAddressText((prev) => (prev ? prev + '\n' : '') + addrLines.join('\n'))
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

  async function createCase() {
    const name = prompt('新案件名')?.trim()
    if (!name) return
    setError('')
    try {
      const created = await api.createCase(name)
      setCases((cur) => [...cur, created].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN')))
      setCaseName(created.name)
    } catch (e) {
      setError('新建案件失败: ' + String(e))
    }
  }

  async function createTag() {
    const name = prompt('新标签名')?.trim()
    if (!name) return
    setError('')
    try {
      const created = await api.createTag(name)
      setTags((cur) => [...cur, created].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN')))
      setTagNames((cur) => [...new Set([...cur, created.name])])
    } catch (e) {
      setError('新建标签失败: ' + String(e))
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
			const groups = groupAddressesByChain(addresses.valid, chain, subjectChains)
			const responses = await Promise.all(Object.entries(groups).map(([groupChain, subjects]) => api.preview({
				subjects, chain: groupChain, datasets, range: buildRange(),
			})))
			const items = responses.flatMap((response) => response.items)
			const overallRatio = items.length > 0 ? items.reduce((sum, item) => sum + item.ratio, 0) / items.length : 0
			setPreview({ overall_ratio: overallRatio, items })
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
			const groups = groupAddressesByChain(addresses.valid, chain, subjectChains)
			const responses = await Promise.all(Object.entries(groups).map(([groupChain, subjects]) => api.ensure({
				source: 'user_download', priority: 100,
				subjects, chain: groupChain, datasets, range: buildRange(),
				reuse_policy: 'prefer_local',
				case_name: caseName || undefined,
				tags: tagNames.length ? tagNames : undefined,
				create_missing_meta: false,
				subject_meta: Object.fromEntries(subjects.filter((addr) => subjectMeta[addr]).map((addr) => [addr, subjectMeta[addr]])),
			})))
			const jobIDs = responses.flatMap((response) => response.job_id ? [response.job_id] : [])
			if (jobIDs.length === 1) navigate('/jobs/' + jobIDs[0])
			else if (jobIDs.length > 1) navigate('/jobs')
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
        <div className="card flex-1 chain-select-card">
          <div className="card-title">链</div>
          <ChainSelect value={chain} onChange={setChain} />
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
        <div className="card-title">案件 / 标签（可选，§22）</div>
        <div className="row gap">
          <select style={{ maxWidth: 220 }} value={caseName} onChange={(e) => setCaseName(e.target.value)}>
            <option value="">选择案件</option>
            {cases.map((c) => <option key={c.id} value={c.name}>{c.name}</option>)}
          </select>
          {tags.map((t) => {
            const on = tagNames.includes(t.name)
            return (
              <label key={t.id} className="chip" style={{ opacity: on ? 1 : 0.6 }}>
                <input type="checkbox" checked={on} onChange={() => setTagNames((cur) => on ? cur.filter((x) => x !== t.name) : [...cur, t.name])} /> {t.name}
              </label>
            )
          })}
          <button className="ghost sm" onClick={createCase}>+ 新建案件</button>
          <button className="ghost sm" onClick={createTag}>+ 新建标签</button>
        </div>
		{(Object.keys(subjectMeta).length > 0 || Object.keys(subjectChains).length > 0) && <p className="muted" style={{ marginTop: 6 }}>已解析 {Object.keys(subjectMeta).length} 个案件/标签映射、{Object.keys(subjectChains).length} 个链映射；不同链将自动拆为多个任务</p>}
      </div>

      <div className="card">
        <div className="card-title">Dataset</div>
        <div className="row gap wrap">
          {DATASETS.map((d) => (
            <label key={d} className="chip">
              <input type="checkbox" checked={datasets.includes(d)} onChange={() => toggleDataset(d)} /> {datasetLabel(d)}
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
                <tr key={i}><td className="mono">{it.subject.slice(0, 10)}…</td><td>{datasetLabel(it.dataset)}</td><td>{(it.ratio * 100).toFixed(1)}%</td></tr>
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

function groupAddressesByChain(addresses: string[], fallbackChain: string, overrides: Record<string, string>) {
	const groups: Record<string, string[]> = {}
	for (const address of addresses) {
		const addressChain = overrides[address] || fallbackChain
		if (!groups[addressChain]) groups[addressChain] = []
		groups[addressChain].push(address)
	}
	return groups
}
