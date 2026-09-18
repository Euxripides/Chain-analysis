#!/usr/bin/env bash
# r9-openpgp-vex.sh — 把根的 GO-2026-5932 独立证明复制进唯一 release EVID，并生成 scoped VEX。
# 只复制、只追加；不修改根的任何原始文件。
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
ROOTPROOF=/tmp/root-openpgp-proof-20260913T023646Z-672f6a
EV="$V/acceptance-evidence/dual-system-v1/sprint7/release/uc-download-import-20260913T021911Z/scoped-vex"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
TS=$(date -u +%Y%m%dT%H%M%SZ)
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "copied_from=$ROOTPROOF"
echo "=== copy root proof (read-only source preserved) ==="
cp -a "$ROOTPROOF" "$EV/root-openpgp-proof"
find "$EV/root-openpgp-proof" -type f | sort
echo "=== hashes of the copied proof ==="
find "$EV/root-openpgp-proof" -type f -exec sha256sum {} \; | sort -k2 > "$EV/root-openpgp-proof.sha256.txt"
wc -l "$EV/root-openpgp-proof.sha256.txt"
echo "=== live artifact hashes + symbol proof re-check (independent of root, same VM) ==="
export PATH=/usr/local/go/bin:$PATH
for b in api worker migrate; do
  f="$V/backend/bin/$b"
  printf '%s sha256=%s size=%s\n' "$b" "$(sha256sum "$f" | cut -d' ' -f1)" "$(stat -c%s "$f")"
  printf '  nm_openpgp_symbols=%s nm_rc=%s\n' "$(nm "$f" 2>/dev/null | grep -ci openpgp)" "$(nm "$f" >/dev/null 2>&1; echo $?)"
done | tee "$EV/live-artifact-recheck.txt"
echo "=== done ==="
echo "EVID=$EV"
