#!/usr/bin/env bash
# ops-log-freeze-v1.sh
#
# Ops P1, phase 1: freeze and archive the unbounded application log WITHOUT
# touching it.
#
# The problem: blockchain-api.service writes StandardOutput=append:/tmp/api.log
# with no rotation, so the file had grown to 1.02 GB / 2.5 M lines and the
# application log never reaches the journal at all.
#
# This phase performs NO change to the running service and NO write to the
# original file. It records an immutable identity for the current log, takes a
# byte-exact snapshot copy, and proves the copy equals the source prefix by
# hashing both ends. copytruncate is never used: it rewrites the original.
#
# ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
SRC="${SRC:-/tmp/api.log}"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/ops-log-freeze-$TS}"
ARCH="${ARCH:-$R/ops/log-archive}"
mkdir -p "$EV" "$ARCH"; chmod 700 "$EV"
exec > >(tee "$EV/freeze-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }

echo "ts_utc=$TS src=$SRC evidence=$EV archive=$ARCH"
if [ ! -f "$SRC" ]; then bad "the source log exists"; echo "GATE_FAIL=$FAIL"; exit 0; fi

# ---- 1. identity of the original, before anything else ----
S0=$(stat -c %s "$SRC")
L0=$(wc -l < "$SRC")
INODE=$(stat -c %i "$SRC")
MTIME=$(stat -c %y "$SRC")
MODE0=$(stat -c %a "$SRC")
SHA0=$(sha256sum "$SRC" | cut -d\  -f1)
printf 'path=%s\nsize=%s\nlines=%s\ninode=%s\nmtime=%s\nmode=%s\nsha256=%s\n' \
  "$SRC" "$S0" "$L0" "$INODE" "$MTIME" "$MODE0" "$SHA0" > "$EV/original-identity.txt"
cat "$EV/original-identity.txt"
echo "original_frozen sha256=$SHA0 size=$S0 lines=$L0"

# ---- 2. byte-exact snapshot copy (the original is opened read-only) ----
DEST="$ARCH/api.log.$TS"
cp --reflink=auto --preserve=mode,timestamps "$SRC" "$DEST"
chmod 0444 "$DEST"
S1=$(stat -c %s "$DEST")
SHA1=$(sha256sum "$DEST" | cut -d\  -f1)
chk "$S1" "$S0" "the snapshot copy has the same size as the frozen original"
chk "$SHA1" "$SHA0" "the snapshot copy is byte-identical to the frozen original"
# The service keeps appending while the copy runs, so also prove that the copy
# equals the SOURCE PREFIX of the same length. head reads, never writes.
PREFIX_SHA=$(head -c "$S1" "$SRC" | sha256sum | cut -d\  -f1)
chk "$PREFIX_SHA" "$SHA1" "the snapshot equals the source prefix of the same length"

# ---- 3. what the original looks like after the snapshot ----
S2=$(stat -c %s "$SRC")
L2=$(wc -l < "$SRC")
printf 'archive_path=%s\narchive_size=%s\narchive_sha256=%s\nsource_size_after=%s\nsource_lines_after=%s\n' \
  "$DEST" "$S1" "$SHA1" "$S2" "$L2" > "$EV/archive-identity.txt"
cat "$EV/archive-identity.txt"
echo "source_grew_during_snapshot_bytes=$(( S2 - S0 ))"

# ---- 4. frozen copy of the unit that produced the unbounded file ----
UNIT=$HOME/.config/systemd/user/blockchain-api.service
cp --preserve=mode,timestamps "$UNIT" "$EV/blockchain-api.service.frozen"
sha256sum "$UNIT" > "$EV/unit-sha256.txt"
cat "$EV/unit-sha256.txt"
chk "$(grep -c 'StandardOutput=append:/tmp/api.log' "$EV/blockchain-api.service.frozen")" "1" "the frozen unit really appends to the unbounded file"
chk "$(grep -c 'StandardError=append:/tmp/api.log' "$EV/blockchain-api.service.frozen")" "1" "the frozen unit appends stderr to the same file"

# ---- 5. the retention envelope that the next phase will rely on ----
echo "=== journald envelope (read only) ==="
journalctl --disk-usage 2>&1 | tail -1
ls -ld /var/log/journal 2>&1
journalctl --header 2>/dev/null | grep -m2 -E '^(File path|Sequential)' || true
echo "journald_conf_non_comment_entries=$(grep -vcE '^#|^$' /etc/systemd/journald.conf 2>/dev/null || echo 0)"
echo "tmp_filesystem=$(df --output=source,fstype,avail -k /tmp | tail -1)"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
