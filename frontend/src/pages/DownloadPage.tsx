import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { FileUp, ShieldCheck } from 'lucide-react'
import { api } from '../api/client'
import ChainSelect from '../components/ChainSelect'
import NoticeDialog from '../components/NoticeDialog'
import { PerformanceModeSelector, useAnkrProductionProfile } from '../components/PerformanceMode'
import { CHAINS, DATASETS, type PerformanceMode } from '../types'
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
  const [performanceMode, setPerformanceMode] = useState<PerformanceMode>('fast')
  const [preview, setPreview] = useState<{ overall_ratio: number; items: { subject: string; dataset: string; ratio: number }[] } | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)
  const profileQuery = useAnkrProductionProfile()

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

  const rangeValid = useMemo(() => {
    if (rangeType === 'full') return true
    if (rangeType === 'block') {
      if (fromBlock.trim() === '' || toBlock.trim() === '') return false
      const from = Number(fromBlock)
      const to = Number(toBlock)
      return Number.isSafeInteger(from) && Number.isSafeInteger(to) && from >= 0 && to >= from
    }
    if (!fromTime || !toTime) return false
    const from = new Date(fromTime).getTime()
    const to = new Date(toTime).getTime()
    return Number.isFinite(from) && Number.isFinite(to) && to >= from
  }, [fromBlock, fromTime, rangeType, toBlock, toTime])

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
				performance_mode: performanceMode,
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
    <div className="page download-page">
      <header className="page-header download-header">
        <div><h1>地址数据下载</h1><p>配置地址、链、范围和数据集</p></div>
      </header>

      <div className="form-workspace">
        <div className="form-main">
          <section className="form-section">
            <header className="form-section-head"><span className="form-section-index">01</span><div><h2>调查对象</h2><p>输入单个或批量地址，系统会自动去重并验证格式</p></div></header>
            <textarea id="download-addresses" className="address-input" aria-label="调查地址" name="addresses" autoComplete="off" spellCheck={false}
              value={addressText} onChange={(e) => setAddressText(e.target.value)} placeholder={'0x9b77…b80dd\n0x8a123…'} rows={3} />
            <div className="input-meta"><span><strong>{addresses.valid.length}</strong> 个有效 · {addresses.dup} 个重复 · {addresses.bad} 个错误</span><button className="ghost" onClick={() => fileRef.current?.click()}><FileUp size={14} aria-hidden="true" /> 导入 CSV / TXT / XLSX</button><input ref={fileRef} type="file" accept=".csv,.txt,.xlsx,.xls" hidden aria-label="导入地址文件" onChange={(e) => { const f = e.target.files?.[0]; if (f) handleFile(f); e.target.value = '' }} /></div>
          </section>

          <section className="form-section">
            <header className="form-section-head"><span className="form-section-index">02</span><div><h2>链与数据范围</h2><p>设置默认链和所需区块或时间窗口</p></div></header>
            <div className="form-control-row">
              <label>区块链<ChainSelect value={chain} onChange={setChain} /></label>
              <div className="field-label">数据范围<div className="range-selector">
            <label><input type="radio" checked={rangeType === 'full'} onChange={() => setRangeType('full')} /> 全量</label>
            <label><input type="radio" checked={rangeType === 'block'} onChange={() => setRangeType('block')} /> 区块范围</label>
            <label><input type="radio" checked={rangeType === 'time'} onChange={() => setRangeType('time')} /> 时间范围</label>
              </div></div>
            </div>
          {rangeType === 'block' && (
            <div className="row gap" style={{ marginTop: 8 }}>
              <input aria-label="起始区块" name="from-block" autoComplete="off" type="number" min="0" step="1" placeholder="起始区块…" value={fromBlock} onChange={(e) => setFromBlock(e.target.value)} />
              <input aria-label="结束区块" name="to-block" autoComplete="off" type="number" min="0" step="1" placeholder="结束区块…" value={toBlock} onChange={(e) => setToBlock(e.target.value)} />
            </div>
          )}
          {rangeType === 'time' && (
            <div className="row gap" style={{ marginTop: 8 }}>
              <input aria-label="开始时间" name="from-time" autoComplete="off" type="datetime-local" value={fromTime} onChange={(e) => setFromTime(e.target.value)} />
              <input aria-label="结束时间" name="to-time" autoComplete="off" type="datetime-local" value={toTime} onChange={(e) => setToTime(e.target.value)} />
            </div>
          )}
          </section>

      <details className="form-section advanced-settings">
        <summary>高级条件（可选）<span>＋ 展开条件</span></summary>
        <div className="advanced-settings-body">
        <div className="row gap">
          <select aria-label="选择案件" style={{ maxWidth: 220 }} value={caseName} onChange={(e) => setCaseName(e.target.value)}>
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
      </details>

      <section className="form-section">
        <header className="form-section-head"><span className="form-section-index">04</span><div><h2>数据集</h2><p>选择需要采集和标准化的数据类型</p></div></header>
        <div className="dataset-grid">
          {DATASETS.map((d) => (
            <label key={d} className="chip">
              <input type="checkbox" checked={datasets.includes(d)} onChange={() => toggleDataset(d)} /> {datasetLabel(d)}
            </label>
          ))}
        </div>
      </section>

      <section className="form-section">
        <header className="form-section-head"><span className="form-section-index">05</span><div><h2>运行模式</h2><p>选择 Ankr 全局拉取策略</p></div></header>
        <PerformanceModeSelector value={performanceMode} onChange={setPerformanceMode} profile={profileQuery.data} disabled={busy} />
      </section>

      {preview && (
        <div className="coverage-preview">
          <div style={{ padding: 18 }}>
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
        </div>
      )}
        </div>

        <aside className="task-preview" aria-label="任务预览">
          <header><small>TASK PREVIEW</small><h2>任务预览</h2></header>
          <dl><div><dt>地址数量</dt><dd>{addresses.valid.length.toLocaleString()}</dd></div><div><dt>默认链</dt><dd>{chain.toUpperCase()}</dd></div><div><dt>数据范围</dt><dd>{rangeType === 'full' ? '全量' : rangeType === 'block' ? '区块范围' : '时间范围'}</dd></div><div><dt>数据集</dt><dd>{datasets.length} 类</dd></div><div><dt>运行模式</dt><dd>{performanceMode.toUpperCase()}</dd></div><div><dt>案件</dt><dd>{caseName || '未关联'}</dd></div><div><dt>Provider</dt><dd>Ankr Advanced API</dd></div></dl>
          <footer><div className={'preview-readiness ' + (addresses.valid.length > 0 && datasets.length > 0 && rangeValid ? 'ready' : '')}><i />{addresses.valid.length > 0 && datasets.length > 0 && rangeValid ? '配置完整，可以创建任务' : '等待补全任务配置'}</div><button onClick={doPreview} disabled={busy || addresses.valid.length === 0 || datasets.length === 0 || !rangeValid}>Coverage 预览</button><button className="primary" onClick={doCreate} disabled={busy || addresses.valid.length === 0 || datasets.length === 0 || !rangeValid}><ShieldCheck size={14} aria-hidden="true" /> {busy ? '正在创建…' : '创建下载任务'}</button></footer>
        </aside>
      </div>
      {error && <NoticeDialog title="下载任务未完成" message={error} onClose={() => setError('')} />}
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
