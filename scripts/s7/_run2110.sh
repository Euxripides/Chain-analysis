set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
echo "=== 1) 创建非超级用户应用登录角色 ==="
APPPW=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -v ON_ERROR_STOP=1 <<SQL
DO \$\$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='blockchain_api') THEN
    CREATE ROLE blockchain_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION;
  END IF;
END
\$\$;
ALTER ROLE blockchain_api PASSWORD '$APPPW';
GRANT blockchain_app TO blockchain_api;
SQL
echo "=== 2) 自检：该角色不是超级用户、无继承权限 ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select rolname, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolinherit, rolcanlogin from pg_roles where rolname in ('blockchain','blockchain_app','blockchain_api')"
echo "=== 3) 更新 deploy/.env（应用 DSN 换账号，迁移账号保留 owner） ==="
python3 - "$APPPW" <<'PY'
import sys, urllib.parse
pw = sys.argv[1]
p = "/home/euripides/projects/blockchain-platform/deploy/.env"
lines = open(p, encoding="utf-8").read().splitlines()
out, have_mig = [], False
owner_url = None
for ln in lines:
    if ln.startswith("DATABASE_URL="):
        owner_url = ln.split("=", 1)[1]
        out.append(ln)
    elif ln.startswith("MIGRATION_DATABASE_URL="):
        have_mig = True
        out.append(ln)
    else:
        out.append(ln)
assert owner_url, "DATABASE_URL 未找到"
if not have_mig:
    out.append("MIGRATION_DATABASE_URL=" + owner_url)
app_url = owner_url.replace("postgres://blockchain:", "postgres://blockchain_api:" + urllib.parse.quote(pw, safe="") + "@", 1)
for i, ln in enumerate(out):
    if ln.startswith("DATABASE_URL="):
        out[i] = "DATABASE_URL=" + app_url
open(p, "w", encoding="utf-8").write("\n".join(out) + "\n")
print("env updated")
PY
grep -E '^(DATABASE_URL|MIGRATION_DATABASE_URL)=' deploy/.env | sed -E 's#://([^:]+):[^@]+@#://\1:<redacted>@#'
echo "=== 4) 迁移工具仍用 owner（应成功且幂等） ==="
./backend/bin/migrate 2>&1 | tail -2
echo SCRIPT_END
