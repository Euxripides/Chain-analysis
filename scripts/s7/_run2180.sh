cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
cp deploy/.env deploy/.env.bak-s7-role-$(date -u +%Y%m%dT%H%M%SZ)
python3 - <<'PY' 2>&1
import urllib.parse, re
pw = open("/tmp/s7-app-role.pw").read().strip()
p = "/home/euripides/projects/blockchain-platform/deploy/.env"
txt = open(p, encoding="utf-8").read()
lines = txt.splitlines()
cur_app = next((l.split("=",1)[1] for l in lines if l.startswith("DATABASE_URL=")), None)
cur_mig = next((l.split("=",1)[1] for l in lines if l.startswith("MIGRATION_DATABASE_URL=")), None)
assert cur_app, "DATABASE_URL missing"
mig_url = cur_mig or cur_app
new_app = re.sub(r"://[^:]+:[^@]+@", "://blockchain_api:" + urllib.parse.quote(pw, safe="") + "@", cur_app, count=1)
out = []
seen_mig = False
for l in lines:
    if l.startswith("DATABASE_URL="):
        out.append("DATABASE_URL=" + new_app)
    elif l.startswith("MIGRATION_DATABASE_URL="):
        out.append(l); seen_mig = True
    else:
        out.append(l)
if not seen_mig:
    out.append("MIGRATION_DATABASE_URL=" + mig_url)
open(p, "w", encoding="utf-8").write("\n".join(out) + "\n")
print("env updated; app user =", urllib.parse.urlparse(new_app).username, "migration user =", urllib.parse.urlparse(mig_url).username)
PY
grep -E '^(DATABASE_URL|MIGRATION_DATABASE_URL)=' deploy/.env | sed -E 's#://([^:]+):[^@]+@#://\1:<redacted>@#'
echo "=== 迁移工具（owner）应成功 ==="
./backend/bin/migrate 2>&1 | tail -2
echo "=== 重启 API/Worker ==="
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service blockchain-worker.service
for i in $(seq 1 90); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
systemctl --user is-active blockchain-api blockchain-worker
echo "=== 运行期连接账号核对 ==="
sudo -n tr '\0' '\n' < /proc/$(pgrep -f 'bin/api' | head -1)/environ 2>/dev/null | grep -E '^(DATABASE_URL|DB_RUNTIME_ROLE)=' | sed -E 's#://([^:]+):[^@]+@#://\1:<redacted>@#'
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select usename, count(*) from pg_stat_activity where datname='$POSTGRES_DB' group by 1 order by 2 desc"
echo SCRIPT_END
