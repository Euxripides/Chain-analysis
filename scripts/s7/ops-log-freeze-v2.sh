#!/usr/bin/env bash
# ops-log-freeze-v2.sh
#
# Ops P1, phase 1, v2. Same goal as v1 (freeze and archive the unbounded
# application log without touching it), with the ordering defect of v1 repaired.
#
# v1 measured the source size, THEN hashed the source. The service appended 868
# bytes between the two reads, so the recorded size no longer described the
# hashed content and the size assertion failed even though the copy was byte
# exact. v1 and its failed round are preserved unchanged.
#
# v2 makes the proof order-independent:
#   * the snapshot copy is authoritative, and
#   * the copy is compared against the SOURCE PREFIX of exactly the copy length,
#     which is immutable while the file only grows.
# REUSE_ARCHIVE=<existing snapshot> re-verifies an earlier snapshot instead of
# writing a second 1 GB copy.
#
# ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
SRC="${SRC:-/tmp/api.log}"
REUSE_ARCHIVE="${REUSE_ARCHIVE:-}"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/ops-log-freeze-v2-$TS}"
ARCH="${ARCH:-/home/euripides/ops-log-archive}"
mkdir -p "$EV" "$ARCH"; chmod 700 "$EV"
exec > >(tee "$EV/freeze-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }

echo "ts_utc=$TS src=$SRC evidence=$EV archive=$ARCH reuse=$REUSE_ARCHIVE"
if [ ! -f "$SRC" ]; then bad "the source log exists"; echo "GATE_FAIL=$FAIL"; exit 0; fi

S_PRE=$(stat -c %s "$SRC")
printf 'path=%s\nsize_before_snapshot=%s\ninode=%s\nmtime=%s\nmode=%s\n' \
  "$SRC" "$S_PRE" "$(stat -c %i "$SRC")" "$(stat -c %y "$SRC")" "$(stat -c %a "$SRC")" \
  > "$EV/original-identity-before.txt"
cat "$EV/original-identity-before.txt"

if [ -n "$REUSE_ARCHIVE" ]; then
  DEST="$REUSE_ARCHIVE"
  [ -f "$DEST" ] || { bad "the reused archive exists"; echo "GATE_FAIL=$FAIL"; exit 0; }
  echo "reusing_snapshot=$DEST"
else
  DEST="$ARCH/api.log.$TS"
  cp --reflink=auto --preserve=mode,timestamps "$SRC" "$DEST"
  chmod 0444 "$DEST"
fi

SNAP_SIZE=$(stat -c %s "$DEST")
SNAP_SHA=$(sha256sum "$DEST" | cut -d\  -f1)
PREFIX_SHA=$(head -c "$SNAP_SIZE" "$SRC" | sha256sum | cut -d\  -f1)
chk "$PREFIX_SHA" "$SNAP_SHA" "the snapshot is byte-identical to the source prefix of the same length"
if [ "$SNAP_SIZE" -ge "$S_PRE" ]; then ok "the snapshot covers at least everything the pre-copy measurement saw"; else bad "the snapshot is smaller than the pre-copy measurement"; fi
S_POST=$(stat -c %s "$SRC")
L_POST=$(wc -l < "$SRC")
echo "snapshot_path=$DEST snapshot_size=$SNAP_SIZE snapshot_sha256=$SNAP_SHA"
echo "source_size_after=$S_POST source_lines_after=$L_POST source_grew_bytes=$(( S_POST - S_PRE ))"
printf 'snapshot_path=%s\nsnapshot_size=%s\nsnapshot_sha256=%s\nsource_size_after=%s\nsource_lines_after=%s\nsource_grew_bytes=%s\n' \
  "$DEST" "$SNAP_SIZE" "$SNAP_SHA" "$S_POST" "$L_POST" "$(( S_POST - S_PRE ))" > "$EV/archive-identity.txt"
chk "$(stat -c %a "$DEST")" "444" "the archived snapshot is read-only"
SNAP_LINES=$(wc -l < "$DEST")
echo "snapshot_lines=$SNAP_LINES"
if [ "$SNAP_LINES" -gt 2400000 ]; then ok "the archived snapshot really contains the unbounded log ($SNAP_LINES lines)"; else bad "the archived snapshot has only $SNAP_LINES lines"; fi

UNIT=$HOME/.config/systemd/user/blockchain-api.service
cp --preserve=mode,timestamps "$UNIT" "$EV/blockchain-api.service.frozen"
sha256sum "$UNIT" > "$EV/unit-sha256.txt"; cat "$EV/unit-sha256.txt"
chk "$(grep -c 'StandardOutput=append:/tmp/api.log' "$EV/blockchain-api.service.frozen")" "1" "the frozen unit really appends stdout to the unbounded file"
chk "$(grep -c 'StandardError=append:/tmp/api.log' "$EV/blockchain-api.service.frozen")" "1" "the frozen unit appends stderr to the same file"
chk "$(fgrep -c 'logrotate' "$EV/blockchain-api.service.frozen")" "0" "the frozen unit has no rotation hook"

echo "=== journald envelope (read only) ==="
journalctl --disk-usage 2>&1 | tail -1
ls -ld /var/log/journal 2>&1
echo "journal_persistent=$([ -d /var/log/journal ] && echo yes || echo no)"
echo "journald_conf_sections=$(grep -cE '^\[' /etc/systemd/journald.conf 2>/dev/null || echo 0)"
echo "journald_conf_tunables=$(grep -cE '^(SystemMaxUse|SystemMaxFileSize|MaxRetentionSec|Storage)=' /etc/systemd/journald.conf 2>/dev/null || echo 0)"
echo "tmp_fstype=$(df --output=fstype /tmp | tail -1)"
echo "tmp_avail_kb=$(df --output=avail -k /tmp | tail -1 | tr -d ' ')"
echo "tmpfiles_will_age_the_original=the original stays under /tmp, so the read-only archive above is the durable copy"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
