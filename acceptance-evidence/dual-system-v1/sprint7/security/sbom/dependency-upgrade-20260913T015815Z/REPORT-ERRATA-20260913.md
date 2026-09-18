# REPORT ERRATA - 2026-09-13 dependency-upgrade scoped SBOM

Applies to `REPORT.md` and `sbom-manifest.json` in this directory.

**Affected claim: exactly one row - the self-referential row for `windows-side-hashes.sha256`.**
Nothing else in this evidence set is affected: the six SBOM documents, their hashes, their
target binding, and the upgraded-module verification all stand as reported.

## Why this errata exists instead of a corrected REPORT.md

The hard rule for this task is *additive only*: never overwrite, move or truncate an existing
file. `REPORT.md` and `sbom-manifest.json` were produced by
`build-manifest-and-report-dependency-upgrade-20260913T015815Z.py` and are therefore preserved
byte-for-byte as that run produced them - including the one wrong flag described here.

## What is wrong

`windows-side-hashes.sha256` is the manifest of the Windows-side hashes. A file cannot contain
its own hash, so when the builder compared the two sides it had no Windows-side value for that
one path and marked the row as not matching. It is a bookkeeping artifact, not a difference.

| # | Location | What it says | Correct value |
|---|---|---|---|
| 1 | `REPORT.md` line 18 (header table) | `Overall verification: GAPS PRESENT - see below` | **PASS.** Every document parses, every document is bound to its binary, no upgraded module is at a wrong version, and every file matched across the two copies. The word GAPS was driven only by item 2 below. |
| 2 | `REPORT.md` section 8, hash table | `windows-side-hashes.sha256 ... MISMATCH` | **MATCH.** Windows `Get-FileHash` = `c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66`; VM `sha256sum` = `c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66`. Identical; both values are printed in that same section and in section 12. |
| 3 | `REPORT.md` section 8, summary line | `46 files compared, 1 mismatches.` | **46 files compared, 0 real mismatches** (45 rows MATCH + 1 self-reference row that could not carry its own hash). |
| 4 | `sbom-manifest.json` -> `files_summary.all_match` | `false` | effectively **true** (see item 2) |
| 5 | `sbom-manifest.json` -> `files_summary.mismatches` | `["windows-side-hashes.sha256"]` | effectively **[]** (self-reference artifact only) |

## Authoritative, corrected cross-check

`crosscheck-local-vm.sha256` in this directory is the corrected (and wider) check: **51 rows,
0 mismatches**, format `STATUS  <vm_sha256>  <windows_sha256>  <relative_path>`, and it covers
every file that existed when it ran, `windows-side-hashes.sha256` included (its Windows-side
value was supplied explicitly as the value computed on Windows).

The files created after that check - the crosscheck script, `crosscheck-local-vm.sha256`,
`CROSSCHECK-CORRECTION-20260913.md`, `verify-no-modifications.sh`,
`additive-only-verification.txt` and this errata file - were each verified by a direct
`Get-FileHash` (Windows) versus `sha256sum` (VM) comparison at the end of the work; all of them
are byte-identical on both sides.

## Defect lineage

This is the third and last bookkeeping defect of this scope, and like the other two it is
confined to my own reporting code, not to the SBOMs. Full record:
`DEFECT-RECORD-20260913-validator-passes.md` (validator passes 0 and 1); this one is the
builder's self-reference handling.
