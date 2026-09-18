#!/usr/bin/env node
/**
 * Supply-chain gate for the `xlsx` (SheetJS) dependency.
 *
 * WHY THIS EXISTS
 *   npm has no fixed release of `xlsx`: registry dist-tags stop at 0.18.5, and two HIGH advisories
 *   remain open against it (GHSA-4r6h-8v6p-xvw6 prototype pollution, <0.19.3; GHSA-5pgg-2g8v-p4x9
 *   ReDoS, <0.20.2). SheetJS publishes fixed releases only from its own CDN, not from npm.
 *   This project therefore pins the dependency to an exact CDN tarball URL and verifies that tarball
 *   against a hash committed to this repository.
 *
 * HONEST BOUNDARY
 *   SheetJS does not publish an official checksum. The expected hash below was computed by us from the
 *   official CDN over HTTPS and reproduced by two independent downloads. It is a *pin*, not a vendor
 *   signature: it protects against the URL later serving different bytes, and against the lockfile
 *   being regenerated against a changed upstream. It does not prove authorship.
 *
 * USAGE
 *   node scripts/verify-xlsx-tarball.mjs              # download the pinned URL and verify it
 *   node scripts/verify-xlsx-tarball.mjs --installed  # verify the installed node_modules copy only
 */
import { createHash } from 'node:crypto'
import { gunzipSync } from 'node:zlib'
import { readFileSync, existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const HERE = dirname(fileURLToPath(import.meta.url))
const FRONTEND = join(HERE, '..')

export const XLSX_TARBALL_URL = 'https://cdn.sheetjs.com/xlsx-0.20.3/xlsx-0.20.3.tgz'
export const XLSX_EXPECTED_VERSION = '0.20.3'
export const XLSX_EXPECTED_SHA512_HEX =
  'a0b0eade3c3b01c2ea2961f60210a9553665f267fa5f661178ff8d7a1d12254cd5fc1759623b61f78b46e6da22301d4f3eb62dc4e09f6a850292fb6e1fedc024'
export const XLSX_EXPECTED_SRI =
  'sha512-oLDq3jw7AcLqKWH2AhCpVTZl8mf6X2YReP+Neh0SJUzV/BdZYjth94tG5toiMB1PPrYtxOCfaoUCkvtuH+3AJA=='

let failures = 0
const ok = (m) => console.log('OK   ' + m)
const bad = (m) => { failures += 1; console.log('FAIL ' + m) }

function sha512Of (buf) {
  return createHash('sha512').update(buf).digest('hex')
}

/** Minimal tar reader: returns the bytes of a named entry, or null. */
function tarEntry (tarBuf, wanted) {
  let offset = 0
  while (offset + 512 <= tarBuf.length) {
    const header = tarBuf.subarray(offset, offset + 512)
    if (header.every((b) => b === 0)) return null
    const name = header.subarray(0, 100).toString('utf8').replace(/\0.*$/, '')
    const sizeField = header.subarray(124, 136).toString('ascii').replace(/\0.*$/, '').trim()
    const size = sizeField ? parseInt(sizeField, 8) : 0
    if (name === wanted) return tarBuf.subarray(offset + 512, offset + 512 + size)
    offset += 512 + Math.ceil(size / 512) * 512
  }
  return null
}

function verifyBuffer (buf, label) {
  const hex = sha512Of(buf)
  if (hex === XLSX_EXPECTED_SHA512_HEX) ok(label + ': sha512 hex matches the pinned value')
  else bad(label + ': sha512 hex mismatch\n       expected ' + XLSX_EXPECTED_SHA512_HEX + '\n       actual   ' + hex)

  const sri = 'sha512-' + createHash('sha512').update(buf).digest('base64')
  if (sri === XLSX_EXPECTED_SRI) ok(label + ': SRI matches the pinned value')
  else bad(label + ': SRI mismatch (expected ' + XLSX_EXPECTED_SRI + ', actual ' + sri + ')')

  let inner = null
  try { inner = tarEntry(gunzipSync(buf), 'package/package.json') } catch (e) { bad(label + ': gunzip/tar parse failed: ' + e.message) }
  if (!inner) { bad(label + ': package/package.json not found inside the tarball'); return }
  let pkg = null
  try { pkg = JSON.parse(inner.toString('utf8')) } catch (e) { bad(label + ': inner package.json is not JSON: ' + e.message); return }
  if (pkg.name === 'xlsx' && pkg.version === XLSX_EXPECTED_VERSION) ok(label + ': inner package.json is ' + pkg.name + '@' + pkg.version)
  else bad(label + ': inner package.json is ' + pkg.name + '@' + pkg.version + ', expected xlsx@' + XLSX_EXPECTED_VERSION)
  if (pkg.license === 'Apache-2.0') ok(label + ': inner license is Apache-2.0')
  else bad(label + ': inner license is ' + pkg.license + ', expected Apache-2.0')
}

async function main () {
  const installedOnly = process.argv.includes('--installed')

  if (!installedOnly) {
    console.log('Fetching ' + XLSX_TARBALL_URL)
    let res
    try { res = await fetch(XLSX_TARBALL_URL, { redirect: 'error' }) } catch (e) { bad('fetch failed: ' + e.message); res = null }
    if (res && !res.ok) { bad('HTTP ' + res.status + ' from ' + XLSX_TARBALL_URL); res = null }
    if (res) {
      const buf = Buffer.from(await res.arrayBuffer())
      console.log('  downloaded ' + buf.length + ' bytes')
      verifyBuffer(buf, 'tarball')
    }
  }

  const installedPkg = join(FRONTEND, 'node_modules', 'xlsx', 'package.json')
  if (existsSync(installedPkg)) {
    const meta = JSON.parse(readFileSync(installedPkg, 'utf8'))
    if (meta.version === XLSX_EXPECTED_VERSION) ok('installed node_modules/xlsx is ' + meta.version)
    else bad('installed node_modules/xlsx is ' + meta.version + ', expected ' + XLSX_EXPECTED_VERSION)
  } else if (installedOnly) {
    bad('node_modules/xlsx/package.json not found (run pnpm install first)')
  } else {
    console.log('NOTE node_modules/xlsx not present; skipped the installed-copy check')
  }

  if (failures > 0) { console.log('\nRESULT xlsx-supply-chain-gate FAIL=' + failures); process.exit(1) }
  console.log('\nRESULT xlsx-supply-chain-gate PASS (failures=0)')
}

main().catch((e) => { console.log('FAIL unexpected error: ' + (e && e.stack ? e.stack : e)); process.exit(2) })
