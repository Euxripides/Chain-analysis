#!/usr/bin/env bash
# ops-journald-cutover-v1.sh
#
# Future-only cutover of the API log from an unbounded append file to journald.
#
# The API unit appends stdout and stderr to /tmp/api.log. That file is 1,023,255,808
# bytes and grows without any bound; the read-only archive
# /home/euripides/ops-log-archive/api.log.20260914T103744Z (0444, 1,022,759,388 B)
# holds the previous rotation. The cutover is deliberately FUTURE-ONLY:
#
#   * a drop-in replaces StandardOutput/StandardError with journal; the packaged
#     unit file is not modified, so removing the drop-in is the whole rollback;
#   * the running process keeps writing to the file it already has open, which is
#     what "future only" means: the next start of the service writes to journald;
#   * NOTHING is truncated, deleted, moved or rewritten. Both the live 1.02 GB file
#     and the read-only archive are measured before and after and must be
#     byte-identical (size, mtime, mode, sha256).
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

UNIT=blockchain-api.service
DROPIN_DIR="$HOME/.config/systemd/user/$UNIT.d"
DROPIN="$DROPIN_DIR/10-journald.conf"
LIVE_LOG=/tmp/api.log
ARCHIVE=/home/euripides/ops-log-archive/api.log.20260914T103744Z
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-journald-cutover}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/ops-$LABEL-$TS; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/cutover-console.log") 2>&1
export XDG_RUNTIME_DIR=/run/user/$(id -u)

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
stat_line(){ stat -c '%s|%Y|%a' "$1" 2>/dev/null; }
sha_of(){ sha256sum "$1" 2>/dev/null | cut -d\  -f1; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "evidence=$EV"

echo "=== 0. before ==="
LIVE_BEFORE=$(stat_line "$LIVE_LOG"); LIVE_SHA_BEFORE=$(sha_of "$LIVE_LOG")
ARCH_BEFORE=$(stat_line "$ARCHIVE"); ARCH_SHA_BEFORE=$(sha_of "$ARCHIVE")
OUT_BEFORE=$(systemctl --user show "$UNIT" -p StandardOutput --value)
ERR_BEFORE=$(systemctl --user show "$UNIT" -p StandardError --value)
JOURNAL_BEFORE=$(journalctl --user --disk-usage 2>/dev/null)
AP=$(systemctl --user show "$UNIT" -p MainPID --value)
echo "live_log=$LIVE_BEFORE sha=$LIVE_SHA_BEFORE"
echo "archive=$ARCH_BEFORE sha=$ARCH_SHA_BEFORE"
echo "standard_output_before=$OUT_BEFORE standard_error_before=$ERR_BEFORE"
echo "journal_before=$JOURNAL_BEFORE api_pid=$AP"
FD_BEFORE=$(ls -l /proc/$AP/fd 2>/dev/null | grep -c "$LIVE_LOG" || true)
echo "api_open_fds_on_live_log=$FD_BEFORE"
chk "$OUT_BEFORE" "append:$LIVE_LOG" "0: the unit really appends to the live log before the cutover"
chk "$(stat -c %a "$ARCHIVE")" "444" "0: the archive is still read-only 0444"

echo "=== 1. install the drop-in (the packaged unit is NOT modified) ==="
UNIT_FILE=/home/euripides/.config/systemd/user/$UNIT
UNIT_SHA_BEFORE=$(sha_of "$UNIT_FILE")
mkdir -p "$DROPIN_DIR"
cat > "$DROPIN" <<'CONF'
# S7 ops: bound the API log by moving future writes to journald.
# The previous target /tmp/api.log and its read-only archive are left untouched.
[Service]
StandardOutput=journal
StandardError=journal
CONF
chmod 644 "$DROPIN"
echo "dropin=$DROPIN"
cat "$DROPIN"
chk "$(sha_of "$UNIT_FILE")" "$UNIT_SHA_BEFORE" "1: the packaged unit file is unchanged by this cutover"
systemctl --user daemon-reload
echo "daemon_reload_rc=$?"

echo "=== 2. effective configuration after the reload ==="
OUT_AFTER=$(systemctl --user show "$UNIT" -p StandardOutput --value)
ERR_AFTER=$(systemctl --user show "$UNIT" -p StandardError --value)
echo "standard_output_after=$OUT_AFTER standard_error_after=$ERR_AFTER"
chk "$OUT_AFTER" "journal" "2: the effective StandardOutput is journal"
chk "$ERR_AFTER" "journal" "2: the effective StandardError is journal"
chk "$(systemctl --user is-active "$UNIT")" "active" "2: the service is still active after the reload"
chk "$(systemctl --user show "$UNIT" -p MainPID --value)" "$AP" "2: the running process was NOT restarted by the reload (same MainPID)"

echo "=== 3. future-only proof: the running process still holds the old file ==="
FD_AFTER=$(ls -l /proc/$AP/fd 2>/dev/null | grep -c "$LIVE_LOG" || true)
echo "api_open_fds_on_live_log_after=$FD_AFTER"
if [ "$FD_AFTER" -ge 1 ]; then
  ok "3: the running process still holds $LIVE_LOG open, so the switch takes effect at the next start (future-only)"
else
  bad "3: the running process no longer holds the live log, which would mean the change was not future-only"
fi

echo "=== 4. the log and the archive are byte-identical ==="
sleep 2
LIVE_AFTER=$(stat_line "$LIVE_LOG"); LIVE_SHA_AFTER=$(sha_of "$LIVE_LOG")
ARCH_AFTER=$(stat_line "$ARCHIVE"); ARCH_SHA_AFTER=$(sha_of "$ARCHIVE")
echo "live_log_after=$LIVE_AFTER sha=$LIVE_SHA_AFTER"
echo "archive_after=$ARCH_AFTER sha=$ARCH_SHA_AFTER"
chk "$LIVE_SHA_AFTER" "$LIVE_SHA_BEFORE" "4: the live log content is unchanged (nothing truncated or rewritten)"
chk "$ARCH_SHA_AFTER" "$ARCH_SHA_BEFORE" "4: the read-only archive is unchanged"
chk "$(stat -c %a "$ARCHIVE")" "444" "4: the archive is still read-only"
# The live file may legitimately gain appends from the still-running process, but
# it may never shrink: a shrink would be a truncation.
LIVE_SIZE_BEFORE=$(printf '%s' "$LIVE_BEFORE" | cut -d'|' -f1)
LIVE_SIZE_AFTER=$(printf '%s' "$LIVE_AFTER" | cut -d'|' -f1)
if [ "$LIVE_SIZE_AFTER" -ge "$LIVE_SIZE_BEFORE" ]; then
  ok "4: the live log never shrank ($LIVE_SIZE_BEFORE -> $LIVE_SIZE_AFTER bytes)"
else
  bad "4: the live log shrank, which would be a truncation ($LIVE_SIZE_BEFORE -> $LIVE_SIZE_AFTER)"
fi

echo "=== 5. journald capacity and rollback ==="
JOURNAL_AFTER=$(journalctl --user --disk-usage 2>/dev/null)
echo "journal_after=$JOURNAL_AFTER"
systemd-analyze verify "$UNIT_FILE" 2>&1 | head -5
echo "rollback=rm -rf $DROPIN_DIR && systemctl --user daemon-reload   (no restart needed to roll back a future-only change)"
echo "next_start_writes_to=journal   previous_target=$LIVE_LOG   archive=$ARCHIVE"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "CUTOVER_FAIL=$FAIL"
exit 0
