#!/usr/bin/env bash
# s7-secret-scan-coverage.sh -- coverage audit for the build-time credential scan.
#
# WHY (root, 2026-09-13): the frozen build-and-freeze.sh extracts literal scan patterns from
# a fixed list of variable names in deploy/.env.  When a name is absent or its value is empty
# the extraction silently produces NO pattern, yet the report still reads
# "secret_patterns_checked=N ... literal_hits=0".  A skipped variable and a scanned variable
# are therefore indistinguishable in that report, and 0 hits can be misread as full coverage.
#
# This script makes the coverage explicit and fail-closed:
#   1. discover the REAL sensitive configuration sources:
#        - deploy/.env (file)
#        - systemd user unit Environment= / EnvironmentFile= for api/worker
#        - the live runtime environment of the running API process (/proc/<pid>/environ)
#   2. classify every discovered sensitive key: COVERED_BY_SCAN / MISSING_FROM_SCAN /
#      EMPTY_VALUE / UNREADABLE_SOURCE
#   3. exit non-zero when any discovered sensitive key with a non-empty value is NOT covered
#      by the scan pattern set, i.e. "skipped" may never be reported as "0 hits".
#
# It never prints a secret value: only the key name, its length and a 12-char sha prefix.
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
ENV_FILE="${ENV_FILE:-$R/deploy/.env}"
EV="${EV:-}"
[ -n "$EV" ] && { mkdir -p "$EV"; }
emit(){ if [ -n "$EV" ]; then tee -a "$EV/secret-scan-coverage.txt"; else cat; fi; }

SENSITIVE_RE='(PASSWORD|PASSWD|SECRET|TOKEN|API_KEY|APIKEY|PRIVATE_KEY|CREDENTIAL|_DSN$|DATABASE_URL|_URL$|MCP_KEY)'
SCAN_COVERED_RE='^(POSTGRES_PASSWORD|CLICKHOUSE_PASSWORD|MINIO_ROOT_PASSWORD|DATABASE_URL|MIGRATION_DATABASE_URL|CLICKHOUSE_URL|GPT_ACTIONS_API_KEY|GPT_ACTIONS_ADMIN_KEY|MCP_API_KEY|ANKR_API_KEY|CHAINBASE_API_KEY|TRONGRID_API_KEY)$'

TMP=$(mktemp -d /tmp/s7seccov-XXXXXX); chmod 700 "$TMP"
KEYS="$TMP/keys.txt"      # key<TAB>source
NONEMPTY="$TMP/nonempty.txt"
: > "$KEYS"; : > "$NONEMPTY"

add_key(){ printf '%s\t%s\n' "$1" "$2" >> "$KEYS"; }

echo "=== source 1: env file ($ENV_FILE) ===" | emit
if [ -r "$ENV_FILE" ]; then
  echo "env_file_readable=1 bytes=$(stat -c %s "$ENV_FILE") mode=$(stat -c %a "$ENV_FILE")" | emit
  while IFS= read -r line; do
    case "$line" in \#*|"") continue;; esac
    key="${line%%=*}"; val="${line#*=}"
    case "$key" in *[!A-Za-z0-9_]*) continue;; esac
    if printf '%s' "$key" | grep -Eq "$SENSITIVE_RE"; then
      add_key "$key" "env_file"
      if [ -n "$val" ]; then printf '%s\n' "$key" >> "$NONEMPTY"; fi
    fi
  done < "$ENV_FILE"
else
  echo "env_file_readable=0" | emit
fi

echo "=== source 2: systemd user units ===" | emit
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
for svc in blockchain-api.service blockchain-worker.service blockchain-mcp.service; do
  unit=$(systemctl --user cat "$svc" 2>/dev/null || true)
  [ -z "$unit" ] && { echo "unit_missing=$svc" | emit; continue; }
  echo "$unit" | grep -E '^Environment=' | sed -E 's/^Environment=//' | tr ' ' '\n' | while IFS= read -r kv; do
    key="${kv%%=*}"; val="${kv#*=}"
    case "$key" in ""|*[!A-Za-z0-9_]*) continue;; esac
    if printf '%s' "$key" | grep -Eq "$SENSITIVE_RE"; then printf '%s\tsystemd:%s\n' "$key" "$svc" >> "$KEYS"; printf '%s\n' "$key" >> "$NONEMPTY"; fi
  done
  echo "$unit" | grep -E '^EnvironmentFile=' | while IFS= read -r ef; do
    f=$(printf '%s' "${ef#EnvironmentFile=-}" | tr -d '-')
    echo "systemd_environment_file=$svc $f readable=$([ -r "$f" ] && echo 1 || echo 0)" | emit
  done
done

echo "=== source 3: live runtime env of the running API ===" | emit
APID=$(systemctl --user show -p MainPID --value blockchain-api.service 2>/dev/null | tr -d '[:space:]')
if [ -n "$APID" ] && [ "$APID" != "0" ] && [ -r "/proc/$APID/environ" ]; then
  tr '\0' '\n' < "/proc/$APID/environ" | while IFS= read -r kv; do
    key="${kv%%=*}"; val="${kv#*=}"
    case "$key" in ""|*[!A-Za-z0-9_]*) continue;; esac
    if printf '%s' "$key" | grep -Eq "$SENSITIVE_RE"; then printf '%s\tlive_env:pid%s\n' "$key" "$APID" >> "$KEYS"; [ -n "$val" ] && printf '%s\n' "$key" >> "$NONEMPTY"; fi
  done
  echo "live_env_readable=1 api_pid=$APID" | emit
else
  echo "live_env_readable=0 api_pid=${APID:-none}" | emit
fi

echo "=== coverage table (key | sources | value len/sha12 | scan coverage) ===" | emit
COVERED=0; MISSING=0; EMPTYN=0
sort -u "$NONEMPTY" > "$TMP/nonempty.u"
sort -u "$KEYS" > "$TMP/keys.u"
while IFS=$'\t' read -r key src; do
  [ -z "$key" ] && continue
  srclist=$(awk -F'\t' -v k="$key" '$1==k{print $2}' "$TMP/keys.u" | paste -sd, -)
  val=$(grep -m1 "^$key=" "$ENV_FILE" 2>/dev/null | sed -E 's/^[^=]+=//' || true)
  if [ -z "$val" ] && [ -r "/proc/$APID/environ" ]; then
    val=$(tr '\0' '\n' < "/proc/$APID/environ" 2>/dev/null | grep -m1 "^$key=" | sed -E 's/^[^=]+=//' || true)
  fi
  if [ -z "$val" ]; then
    echo "$key | $srclist | EMPTY(nonenv) | EMPTY_VALUE" | emit
    EMPTYN=$((EMPTYN+1)); continue
  fi
  if printf '%s' "$key" | grep -Eq "$SCAN_COVERED_RE"; then
    echo "$key | $srclist | len=${#val} sha12=$(printf '%s' "$val" | sha256sum | cut -c1-12) | COVERED_BY_SCAN" | emit
    COVERED=$((COVERED+1))
  else
    echo "$key | $srclist | len=${#val} sha12=$(printf '%s' "$val" | sha256sum | cut -c1-12) | MISSING_FROM_SCAN" | emit
    MISSING=$((MISSING+1))
  fi
done < "$TMP/keys.u"

echo "=== summary ===" | emit
echo "discovered_sensitive_keys=$(wc -l < "$TMP/keys.u")" | emit
echo "nonempty_sensitive_keys=$(wc -l < "$TMP/nonempty.u")" | emit
echo "covered_by_scan=$COVERED missing_from_scan=$MISSING empty_value=$EMPTYN" | emit
echo "SECRET_SCAN_COVERAGE_VERDICT=$([ "$MISSING" -eq 0 ] && echo PASS || echo FAIL)" | emit
rm -rf "$TMP"
[ "$MISSING" -eq 0 ] || exit 1
exit 0
