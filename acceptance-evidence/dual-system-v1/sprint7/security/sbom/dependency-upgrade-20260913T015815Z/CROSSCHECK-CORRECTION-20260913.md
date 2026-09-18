# Cross-check correction note (2026-09-13 dependency-upgrade scoped SBOM)

This note corrects exactly one row of `REPORT.md` and of `sbom-manifest.json`.
Nothing in the SBOM documents themselves is affected, and no existing file was modified:
`REPORT.md` is preserved byte-for-byte as produced by the builder.

## What the original report says

    REPORT.md section 8:          46 files compared, 1 mismatches.
    REPORT.md row:                | windows-side-hashes.sha256 | c46f97a4...a66 | **MISMATCH** |
    sbom-manifest.json:           files_summary.all_match = false,
                                  files_summary.mismatches = ["windows-side-hashes.sha256"]

## Why that row is spurious (not a hash difference)

`windows-side-hashes.sha256` is the manifest of the Windows-side hashes. By construction it
cannot contain its own hash, so the builder had no Windows-side value for that one path
and marked it as not matching. Its real values, verified on both sides, are identical:

- Windows (`Get-FileHash -Algorithm SHA256`): `1bc7aeda1473738063287c7400563d1b47fc90aa8f1043ef4f39e8078669c4b8`
- VM (`sha256sum`): `1bc7aeda1473738063287c7400563d1b47fc90aa8f1043ef4f39e8078669c4b8`

The report itself already prints both numbers (section 8 prose and section 12 inventory),
so the value is not hidden - only the MATCH flag of that single row is wrong.

## Corrected picture

- Files compared in this final check: **51**
- Rows with `status = MATCH`: **51**
- Rows with a real difference: **0**

Every file of this scope is byte-identical on the acceptance VM and in the Windows
worktree, `windows-side-hashes.sha256` included.

The authoritative machine-readable table is `crosscheck-local-vm.sha256` in this
directory: `STATUS  <vm_sha256>  <windows_sha256>  <relative_path>`.

## Coverage boundary (stated honestly)

Three files necessarily fall outside the table above because they are produced at or after
this check: this crosscheck script, `crosscheck-local-vm.sha256` and this note.
They are copied to the Windows worktree afterwards and verified there by a direct
`Get-FileHash` vs `sha256sum` comparison; the result is recorded in the
completion report for this task rather than in a file that would itself need verifying.

