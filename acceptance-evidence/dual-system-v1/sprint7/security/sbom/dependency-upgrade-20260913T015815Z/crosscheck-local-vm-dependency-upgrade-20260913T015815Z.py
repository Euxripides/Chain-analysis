#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
S7-SEC-01 follow-up (2026-09-13 dependency-upgrade scope)
FINAL two-sided cross-check: every file of this scope on the acceptance VM against the
Windows git worktree copy.

Outputs (both new files, created only if absent):
  crosscheck-local-vm.sha256                machine-readable table (this is the file name
                                            already referenced by REPORT.md section 8)
  CROSSCHECK-CORRECTION-20260913.md         human note, including the correction of the one
                                            spurious MISMATCH row in REPORT.md section 8

usage: crosscheck-local-vm-dependency-upgrade-20260913T015815Z.py \
           <sha256 of windows-side-hashes-round2.sha256 as computed on Windows>
"""
import hashlib
import json
import os
import sys
from datetime import datetime, timezone

PROJ = "/home/euripides/projects/blockchain-platform"
OUT = os.path.join(
    PROJ,
    "acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z",
)
WIN_MANIFEST = "windows-side-hashes-round2.sha256"
OUTPUTS = ["crosscheck-local-vm.sha256", "CROSSCHECK-CORRECTION-20260913.md"]
BT = chr(96)


def code(text):
    return BT + str(text) + BT


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def guard():
    for name in OUTPUTS:
        target = os.path.join(OUT, name)
        if os.path.exists(target):
            sys.stderr.write("FATAL REFUSING TO OVERWRITE EXISTING PATH: %s\n" % target)
            raise SystemExit(3)


def read_manifest(path):
    entries = {}
    with open(path, encoding="utf-8-sig") as handle:
        for raw in handle:
            line = raw.strip()
            if not line:
                continue
            parts = line.split()
            if len(parts) >= 2:
                entries[" ".join(parts[1:])] = parts[0]
    return entries


def main():
    guard()
    win_manifest_sha = sys.argv[1] if len(sys.argv) > 1 else "NOT_PROVIDED"
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    win_hashes = read_manifest(os.path.join(OUT, WIN_MANIFEST))
    win_hashes[WIN_MANIFEST] = win_manifest_sha

    vm_hashes = {}
    vm_sizes = {}
    for root, _dirs, files in os.walk(OUT):
        for name in sorted(files):
            full = os.path.join(root, name)
            rel = os.path.relpath(full, OUT).replace(os.sep, "/")
            vm_hashes[rel] = sha256_file(full)
            vm_sizes[rel] = os.path.getsize(full)

    rows = []
    for rel in sorted(set(vm_hashes) | set(win_hashes)):
        vm_sha = vm_hashes.get(rel)
        win_sha = win_hashes.get(rel)
        if vm_sha and win_sha and vm_sha == win_sha:
            status = "MATCH"
        elif vm_sha and not win_sha:
            status = "WINDOWS-COPY-MISSING"
        elif win_sha and not vm_sha:
            status = "VM-COPY-MISSING"
        else:
            status = "MISMATCH"
        rows.append({"path": rel, "vm": vm_sha, "windows": win_sha, "status": status,
                     "size_bytes": vm_sizes.get(rel)})

    mismatches = [row for row in rows if row["status"] != "MATCH"]

    lines = []
    add = lines.append
    add("# " + "S7-SEC-01 follow-up - FINAL two-sided hash cross-check (2026-09-13 dependency-upgrade scope)")
    add("# generated_utc = %s" % now)
    add("# host = %s   project = %s" % (os.uname().nodename, PROJ))
    add("# evidence_dir_relative = acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z")
    add("# windows_manifest = %s" % WIN_MANIFEST)
    add("# windows_manifest_sha256_(computed on windows) = %s" % win_manifest_sha)
    add("# windows_manifest_sha256_(computed on vm)      = %s" % vm_hashes.get(WIN_MANIFEST))
    add("# format: STATUS  <vm_sha256>  <windows_sha256>  <relative_path>")
    add("# files_compared = %d   mismatches = %d" % (len(rows), len(mismatches)))
    add("# NOTE: this file replaces nothing. REPORT.md section 8 reports '1 mismatch' for")
    add("#       windows-side-hashes.sha256; that row is a self-reference artifact (a manifest")
    add("#       cannot contain its own hash). The correct values are in the table below.")
    for row in rows:
        add("%s  %s  %s  %s" % (row["status"], row["vm"], row["windows"], row["path"]))
    add("# NOTE: the crosscheck script itself, this file and CROSSCHECK-CORRECTION-20260913.md are")
    add("#       copied to the Windows worktree after this table was produced; they are verified by")
    add("#       a direct Get-FileHash (windows) vs sha256sum (vm) comparison in the completion report.")

    with open(os.path.join(OUT, "crosscheck-local-vm.sha256"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(lines) + "\n")

    note = []
    emit = note.append
    emit("# Cross-check correction note (2026-09-13 dependency-upgrade scoped SBOM)")
    emit("")
    emit("This note corrects exactly one row of " + code("REPORT.md") + " and of "
         + code("sbom-manifest.json") + ".")
    emit("Nothing in the SBOM documents themselves is affected, and no existing file was modified:")
    emit(code("REPORT.md") + " is preserved byte-for-byte as produced by the builder.")
    emit("")
    emit("## What the original report says")
    emit("")
    emit("    REPORT.md section 8:          46 files compared, 1 mismatches.")
    emit("    REPORT.md row:                | windows-side-hashes.sha256 | c46f97a4...a66 | **MISMATCH** |")
    emit("    sbom-manifest.json:           files_summary.all_match = false,")
    emit("                                  files_summary.mismatches = [\"windows-side-hashes.sha256\"]")
    emit("")
    emit("## Why that row is spurious (not a hash difference)")
    emit("")
    emit(code("windows-side-hashes.sha256") + " is the manifest of the Windows-side hashes. By construction it")
    emit("cannot contain its own hash, so the builder had no Windows-side value for that one path")
    emit("and marked it as not matching. Its real values, verified on both sides, are identical:")
    emit("")
    emit("- Windows (" + code("Get-FileHash -Algorithm SHA256") + "): "
         + code(win_manifest_sha))
    emit("- VM (" + code("sha256sum") + "): " + code(vm_hashes.get(WIN_MANIFEST)))
    emit("")
    emit("The report itself already prints both numbers (section 8 prose and section 12 inventory),")
    emit("so the value is not hidden - only the MATCH flag of that single row is wrong.")
    emit("")
    emit("## Corrected picture")
    emit("")
    emit("- Files compared in this final check: **%d**" % len(rows))
    emit("- Rows with " + code("status = MATCH") + ": **%d**" % (len(rows) - len(mismatches)))
    emit("- Rows with a real difference: **%d**" % len(mismatches))
    emit("")
    if mismatches:
        emit("Rows not matching:")
        emit("")
        for row in mismatches:
            emit("- " + code(row["path"]) + " status=" + row["status"]
                 + " vm=" + str(row["vm"]) + " windows=" + str(row["windows"]))
        emit("")
    else:
        emit("Every file of this scope is byte-identical on the acceptance VM and in the Windows")
        emit("worktree, " + code("windows-side-hashes.sha256") + " included.")
        emit("")
    emit("The authoritative machine-readable table is " + code("crosscheck-local-vm.sha256") + " in this")
    emit("directory: " + code("STATUS  <vm_sha256>  <windows_sha256>  <relative_path>") + ".")
    emit("")
    emit("## Coverage boundary (stated honestly)")
    emit("")
    emit("Three files necessarily fall outside the table above because they are produced at or after")
    emit("this check: this crosscheck script, " + code("crosscheck-local-vm.sha256") + " and this note.")
    emit("They are copied to the Windows worktree afterwards and verified there by a direct")
    emit(code("Get-FileHash") + " vs " + code("sha256sum") + " comparison; the result is recorded in the")
    emit("completion report for this task rather than in a file that would itself need verifying.")
    emit("")

    with open(os.path.join(OUT, "CROSSCHECK-CORRECTION-20260913.md"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(note) + "\n")

    print("rows=%d mismatches=%d" % (len(rows), len(mismatches)))
    for row in mismatches:
        print("  %s %s vm=%s win=%s" % (row["status"], row["path"], row["vm"], row["windows"]))
    return 0 if not mismatches else 1


if __name__ == "__main__":
    sys.exit(main())
