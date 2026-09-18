#!/usr/bin/env node
/**
 * 校验 packages/ui/qr.ts 的二维码实现是否与独立参考实现逐位一致。
 *
 * 参考实现与口径
 *   fixture 由 VM 上的 Python segno 1.6.6 生成：
 *   segno.make(data, error='m', mode='byte', encoding='utf-8', boost_error=False, micro=False)，
 *   matrix 为 [[0/1, ...], ...]（不含静默区）。生成脚本只依赖 segno，不引用本仓库任何代码。
 *
 * 校验内容
 *   1. 每个 fixture：版本、边长、以及 size*size 个模块逐位比对（差异会打印行列坐标）；
 *   2. fixture 集合指纹（sha256）与生成时一致，防止参考数据被改写；
 *   3. qrPathData 生成的 SVG path 反解回矩阵后与模块一致（含静默区偏移）；
 *   4. 边界行为：空内容与超出等级 M / 版本 20 容量的内容必须抛错而不是产出错误二维码。
 *
 * 加载方式
 *   Node 24 直接 import .ts（类型擦除）；老版本 Node 自动回退到仓库自带 tsc 编到临时目录，
 *   两者都不联网、不安装任何依赖。
 *
 * 用法
 *   node scripts/verify-qr.mjs
 */
import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, mkdtempSync, readFileSync, readdirSync, renameSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, relative } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const HERE = dirname(fileURLToPath(import.meta.url))
const FRONTEND = join(HERE, '..')
const QR_TS = join(FRONTEND, 'packages', 'ui', 'qr.ts')
const FIXTURE_DIR = join(FRONTEND, 'packages', 'ui', '__fixtures__', 'qr')

/** fixture 集合指纹：拼接每个文件 sha256（十六进制）后再取 sha256，由生成脚本打印。 */
const FIXTURE_SET_SHA256 =
  '6cf1e85fbda2249e877f65a740a5e2442523c104c8f405bd7ab8446f7cdc0954'

/** 等级 M / 版本 20 的 byte 模式容量上限（字节）。 */
const MAX_BYTES_V20_M = 666

const MAX_DIFF_SAMPLES = 12

let passes = 0
let failures = 0
const ok = (m) => { passes += 1; console.log('PASS ' + m) }
const bad = (m) => { failures += 1; console.log('FAIL ' + m) }
const info = (m) => { console.log('INFO ' + m) }
const sha256 = (buf) => createHash('sha256').update(buf).digest('hex')

/** 加载 qr.ts：优先用 Node 原生 TypeScript 支持，失败则回退到 tsc 编译。 */
async function loadQrModule () {
  try {
    const mod = await import(pathToFileURL(QR_TS).href)
    info('加载方式：Node 原生 TypeScript 类型擦除（' + relative(FRONTEND, QR_TS) + '）')
    return mod
  } catch (err) {
    info('Node 原生 TS 加载失败：' + (err && err.message ? err.message : String(err)))
    info('回退：用仓库内 node_modules/typescript 编译到临时目录后再加载')
  }
  return compileWithTsc()
}

/** 用仓库自带的 tsc 把 qr.ts 编成 ESM 到临时目录（不写入仓库、不联网）。 */
async function compileWithTsc () {
  const tsc = join(FRONTEND, 'node_modules', 'typescript', 'bin', 'tsc')
  if (!existsSync(tsc)) {
    console.error('FAIL 找不到 ' + tsc + '，无法编译 qr.ts（Node 版本过低时需要它）')
    process.exit(2)
  }
  const work = mkdtempSync(join(tmpdir(), 'qr-verify-'))
  const out = join(work, 'out')
  const result = spawnSync(process.execPath, [
    tsc, '--target', 'es2022', '--module', 'esnext', '--moduleResolution', 'bundler',
    '--skipLibCheck', '--strict', '--outDir', out, QR_TS,
  ], { cwd: work, encoding: 'utf8' })
  if (result.status !== 0) {
    console.error(result.stdout || '')
    console.error(result.stderr || '')
    console.error('FAIL tsc 编译失败，退出码 ' + result.status)
    process.exit(2)
  }
  const compiled = join(out, 'qr.js')
  if (!existsSync(compiled)) {
    console.error('FAIL tsc 没有产出 ' + compiled)
    process.exit(2)
  }
  // 临时目录没有 package.json，改名为 .mjs 才会按 ESM 解析
  const esm = join(work, 'qr.mjs')
  renameSync(compiled, esm)
  try {
    const mod = await import(pathToFileURL(esm).href)
    info('加载方式：tsc 编译产物（临时目录，用后即删）')
    return mod
  } finally {
    rmSync(work, { recursive: true, force: true })
  }
}

/** 逐位比较本实现输出与参考矩阵，返回问题描述数组。 */
function compareMatrix (fixture, result) {
  const problems = []
  const size = fixture.size
  if (result.version !== fixture.version) {
    problems.push('版本不一致：参考 v' + fixture.version + '，本实现 v' + result.version)
  }
  if (result.size !== fixture.size) {
    problems.push('边长不一致：参考 ' + fixture.size + '，本实现 ' + result.size)
  }
  if (!Array.isArray(fixture.matrix) || fixture.matrix.length !== size) {
    problems.push('fixture 结构异常：matrix 行数 ' + (fixture.matrix ? fixture.matrix.length : 'n/a'))
    return problems
  }
  if (result.version !== fixture.version || result.size !== fixture.size) return problems

  let diff = 0
  const samples = []
  for (let y = 0; y < size; y += 1) {
    const row = fixture.matrix[y]
    if (!Array.isArray(row) || row.length !== size) {
      problems.push('fixture 结构异常：第 ' + y + ' 行列数不为 ' + size)
      return problems
    }
    for (let x = 0; x < size; x += 1) {
      const want = row[x] === 1
      const got = result.modules[y * size + x] === true
      if (want !== got) {
        diff += 1
        if (samples.length < MAX_DIFF_SAMPLES) {
          samples.push('(行 ' + y + ', 列 ' + x + ') 参考 ' + (want ? 1 : 0) + ' / 本实现 ' + (got ? 1 : 0))
        }
      }
    }
  }
  if (diff > 0) {
    problems.push('共 ' + diff + ' 个模块不同（前 ' + samples.length + ' 处：' + samples.join('，') + '）')
  }
  return problems
}

/** 把 SVG path 反解成模块集合，用于验证 qrPathData。 */
function parsePathRects (path, size, quietZone) {
  const rects = []
  const re = /M(\d+) (\d+)h(\d+)v1h-(\d+)z/g
  let consumed = 0
  let match = re.exec(path)
  while (match !== null) {
    consumed += match[0].length
    const x = Number(match[1])
    const y = Number(match[2])
    const width = Number(match[3])
    const back = Number(match[4])
    if (width !== back || width < 1) return { error: '子路径不是 1 模块高的横线：' + match[0] }
    if (x < quietZone || y < quietZone || x + width > size + quietZone || y >= size + quietZone) {
      return { error: '子路径越出 viewBox：' + match[0] }
    }
    rects.push({ x, y, width })
    match = re.exec(path)
  }
  if (consumed !== path.length) return { error: 'path 中存在无法解析的片段' }
  return { rects }
}

/** 校验 qrPathData：反解后的深色模块必须与原矩阵逐位一致。 */
function verifyPathData (qrPathData, fixture, result) {
  const quietZone = 4
  const path = qrPathData(result.modules, result.size, quietZone)
  if (typeof path !== 'string' || path.length === 0) return 'path 为空'
  if (!path.startsWith('M') || !path.endsWith('z')) return 'path 形状异常'
  const parsed = parsePathRects(path, result.size, quietZone)
  if (parsed.error) return parsed.error
  const painted = new Uint8Array(result.size * result.size)
  for (let i = 0; i < parsed.rects.length; i += 1) {
    const rect = parsed.rects[i]
    for (let k = 0; k < rect.width; k += 1) {
      const x = rect.x - quietZone + k
      const y = rect.y - quietZone
      painted[y * result.size + x] = 1
    }
  }
  let diff = 0
  const samples = []
  for (let i = 0; i < painted.length; i += 1) {
    const want = result.modules[i] ? 1 : 0
    if (painted[i] !== want) {
      diff += 1
      if (samples.length < MAX_DIFF_SAMPLES) {
        samples.push('(行 ' + Math.floor(i / result.size) + ', 列 ' + (i % result.size) + ')')
      }
    }
  }
  if (diff > 0) return diff + ' 个模块与 path 不一致：' + samples.join('，')
  return null
}

function expectThrow (label, fn) {
  try {
    fn()
  } catch (err) {
    ok(label + '（抛错信息：' + (err && err.message ? err.message : String(err)) + '）')
    return
  }
  bad(label + '：本应抛错，实际返回了矩阵')
}

async function main () {
  if (!existsSync(QR_TS)) {
    console.error('FAIL 找不到 ' + QR_TS)
    process.exit(2)
  }
  if (!existsSync(FIXTURE_DIR)) {
    console.error('FAIL 找不到参考矩阵目录 ' + FIXTURE_DIR)
    process.exit(2)
  }

  const { encodeQr, qrPathData } = await loadQrModule()
  if (typeof encodeQr !== 'function' || typeof qrPathData !== 'function') {
    console.error('FAIL qr.ts 没有导出 encodeQr / qrPathData')
    process.exit(2)
  }

  const names = readdirSync(FIXTURE_DIR).filter((f) => f.endsWith('.json')).sort()
  if (names.length === 0) {
    console.error('FAIL ' + FIXTURE_DIR + ' 下没有任何 .json fixture')
    process.exit(2)
  }
  info('参考矩阵用例数：' + names.length + '（目录 ' + relative(FRONTEND, FIXTURE_DIR) + '）')

  const digestParts = []
  const matrixParts = []
  const masks = new Set()
  const versions = new Set()
  let pathChecked = 0
  let byteTotal = 0

  for (let i = 0; i < names.length; i += 1) {
    const name = names[i]
    const raw = readFileSync(join(FIXTURE_DIR, name))
    const digest = sha256(raw)
    digestParts.push(digest)
    let fixture = null
    try {
      fixture = JSON.parse(raw.toString('utf8'))
    } catch (err) {
      bad(name + '：fixture 不是合法 JSON（' + err.message + '）')
      continue
    }

    const label = (fixture.name || name.replace(/\.json$/, ''))
    const problems = []

    const utf8Length = Buffer.byteLength(fixture.data, 'utf8')
    if (utf8Length !== fixture.data_bytes_utf8) {
      problems.push('fixture 记录字节数 ' + fixture.data_bytes_utf8 + '，实际 UTF-8 字节数 ' + utf8Length)
    }

    let result = null
    try {
      result = encodeQr(fixture.data)
    } catch (err) {
      problems.push('encodeQr 抛错：' + (err && err.message ? err.message : String(err)))
    }
    if (result && problems.length === 0) problems.push(...compareMatrix(fixture, result))

    if (result && problems.length === 0) {
      const pathProblem = verifyPathData(qrPathData, fixture, result)
      if (pathProblem) problems.push('qrPathData：' + pathProblem)
      else pathChecked += 1
      for (let k = 0; k < result.modules.length; k += 1) {
        matrixParts.push(result.modules[k] ? '1' : '0')
      }
    }

    if (problems.length > 0) {
      bad(label + '：' + problems.join('；'))
      continue
    }

    masks.add(fixture.mask)
    versions.add(result.version)
    byteTotal += utf8Length
    ok(label + '：v' + result.version + ' ' + result.size + 'x' + result.size +
      '（' + utf8Length + ' 字节，mask=' + fixture.mask + '）' + result.size * result.size +
      ' 个模块逐位一致，sha256=' + digest.slice(0, 16))
  }

  const fixtureSetDigest = sha256(Buffer.from(digestParts.join(''), 'ascii'))
  if (fixtureSetDigest === FIXTURE_SET_SHA256) {
    ok('fixture 集合指纹与 segno 生成时一致：sha256=' + fixtureSetDigest)
  } else {
    bad('fixture 集合指纹不一致：期望 ' + FIXTURE_SET_SHA256 + '，实际 ' + fixtureSetDigest +
      '（重新生成 fixture 后需要同步更新本文件的常量）')
  }

  const matrixDigest = sha256(Buffer.from(matrixParts.join(''), 'ascii'))
  ok('本实现矩阵拼接指纹：sha256=' + matrixDigest + '（' + matrixParts.length + ' 个模块）')

  if (pathChecked === names.length) {
    ok('qrPathData：' + pathChecked + ' 个用例的 SVG path 反解回矩阵后逐位一致（静默区 4 模块偏移已校验）')
  } else {
    bad('qrPathData：只有 ' + pathChecked + '/' + names.length + ' 个用例通过反解比对')
  }

  const maskList = Array.from(masks).sort((a, b) => a - b)
  if (maskList.length >= 4 && !(maskList.length === 1 && maskList[0] === 0)) {
    ok('掩码自动选择：' + names.length + ' 个用例命中 ' + maskList.length + ' 种掩码 [' + maskList.join(', ') +
      ']，不是固定 mask 0')
  } else {
    bad('掩码分布异常：' + maskList.join(', ') + '（参考实现应给出多种掩码）')
  }
  ok('版本自动选择：命中版本 [' + Array.from(versions).sort((a, b) => a - b).join(', ') + ']，累计数据 ' + byteTotal + ' 字节')

  // 边界行为：空内容与超容量内容必须失败，且容量上限恰好是 666 字节（等级 M / v20）
  expectThrow('空字符串必须抛错（组件据此返回 null）', () => encodeQr(''))
  expectThrow('超过 v20 容量的 ' + (MAX_BYTES_V20_M + 1) + ' 字节必须抛错', () =>
    encodeQr('A'.repeat(MAX_BYTES_V20_M + 1)))
  const largest = encodeQr('A'.repeat(MAX_BYTES_V20_M))
  if (largest.version === 20 && largest.size === 97) {
    ok('容量上限边界：' + MAX_BYTES_V20_M + ' 字节刚好落在 v20（97x97）')
  } else {
    bad('容量上限边界：' + MAX_BYTES_V20_M + ' 字节得到 v' + largest.version + ' ' + largest.size + 'x' + largest.size)
  }

  console.log('----------------------------------------------------------')
  console.log('PASS 行数：' + passes + '，FAIL 行数：' + failures + '，用例 ' + names.length + ' 个')
  console.log('fixture 集合 sha256：' + fixtureSetDigest)
  console.log('本实现输出 sha256：' + matrixDigest)
  if (failures > 0) {
    console.error('verify-qr: 失败 ' + failures + ' 项')
    process.exit(1)
  }
  console.log('verify-qr: 全绿')
}

main().catch((err) => {
  console.error('FAIL 未预期异常：' + (err && err.stack ? err.stack : String(err)))
  process.exit(2)
})
