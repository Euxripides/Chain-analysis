#!/usr/bin/env bash
# recon-app-credential-state.sh — read-only. Prints ONLY fingerprints (len/sha12), never values.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform

python3 - <<'PY'
import re, hashlib, pathlib, json, subprocess, os
def fp(s): return hashlib.sha256(s.encode()).hexdigest()[:12]
def dsn_parts(v):
    m = re.match(r'^([A-Za-z0-9+]+)://([^:@/]+):([^@]*)@(.*)$', v)
    return m
out = {}
env = pathlib.Path('deploy/.env')
st = env.stat()
txt = env.read_text()
keys = {}
for line in txt.splitlines():
    mm = re.match(r'^([A-Z0-9_]+)=(.*)$', line)
    if mm: keys[mm.group(1)] = mm.group(2)
rep = {'env_mode': oct(st.st_mode & 0o777), 'env_size': st.st_size}
for k in ['DATABASE_URL','MIGRATION_DATABASE_URL','CLICKHOUSE_URL']:
    if k in keys:
        d = dsn_parts(keys[k])
        if d:
            pw = d.group(3)
            rep[k] = {'scheme':d.group(1),'user':d.group(2),'pwlen':len(pw),'sha12':fp(pw),
                      'nonalnum':sum(1 for c in pw if not c.isalnum()),'host':d.group(4)[:60]}
for k in ['POSTGRES_PASSWORD','CLICKHOUSE_PASSWORD','MINIO_ROOT_PASSWORD','POSTGRES_USER','MINIO_ROOT_USER']:
    if k in keys:
        v = keys[k]
        rep[k] = {'len':len(v),'sha12':fp(v)} if 'PASSWORD' in k else v
print(json.dumps(rep, indent=1, ensure_ascii=False))
PY
echo "=== api/worker runtime ==="
systemctl --user is-active blockchain-api.service blockchain-worker.service 2>&1
for s in blockchain-api blockchain-worker blockchain-mcp; do
  pid=$(systemctl --user show -p MainPID --value $s.service 2>/dev/null)
  echo "$s MainPID=$pid sha=$( [ -n "$pid" ] && [ -r /proc/$pid/exe ] && sha256sum /proc/$pid/exe | cut -c1-64 || echo NA)"
done
echo "=== consumers referencing the app DSN (paths only) ==="
grep -rl --binary-files=without-match -E 'DATABASE_URL' deploy/ scripts/ backend/cmd/ 2>/dev/null | sort | head -40
echo "=== systemd user units with EnvironmentFile ==="
grep -rl 'EnvironmentFile' ~/.config/systemd/user/ 2>/dev/null | sort
echo "=== timers ==="
systemctl --user list-timers --all --no-pager 2>&1 | head -20
