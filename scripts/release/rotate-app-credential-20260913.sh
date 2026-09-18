#!/usr/bin/env bash
# rotate-app-credential-20260913.sh
# Rotate the PostgreSQL application-role credential exposed in plaintext into a DSH
# session transcript (sealed session seq 8699/8700: role=blockchain_api, pwlen=43,
# pw_sha12=49da98fb5866). Fail-closed. Never prints a secret. SQL via stdin only.
# FROZEN once written - corrections go to a new -v2 file, never hot-edited.
set -uo pipefail

REPO=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EVID="$REPO/acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-$TS"
PSQL_HOST=127.0.0.1
PSQL_PORT=5433
PSQL_DB=blockchain_platform
APP_ROLE=blockchain_api
ADMIN_ROLE=blockchain
FAILED=0
STEP=0
NEWCONN=0

mkdir -p "$EVID"; chmod 700 "$EVID"
say()  { echo "[$(date -u +%H:%M:%S)] $*"; }
fail(){ echo "[$(date -u +%H:%M:%S)] FAIL: $*"; FAILED=$((FAILED+1)); }
sha12(){ printf '%s' "$1" | sha256sum | cut -c1-12; }

cd "$REPO" || { echo "FATAL: repo missing"; exit 90; }
echo "=== rotate-app-credential START ts=$TS ==="
echo "REPO=$REPO APP_ROLE=$APP_ROLE EVID=$EVID"

# ------------------------------------------------------------------ [1] preflight
STEP=$((STEP+1)); say "[$STEP] preflight"
command -v psql >/dev/null || fail "psql missing"
command -v openssl >/dev/null || fail "openssl missing"
[ -f deploy/.env ] || fail "deploy/.env missing"
for s in blockchain-api blockchain-worker blockchain-mcp; do
  a=$(systemctl --user is-active "$s.service" 2>/dev/null)
  [ "$a" = active ] || fail "$s not active (=$a)"
done
[ "$FAILED" -ne 0 ] && { echo "ABORT preflight FAILED=$FAILED"; exit 91; }

PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M)
print(m.group(1).strip())")
OLD=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(m.group(1))")
[ -n "$PGP" ] || fail "POSTGRES_PASSWORD empty"
[ -n "$OLD" ] || fail "DATABASE_URL password empty"
[ "$FAILED" -ne 0 ] && { echo "ABORT preflight-creds FAILED=$FAILED"; exit 91; }
OLDLEN=$(printf '%s' "$OLD" | wc -c)
OLDSHA=$(sha12 "$OLD")
echo "old_app_pw_len=$OLDLEN old_app_pw_sha12=$OLDSHA"
echo "old_app_pw_expected_sha12=49da98fb5866"
[ "$OLDSHA" = "49da98fb5866" ] || fail "live app pw sha12 does NOT match the leaked value - stop, state changed"

psqlq(){ local pw="$1"; shift
  PGPASSWORD="$pw" psql -h "$PSQL_HOST" -p "$PSQL_PORT" -U "$ADMIN_ROLE" -d "$PSQL_DB" \
    -v ON_ERROR_STOP=1 -At -F'|' "$@"; }
psqlapp(){ local pw="$1"; shift
  PGPASSWORD="$pw" psql -h "$PSQL_HOST" -p "$PSQL_PORT" -U "$APP_ROLE" -d "$PSQL_DB" \
    -v ON_ERROR_STOP=1 -At -F'|' "$@"; }

snap_counts(){
  echo "--- jobs by status ---"
  psqlq "$PGP" -c "SELECT 'job:'||status,count(*)::text FROM download_jobs GROUP BY status ORDER BY 1;"
  echo "--- runtime row counts ---"
  psqlq "$PGP" -c "SELECT 'cnt:download_jobs',count(*)::text FROM download_jobs UNION ALL SELECT 'cnt:download_shards',count(*) FROM download_shards UNION ALL SELECT 'cnt:checkpoints',count(*) FROM checkpoints UNION ALL SELECT 'cnt:outbox_events',count(*) FROM outbox_events UNION ALL SELECT 'cnt:job_event_log',count(*) FROM job_event_log UNION ALL SELECT 'cnt:platform_jobs',count(*) FROM platform_jobs UNION ALL SELECT 'cnt:backup_jobs',count(*) FROM backup_jobs ORDER BY 1;"
  echo "--- shards by status ---"
  psqlq "$PGP" -c "SELECT 'shard:'||status,count(*)::text FROM download_shards GROUP BY status ORDER BY 1;"
}
snap_meta(){
  echo "--- role attrs ---"
  printf "SELECT rolname,rolcanlogin,rolsuper,rolbypassrls,rolcreatedb,rolcreaterole,coalesce(rolvaliduntil::text,'none') FROM pg_roles WHERE rolname IN ('%s','%s') ORDER BY rolname;\n" "$ADMIN_ROLE" "$APP_ROLE" | psqlq "$PGP"
  echo "--- memberships ---"
  printf "SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname='%s';\n" "$APP_ROLE" | psqlq "$PGP"
  echo "--- app role live backends ---"
  printf "SELECT count(*)::text,coalesce(min(backend_start)::text,'none') FROM pg_stat_activity WHERE usename='%s';\n" "$APP_ROLE" | psqlq "$PGP"
}
say "capture PRE state"
snap_counts > "$EVID/pre-counts.txt" 2>&1
snap_meta   > "$EVID/pre-meta.txt"   2>&1
cat "$EVID/pre-counts.txt" "$EVID/pre-meta.txt"
cp -p deploy/.env "$EVID/deploy.env.pre-app-rotation.bak"; chmod 600 "$EVID/deploy.env.pre-app-rotation.bak"
PRE_ENV_SHA=$(sha256sum deploy/.env | awk '{print $1}')
echo "pre_env_sha256=$PRE_ENV_SHA"; echo "$PRE_ENV_SHA" > "$EVID/deploy.env.pre.sha256"

printf "SELECT 'old_ok',count(*) FROM download_jobs;\n" | psqlapp "$OLD" > "$EVID/old-auth-precheck.txt" 2>&1
OLD_PRE_RC=$?
sed -n '1,5p' "$EVID/old-auth-precheck.txt"
if [ "$OLD_PRE_RC" -eq 0 ]; then say "old credential accepted pre-rotation (as expected)"
else fail "old credential REJECTED pre-rotation - unknown state, aborting"; fi
[ "$FAILED" -ne 0 ] && { echo "ABORT precheck FAILED=$FAILED"; exit 92; }

# ------------------------------------------------------------- [2] new credential
STEP=$((STEP+1)); say "[$STEP] generate new credential"
umask 077
NEW=$(openssl rand -hex 32)
printf '%s' "$NEW" > "$EVID/new-app.pw"; chmod 600 "$EVID/new-app.pw"
NEWLEN=$(printf '%s' "$NEW" | wc -c); NEWSHA=$(sha12 "$NEW")
echo "new_app_pw_len=$NEWLEN new_app_pw_sha12=$NEWSHA"
[ "$NEWLEN" -eq 64 ] || fail "new credential length != 64"
[ "$NEWSHA" != "$OLDSHA" ] || fail "new credential equals old"
[ "$FAILED" -ne 0 ] && { echo "ABORT newcred FAILED=$FAILED"; exit 93; }

# ------------------------------------------------------------------ [3] ALTER ROLE
STEP=$((STEP+1)); say "[$STEP] ALTER ROLE $APP_ROLE (SQL via stdin, no argv secret)"
ESCAPED=$(printf '%s' "$NEW" | sed "s/'/''/g")
if printf "ALTER ROLE %s WITH PASSWORD '%s';\n" "$APP_ROLE" "$ESCAPED" | psqlq "$PGP" > "$EVID/alter-role.out" 2>&1; then
  say "ALTER ROLE rc=0"
else
  fail "ALTER ROLE rc!=0"; sed -n '1,10p' "$EVID/alter-role.out"
fi
[ "$FAILED" -ne 0 ] && { echo "ABORT alter FAILED=$FAILED"; exit 94; }

# ------------------------------------------- [4] old rejected / new accepted (real)
STEP=$((STEP+1)); say "[$STEP] verify old rejected + new accepted over real TCP/SCRAM"
printf "SELECT 1;\n" | psqlapp "$OLD" > "$EVID/old-auth-after.txt" 2>&1
OLD_RC=$?
echo "old_auth_rc=$OLD_RC"; sed -n '1,6p' "$EVID/old-auth-after.txt"
[ "$OLD_RC" -ne 0 ] || fail "old credential STILL ACCEPTED after ALTER"
if grep -q "password authentication failed" "$EVID/old-auth-after.txt"; then
  say "old rejection = real SCRAM 'password authentication failed'"
else
  fail "old rejection reason is not the expected SCRAM failure text"
fi
printf "SELECT 'new_ok',count(*) FROM download_jobs;\n" | psqlapp "$NEW" > "$EVID/new-auth-after.txt" 2>&1
NEW_RC=$?
echo "new_auth_rc=$NEW_RC"; sed -n '1,6p' "$EVID/new-auth-after.txt"
[ "$NEW_RC" -eq 0 ] || fail "new credential rejected on real business query"
[ "$FAILED" -ne 0 ] && { echo "ABORT verify FAILED=$FAILED"; exit 95; }

# ------------------------------------------------------- [5] update all consumers
STEP=$((STEP+1)); say "[$STEP] update deploy/.env (DATABASE_URL line only, atomic 0600)"
python3 - "$NEW" <<'PY'
import sys,os,re,pathlib,tempfile
new=sys.argv[1]
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
[ "$PRE_ENV_SHA" != "$POST_ENV_SHA" ] || fail "deploy/.env unchanged after rewrite"
stat -c 'deploy.env mode=%a owner=%U:%G' deploy/.env
echo "$POST_ENV_SHA" > "$EVID/deploy.env.post.sha256"
CHK=$(python3 -c "
import re,pathlib,hashlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(hashlib.sha256(m.group(1).encode()).hexdigest()[:12])")
echo "env_now_app_pw_sha12=$CHK (expect $NEWSHA)"
[ "$CHK" = "$NEWSHA" ] || fail "deploy/.env does not carry the new credential"
[ "$FAILED" -ne 0 ] && { echo "ABORT consumer-update FAILED=$FAILED"; exit 96; }

# --------------------------------------------------------- [6] restart pools (proof)
STEP=$((STEP+1)); say "[$STEP] restart services to rebuild connection pools"
RESTART_EPOCH=$(date +%s)
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

# --------------------------------------------------------- [7] post runtime proof
STEP=$((STEP+1)); say "[$STEP] post runtime proof"
HC=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/live)
AN=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/download-jobs)
echo "health_live=$HC anon_jobs=$AN"
[ "$HC" = "200" ] || fail "health/live=$HC"
[ "$AN" = "401" ] || fail "anon download-jobs=$AN (expect 401)"
snap_meta > "$EVID/post-meta.txt" 2>&1
cat "$EVID/post-meta.txt"
NEWCONN=$(printf "SELECT count(*) FROM pg_stat_activity WHERE usename='%s' AND extract(epoch from backend_start) >= %s;\n" "$APP_ROLE" "$RESTART_EPOCH" | psqlq "$PGP")
echo "app_backends_started_after_restart=$NEWCONN"
[ "$NEWCONN" -ge 1 ] || fail "no fresh app-role backend after restart (pool not rebuilt with new credential)"

# ------------------------------------------------------- [8] business parity check
STEP=$((STEP+1)); say "[$STEP] business parity (counts) pre vs post"
snap_counts > "$EVID/post-counts.txt" 2>&1
if diff -u "$EVID/pre-counts.txt" "$EVID/post-counts.txt" > "$EVID/counts-diff.txt"; then
  say "PRE/POST business counts IDENTICAL - no task/progress loss"
else
  fail "business counts changed across rotation"; sed -n '1,80p' "$EVID/counts-diff.txt"
fi

# ------------------------------------------------------------------ [9] summary
STEP=$((STEP+1)); say "[$STEP] summary"
{
  echo "ts=$TS"
  echo "role=$APP_ROLE"
  echo "leaked_pw_len=43 leaked_pw_sha12=49da98fb5866"
  echo "old_pw_len=$OLDLEN old_pw_sha12=$OLDSHA"
  echo "new_pw_len=$NEWLEN new_pw_sha12=$NEWSHA"
  echo "old_auth_pre_rc=$OLD_PRE_RC old_auth_after_rc=$OLD_RC new_auth_after_rc=$NEW_RC"
  echo "env_sha_before=$PRE_ENV_SHA"
  echo "env_sha_after=$POST_ENV_SHA"
  echo "app_backends_after_restart=$NEWCONN"
  echo "FAILED=$FAILED"
} > "$EVID/summary.txt"
cat "$EVID/summary.txt"
echo "EVID=$EVID"
echo "=== rotate-app-credential END FAILED=$FAILED ==="
if [ "$FAILED" -gt 0 ]; then exit 1; fi
exit 0
