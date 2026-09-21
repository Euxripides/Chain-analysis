#!/usr/bin/env bash
# B5 read-compatibility: after rolling the write bucket back to raw, prove that
#   (1) a raw-only object (B2 sample 40a9b887), (2) an export-only object (B4 sample d4f3fdcd) and
#   (3) a dual-copy migrated object (be026ec9)
# are all still readable through the dual-read path, byte-identical to PostgreSQL.
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P"
set -a; . deploy/.env; set +a
set -a; . /tmp/s6-e2e-env.sh; set +a
BASE=http://127.0.0.1:8080
PROBE=$P/acceptance-evidence/dual-system-v1/sprint7/b2raw-probe-calibration-20260913T133310Z/objectprobe-strict-v3
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${EV:-$P/acceptance-evidence/dual-system-v1/sprint7/b5-read-compat-$TS}
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee -a "$EV/run.log") 2>&1
PASS=0; FAIL=0
check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }
nonempty(){ if [ -n "$2" ]; then echo "  PASS $1 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $3 :: EMPTY"; FAIL=$((FAIL+1)); fi; }
pgq(){ docker exec deploy-postgres-1 psql -q -U blockchain -d blockchain_platform -tAc "SET ROLE blockchain_app; $1" | tail -1; }
J=$EV/cookies.txt; umask 077
L=$(curl -sS --max-time 30 -c "$J" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
check SETUP "verification client logged in" "$(printf '%s' "$L" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("success"))' 2>/dev/null)" "True"
echo "api_pid=$(systemctl --user show blockchain-api.service -p MainPID --value)" > "$EV/freeze.txt"
echo "api_sha=$(sha256sum $P/backend/bin/api | cut -d' ' -f1)" >> "$EV/freeze.txt"
echo "worker_sha=$(sha256sum $P/backend/bin/worker | cut -d' ' -f1)" >> "$EV/freeze.txt"
echo "proc_wb_set=$(tr '\0' '\n' < /proc/$(systemctl --user show blockchain-api.service -p MainPID --value)/environ | grep -c '^EXPORT_WRITE_BUCKET=' || true)" >> "$EV/freeze.txt"
cat "$EV/freeze.txt"
for ID in 40a9b887-6e7d-45d9-8923-2c989516501c d4f3fdcd-9687-4ea5-8317-b066f4e6569f be026ec9-2ea8-498d-8c68-ba7328131ff8; do
  REF=$(pgq "select object_ref from exports where id='$ID'")
  PSHA=$(pgq "select sha256 from exports where id='$ID'")
  ST=$(pgq "select status from exports where id='$ID'")
  nonempty "$ID" "$REF" "PG object_ref present (status=$ST)"
  nonempty "$ID" "$PSHA" "PG sha256 present"
  "$PROBE" -action=stat-strict -key="$REF" -out "$EV/probe-$ID.json" > /dev/null 2>&1
  check "$ID" "strict probe rc" "$?" "0"
  echo "  $ID classes: $(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
print(' '.join(r['role']+'='+r['classification'] for r in d['results']))" "$EV/probe-$ID.json")"
  CODE=$(curl -sS --max-time 60 -b "$J" -H 'X-Auth-Audience: user' -o "$EV/file-$ID.bin" -w '%{http_code}' "$BASE/api/v1/exports/$ID/download")
  check "$ID" "authenticated full download returns 200" "$CODE" "200"
  check "$ID" "downloaded bytes sha256 == PG sha256" "$(sha256sum "$EV/file-$ID.bin" | cut -d' ' -f1)" "$PSHA"
done
echo "SUMMARY PASS=$PASS FAIL=$FAIL"
echo "pass=$PASS fail=$FAIL" > "$EV/gate.txt"
[ "$FAIL" -eq 0 ] && { echo "B5_READ_COMPAT=PASS"; exit 0; }
echo "B5_READ_COMPAT=FAIL"; exit 6
