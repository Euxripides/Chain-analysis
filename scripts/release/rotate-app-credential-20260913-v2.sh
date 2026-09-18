#!/usr/bin/env bash
# rotate-app-credential-20260913-v2.sh
# Rotate the PostgreSQL application-role credential exposed in plaintext into a DSH
# session transcript (sealed session seq 8699/8700: role=blockchain_api, pwlen=43,
# pw_sha12=49da98fb5866).
#
# Corrections carried over from v1 (see ERRATUM-app-rotation-v1-argv-20260913.md):
#   * v1 passed the NEW password as python argv (python3 - "$NEW"). v2 passes a
#     0600 FILE PATH in argv and the value only via file content / stdin.
#   * v1 precheck used a table SELECT without SET ROLE (INHERIT FALSE membership) and
#     wrongly reported the credential as rejected. v2 uses SELECT 1 for auth and
#     SET ROLE blockchain_app + real business query for authorization.
#   * v1 count UNION mixed text/bigint. Fixed with explicit ::text.
#   * v1 claimed "no task/progress loss" from row counts alone. v2 runs a real
#     content-level comparison (R1-R4) over jobs/shards/checkpoints/outbox.
# FROZEN once written. Do not hot-edit; corrections go to a new version.
set -uo pipefail

REPO=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EVID="${APP_ROTATION_EVID:-$REPO/acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-$TS}"
PSQL_HOST=127.0.0.1
PSQL_PORT=5433
PSQL_DB=blockchain_platform
APP_ROLE=blockchain_api
RUNTIME_ROLE=blockchain_app
ADMIN_ROLE=blockchain
LEAKED_SHA12=49da98fb5866
EXPECT_CAPTURE_SHA=d3ba0fab699556ad5728658e327c439cff63fec3c7f8c931a015a1212f3a240f
EXPECT_COMPARE_SHA=545f5da7ef03916ff36d2ddaaa1bbc700262e6668488569a095827317c2132b1
FAILED=0
STEP=0

mkdir -p "$EVID"; chmod 700 "$EVID"
say()  { echo "[$(date -u +%H:%M:%S)] $*"; }
fail(){ echo "[$(date -u +%H:%M:%S)] FAIL: $*"; FAILED=$((FAILED+1)); }
sha12(){ printf '%s' "$1" | sha256sum | cut -c1-12; }

cd "$REPO" || { echo "FATAL repo missing"; exit 90; }
echo "=== rotate-app-credential-v2 START ts=$TS ==="
echo "EVID=$EVID"

# ------------------------------------------------------------------ [1] preflight
STEP=$((STEP+1)); say "[$STEP] preflight"
for f in scripts/release/capture-runtime-content.sh scripts/release/compare-runtime-content.py; do
  h=$(sha256sum "$f" | cut -d' ' -f1); echo "frozen_helper $f sha256=$h"
done
[ "$(sha256sum scripts/release/capture-runtime-content.sh | cut -d' ' -f1)" = "$EXPECT_CAPTURE_SHA" ] || fail "capture script hash mismatch"
[ "$(sha256sum scripts/release/compare-runtime-content.py | cut -d' ' -f1)" = "$EXPECT_COMPARE_SHA" ] || fail "compare script hash mismatch"
command -v psql >/dev/null || fail "psql missing"
command -v openssl >/dev/null || fail "openssl missing"
for s in blockchain-api blockchain-worker blockchain-mcp; do
  a=$(systemctl --user is-active "$s.service" 2>/dev/null)
  echo "preflight $s active=$a"
  [ "$a" = active ] || fail "$s not active (=$a)"
done
[ -f deploy/.env ] || fail "deploy/.env missing"
RROLE=$(grep -m1 '^DB_RUNTIME_ROLE=' deploy/.env | cut -d= -f2-)
echo "DB_RUNTIME_ROLE=$RROLE (expect $RUNTIME_ROLE)"
[ "$RROLE" = "$RUNTIME_ROLE" ] || fail "DB_RUNTIME_ROLE != $RUNTIME_ROLE; ADR-017 runtime separation not in place"
[ "$FAILED" -ne 0 ] && { echo "ABORT preflight FAILED=$FAILED"; exit 91; }

PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
OLD=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M); print(m.group(1))")
[ -n "$PGP" ] || fail "POSTGRES_PASSWORD empty"
[ -n "$OLD" ] || fail "DATABASE_URL password empty"
[ "$FAILED" -ne 0 ] && { echo "ABORT preflight-creds FAILED=$FAILED"; exit 91; }
OLDLEN=$(printf '%s' "$OLD" | wc -c); OLDSHA=$(sha12 "$OLD")
echo "old_app_pw_len=$OLDLEN old_app_pw_sha12=$OLDSHA"
[ "$OLDSHA" = "$LEAKED_SHA12" ] || fail "live app pw sha12 != leaked value; state changed, stop"

psqlq(){ local pw="$1"; shift
  PGPASSWORD="$pw" psql -h "$PSQL_HOST" -p "$PSQL_PORT" -U "$ADMIN_ROLE" -d "$PSQL_DB" -v ON_ERROR_STOP=1 -At -F'|' "$@"; }
psqlapp(){ local pw="$1"; shift
  PGPASSWORD="$pw" psql -h "$PSQL_HOST" -p "$PSQL_PORT" -U "$APP_ROLE" -d "$PSQL_DB" -v ON_ERROR_STOP=1 -At -F'|' "$@"; }

snap_meta(){
  echo "--- role attrs ---"
  printf "SELECT rolname,rolcanlogin,rolsuper,rolbypassrls,rolcreatedb,rolcreaterole,coalesce(rolvaliduntil::text,'none') FROM pg_roles WHERE rolname IN ('%s','%s') ORDER BY rolname;\n" "$ADMIN_ROLE" "$APP_ROLE" | psqlq "$PGP"
  echo "--- memberships (inherit/set options) ---"
  printf "SELECT r.rolname, m.inherit_option, m.set_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname='%s';\n" "$APP_ROLE" | psqlq "$PGP"
  echo "--- app role live backends ---"
  printf "SELECT count(*)::text,coalesce(min(backend_start)::text,'none') FROM pg_stat_activity WHERE usename='%s';\n" "$APP_ROLE" | psqlq "$PGP"
}
snap_counts(){
  echo "--- jobs by status ---"
  psqlq "$PGP" -c "SELECT 'job:'||status, count(*)::text FROM download_jobs GROUP BY status ORDER BY 1;"
  echo "--- shards by status ---"
  psqlq "$PGP" -c "SELECT 'shard:'||status, count(*)::text FROM download_shards GROUP BY status ORDER BY 1;"
  echo "--- table row counts ---"
  psqlq "$PGP" -c "SELECT 'cnt:download_jobs',count(*)::text FROM download_jobs UNION ALL SELECT 'cnt:download_shards',count(*)::text FROM download_shards UNION ALL SELECT 'cnt:checkpoints',count(*)::text FROM checkpoints UNION ALL SELECT 'cnt:outbox_events',count(*)::text FROM outbox_events UNION ALL SELECT 'cnt:job_event_log',count(*)::text FROM job_event_log UNION ALL SELECT 'cnt:platform_jobs',count(*)::text FROM platform_jobs UNION ALL SELECT 'cnt:backup_jobs',count(*)::text FROM backup_jobs ORDER BY 1;"
}

# ------------------------------------------------------------------ [2] PRE state
STEP=$((STEP+1)); say "[$STEP] capture PRE state (meta + counts + content)"
snap_meta   > "$EVID/pre-meta.txt"   2>&1
snap_counts > "$EVID/pre-counts.txt" 2>&1
bash scripts/release/capture-runtime-content.sh PRE "$EVID/pre-content" > "$EVID/pre-content-capture.txt" 2>&1
CAPRC=$?
[ "$CAPRC" -eq 0 ] || fail "PRE content capture rc=$CAPRC"
sed -n '1,20p' "$EVID/pre-content-capture.txt"
cat "$EVID/pre-meta.txt" "$EVID/pre-counts.txt"
cp -p deploy/.env "$EVID/deploy.env.pre-app-rotation.bak"; chmod 600 "$EVID/deploy.env.pre-app-rotation.bak"
PRE_ENV_SHA=$(sha256sum deploy/.env | awk '{print $1}'); echo "$PRE_ENV_SHA" > "$EVID/deploy.env.pre.sha256"
echo "pre_env_sha256=$PRE_ENV_SHA"

# auth-only precheck (no table access; membership is INHERIT FALSE)
printf "SELECT 'auth_ok';\n" | psqlapp "$OLD" > "$EVID/old-auth-precheck.txt" 2>&1
OLD_PRE_RC=$?
sed -n '1,4p' "$EVID/old-auth-precheck.txt"
[ "$OLD_PRE_RC" -eq 0 ] || fail "old credential rejected pre-rotation - unknown state"
# authorization path precheck (SET ROLE then real business read)
printf "SET ROLE %s;\nSELECT 'biz_ok',count(*) FROM download_jobs;\n" "$RUNTIME_ROLE" | psqlapp "$OLD" > "$EVID/old-biz-precheck.txt" 2>&1
OLD_PRE_BIZ_RC=$?
sed -n '1,4p' "$EVID/old-biz-precheck.txt"
[ "$OLD_PRE_BIZ_RC" -eq 0 ] || fail "old credential cannot run business query via SET ROLE pre-rotation"
[ "$FAILED" -ne 0 ] && { echo "ABORT precheck FAILED=$FAILED"; exit 92; }

# ------------------- [2b] validate PRE artifacts + freeze manifest BEFORE ALTER
STEP=$((STEP+1)); say "[$STEP] validate PRE artifacts and freeze manifest BEFORE ALTER"
PMODE=$(stat -c %a "$EVID" 2>/dev/null)
echo "evid_mode=$PMODE"
[ "$PMODE" = "700" ] || fail "EVID mode not 700 (=$PMODE)"
[ -d "$EVID/pre-content" ] || fail "PRE content dir missing"
for t in download_jobs download_shards checkpoints outbox_events platform_jobs backup_jobs job_event_log; do
  f="$EVID/pre-content/$t.tsv.gz"
  if [ ! -s "$f" ]; then fail "PRE dump missing/empty: $t"; fi
done
python3 - "$EVID/pre-content/columns-pre.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))
need={'download_jobs','download_shards','checkpoints','outbox_events','platform_jobs','backup_jobs','job_event_log'}
missing=need-set(d)
if missing: raise SystemExit('columns-pre.json missing tables: %s'%sorted(missing))
for k,v in d.items():
    if not v.get('columns') or not v.get('pk'): raise SystemExit('bad spec for %s'%k)
print('columns-pre.json OK tables=%d'%len(d))
PY
COLRC=$?
[ "$COLRC" -eq 0 ] || fail "columns-pre.json invalid rc=$COLRC"
ROWS_NONEMPTY=$(grep -c 'rows=[1-9]' "$EVID/pre-content/content-PRE.txt")
echo "pre_tables_with_positive_rows=$ROWS_NONEMPTY"
[ "$ROWS_NONEMPTY" -ge 5 ] || fail "too few non-empty PRE tables ($ROWS_NONEMPTY)"
( cd "$EVID" && find . -type f -print0 | sort -z | xargs -0 sha256sum ) > "$EVID/MANIFEST-pre-ALTER.sha256" 2>/dev/null
MFCOUNT=$(wc -l < "$EVID/MANIFEST-pre-ALTER.sha256")
echo "pre_alter_manifest_entries=$MFCOUNT"
[ "$MFCOUNT" -ge 8 ] || fail "pre-ALTER manifest too small ($MFCOUNT)"
( cd "$EVID" && find . -type f -printf '%m %u:%g %10s %p\n' | sort -k4 ) > "$EVID/STAT-pre-ALTER.txt" 2>/dev/null
sed -n '1,40p' "$EVID/STAT-pre-ALTER.txt"
[ "$FAILED" -ne 0 ] && { echo "ABORT pre-ALTER validation FAILED=$FAILED"; exit 97; }
say "PRE artifacts validated and manifested; proceeding to ALTER"

# ------------------------------------------------------------- [3] new credential
STEP=$((STEP+1)); say "[$STEP] generate new credential into 0600 file (value never in argv)"
umask 077
NEWFILE="$EVID/new-app.pw"
openssl rand -hex 32 > "$NEWFILE"; chmod 600 "$NEWFILE"
NEW=$(cat "$NEWFILE")
NEWLEN=$(printf '%s' "$NEW" | wc -c); NEWSHA=$(sha12 "$NEW")
echo "new_app_pw_len=$NEWLEN new_app_pw_sha12=$NEWSHA (file $NEWFILE mode $(stat -c %a "$NEWFILE"))"
[ "$NEWLEN" -eq 64 ] || fail "new credential length != 64"
[ "$NEWSHA" != "$OLDSHA" ] || fail "new credential equals old"
[ "$FAILED" -ne 0 ] && { echo "ABORT newcred FAILED=$FAILED"; exit 93; }

# ------------------------------------------------------------------ [4] ALTER ROLE
STEP=$((STEP+1)); say "[$STEP] ALTER ROLE $APP_ROLE (SQL via stdin; no secret in argv)"
ESCAPED=$(printf '%s' "$NEW" | sed "s/'/''/g")
if printf "ALTER ROLE %s WITH PASSWORD '%s';\n" "$APP_ROLE" "$ESCAPED" | psqlq "$PGP" > "$EVID/alter-role.out" 2>&1; then
  say "ALTER ROLE rc=0"
else
  fail "ALTER ROLE rc!=0"; sed -n '1,10p' "$EVID/alter-role.out"
fi
[ "$FAILED" -ne 0 ] && { echo "ABORT alter FAILED=$FAILED"; exit 94; }

# ------------------------------------------- [5] old rejected / new accepted real
STEP=$((STEP+1)); say "[$STEP] old rejected + new accepted over real TCP/SCRAM"
printf "SELECT 1;\n" | psqlapp "$OLD" > "$EVID/old-auth-after.txt" 2>&1
OLD_RC=$?
echo "old_auth_rc=$OLD_RC"; sed -n '1,5p' "$EVID/old-auth-after.txt"
[ "$OLD_RC" -ne 0 ] || fail "old credential STILL ACCEPTED after ALTER"
grep -q "password authentication failed" "$EVID/old-auth-after.txt" \
  && say "old rejection = real SCRAM 'password authentication failed'" \
  || fail "old rejection reason not the expected SCRAM failure text"
printf "SELECT 'auth_ok';\n" | psqlapp "$NEW" > "$EVID/new-auth-after.txt" 2>&1
NEW_RC=$?
echo "new_auth_rc=$NEW_RC"; sed -n '1,4p' "$EVID/new-auth-after.txt"
[ "$NEW_RC" -eq 0 ] || fail "new credential rejected on real connection"
printf "SET ROLE %s;\nSELECT 'biz_ok',count(*) FROM download_jobs;\n" "$RUNTIME_ROLE" | psqlapp "$NEW" > "$EVID/new-biz-after.txt" 2>&1
NEW_BIZ_RC=$?
echo "new_biz_rc=$NEW_BIZ_RC"; sed -n '1,4p' "$EVID/new-biz-after.txt"
[ "$NEW_BIZ_RC" -eq 0 ] || fail "new credential cannot run real business query via SET ROLE"
[ "$FAILED" -ne 0 ] && { echo "ABORT verify FAILED=$FAILED"; exit 95; }

# ------------------------------------------------------- [6] update deploy/.env
STEP=$((STEP+1)); say "[$STEP] update deploy/.env DATABASE_URL (value read from 0600 file; only path in argv)"
python3 - "$NEWFILE" <<'PY'
import sys,os,re,pathlib,tempfile
path=sys.argv[1]                      # path only - never the value
new=open(path).read().strip()
if len(new)!=64: raise SystemExit('unexpected new credential length')
p=pathlib.Path('deploy/.env')
lines=p.read_text().splitlines(keepends=True)
n=0
for i,l in enumerate(lines):
    if l.startswith('DATABASE_URL='):
        m=re.match(r'^(DATABASE_URL=)([A-Za-z0-9+]+://)([^:@/]+):([^@]*)@(.*)$', l.rstrip('\n'))
        if not m: raise SystemExit('DATABASE_URL shape unexpected')
        lines[i]="%s%s%s:%s@%s\n"%(m.group(1),m.group(2),m.group(3),new,m.group(5)); n+=1
if n!=1: raise SystemExit('expected exactly 1 DATABASE_URL line, got %d'%n)
d=os.path.dirname(os.path.abspath('deploy/.env'))
fd,tmp=tempfile.mkstemp(dir=d,prefix='.env.rotate.')
os.write(fd,''.join(lines).encode()); os.close(fd); os.chmod(tmp,0o600); os.replace(tmp,'deploy/.env')
print('deploy/.env rewritten; DATABASE_URL lines updated =',n)
PY
PYRC=$?
[ "$PYRC" -eq 0 ] || fail "deploy/.env rewrite rc=$PYRC"
POST_ENV_SHA=$(sha256sum deploy/.env | awk '{print $1}')
echo "env_sha_before=$PRE_ENV_SHA"; echo "env_sha_after=$POST_ENV_SHA"
[ "$PRE_ENV_SHA" != "$POST_ENV_SHA" ] || fail "deploy/.env unchanged"
stat -c 'deploy.env mode=%a owner=%U:%G' deploy/.env
echo "$POST_ENV_SHA" > "$EVID/deploy.env.post.sha256"
CHK=$(python3 -c "
import re,pathlib,hashlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(hashlib.sha256(m.group(1).encode()).hexdigest()[:12])")
echo "env_now_app_pw_sha12=$CHK (expect $NEWSHA)"
[ "$CHK" = "$NEWSHA" ] || fail "deploy/.env does not carry the new credential"
RROLE2=$(grep -m1 '^DB_RUNTIME_ROLE=' deploy/.env | cut -d= -f2-)
[ "$RROLE2" = "$RUNTIME_ROLE" ] || fail "DB_RUNTIME_ROLE changed during rewrite"
[ "$FAILED" -ne 0 ] && { echo "ABORT env-update FAILED=$FAILED"; exit 96; }

# --------------------------------------------------------- [7] restart + pool proof
STEP=$((STEP+1)); say "[$STEP] restart services (rebuild pools on new credential)"
RESTART_EPOCH=$(date +%s); RESTART_ISO=$(date -u +%Y-%m-%dT%H:%M:%SZ)
for s in blockchain-api blockchain-worker blockchain-mcp; do
  if systemctl --user restart "$s.service" 2>"$EVID/restart-$s.err"; then say "restart $s rc=0"
  else fail "restart $s rc!=0"; sed -n '1,5p' "$EVID/restart-$s.err"; fi
done
sleep 8
for s in blockchain-api blockchain-worker blockchain-mcp; do
  a=$(systemctl --user is-active "$s.service" 2>/dev/null)
  pid=$(systemctl --user show -p MainPID --value "$s.service" 2>/dev/null)
  exe=""
  if [ -n "$pid" ] && [ "$pid" != "0" ] && [ -r "/proc/$pid/exe" ]; then exe=$(sha256sum "/proc/$pid/exe" | cut -c1-64); fi
  echo "$s active=$a MainPID=$pid exe_sha256=$exe"
  [ "$a" = active ] || fail "$s not active after restart (=$a)"
done

# --------------------------------------------------------- [8] end-to-end readiness
STEP=$((STEP+1)); say "[$STEP] end-to-end readiness through the real API process"
sleep 3
/usr/bin/curl -s -o "$EVID/health-ready.json" -w '%{http_code}' http://127.0.0.1:8080/health/ready > "$EVID/health-ready.code"
HR_CODE=$(cat "$EVID/health-ready.code")
echo "health_ready=$HR_CODE body=$(cat "$EVID/health-ready.json")"
[ "$HR_CODE" = "200" ] || fail "health/ready=$HR_CODE (pool did not come up on the new credential)"
grep -q '"postgres":"ok"' "$EVID/health-ready.json" || fail "health/ready postgres != ok"
grep -q '"clickhouse":"ok"' "$EVID/health-ready.json" || fail "health/ready clickhouse != ok"
HL=$(/usr/bin/curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/live)
AN=$(/usr/bin/curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/download-jobs)
echo "health_live=$HL anon_jobs=$AN"
[ "$HL" = "200" ] || fail "health/live=$HL"
[ "$AN" = "401" ] || fail "anon download-jobs=$AN (expect 401)"
snap_meta > "$EVID/post-meta.txt" 2>&1; cat "$EVID/post-meta.txt"
NEWCONN=$(printf "SELECT count(*) FROM pg_stat_activity WHERE usename='%s' AND extract(epoch from backend_start) >= %s;\n" "$APP_ROLE" "$RESTART_EPOCH" | psqlq "$PGP")
echo "app_backends_started_after_restart=$NEWCONN"
[ "$NEWCONN" -ge 1 ] || fail "no fresh app-role backend after restart"
PGCM=$(docker ps --format '{{.Names}}' | grep -E '^deploy-postgres' | head -1)
if [ -n "$PGCM" ]; then
  docker logs --since "$RESTART_ISO" "$PGCM" > "$EVID/pg-log-since-restart.txt" 2>&1
  AUTHFAILS=$(grep -c 'password authentication failed' "$EVID/pg-log-since-restart.txt")
  echo "pg_auth_failures_since_restart=$AUTHFAILS (container $PGCM)"
  [ "$AUTHFAILS" -eq 0 ] || fail "postgres logged $AUTHFAILS authentication failures after restart"
else
  echo "WARN: postgres container not found; pg log check skipped"
fi

# --------------------------------------------------------- [9] content parity gate
STEP=$((STEP+1)); say "[$STEP] content-level parity (R1-R4) pre vs post"
bash scripts/release/capture-runtime-content.sh POST "$EVID/post-content" > "$EVID/post-content-capture.txt" 2>&1
CAPRC2=$?
[ "$CAPRC2" -eq 0 ] || fail "POST content capture rc=$CAPRC2"
sed -n '1,20p' "$EVID/post-content-capture.txt"
python3 scripts/release/compare-runtime-content.py "$EVID/pre-content" "$EVID/post-content" "$EVID/content-parity.json" > "$EVID/content-parity.out" 2>&1
CMPRC=$?
sed -n '1,80p' "$EVID/content-parity.out"
[ "$CMPRC" -eq 0 ] || fail "content parity R1-R4 gates FAILED (rc=$CMPRC)"
snap_counts > "$EVID/post-counts.txt" 2>&1
diff -u "$EVID/pre-counts.txt" "$EVID/post-counts.txt" > "$EVID/counts-diff.txt" || say "NOTE: status/row counts differ (live workload) - see counts-diff.txt"

# ------------------------------------------------------------------ [10] summary
STEP=$((STEP+1)); say "[$STEP] summary"
CONTENT_CHANGED=$(python3 -c "
import json;d=json.load(open('$EVID/content-parity.json'));print(d.get('total_changed_rows','?'))")
{
  echo "ts=$TS"
  echo "role=$APP_ROLE runtime_role=$RUNTIME_ROLE"
  echo "leaked_pw_len=43 leaked_pw_sha12=$LEAKED_SHA12"
  echo "old_pw_len=$OLDLEN old_pw_sha12=$OLDSHA"
  echo "new_pw_len=$NEWLEN new_pw_sha12=$NEWSHA"
  echo "old_auth_pre_rc=$OLD_PRE_RC old_biz_pre_rc=$OLD_PRE_BIZ_RC"
  echo "old_auth_after_rc=$OLD_RC new_auth_after_rc=$NEW_RC new_biz_after_rc=$NEW_BIZ_RC"
  echo "env_sha_before=$PRE_ENV_SHA"
  echo "env_sha_after=$POST_ENV_SHA"
  echo "health_ready=$HR_CODE health_live=$HL anon_jobs=$AN"
  echo "app_backends_after_restart=$NEWCONN"
  echo "content_parity_total_changed_rows=$CONTENT_CHANGED"
  echo "content_parity_gates=R1(no row loss) R2(no row disappearance) R3(immutable job identity) R4(progress monotonicity)"
  echo "scope_note=content parity proves no loss/regression across the rotation window; it does NOT by itself prove the whole system is accepted"
  echo "FAILED=$FAILED"
} > "$EVID/summary.txt"
cat "$EVID/summary.txt"
echo "EVID=$EVID"
echo "=== rotate-app-credential-v2 END FAILED=$FAILED ==="
if [ "$FAILED" -gt 0 ]; then exit 1; fi
exit 0
