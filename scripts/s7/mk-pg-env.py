import sys,urllib.parse,os
"""Emit PG* env for psql/pg_dump from deploy/.env.

备份/迁移类操作需要 owner 角色（应用角色 blockchain_api 对 schema_migrations 等表没有
权限，pg_dump 会因 LOCK TABLE 被拒），因此这里优先使用 MIGRATION_DATABASE_URL，
没有时才回落到 DATABASE_URL。
"""
env=sys.argv[1]; out=sys.argv[2]
vals={}
for line in open(env,encoding="utf-8",errors="replace"):
    if "=" not in line or line.startswith("#"): continue
    k,v=line.rstrip("\n").split("=",1); vals[k]=v
dsn=vals.get("MIGRATION_DATABASE_URL") or vals.get("DATABASE_URL","")
source="MIGRATION_DATABASE_URL" if vals.get("MIGRATION_DATABASE_URL") else "DATABASE_URL"
if not dsn.startswith("postgres://"):
    print("NO_DSN",file=sys.stderr); sys.exit(2)
u=urllib.parse.urlsplit(dsn)
if not u.password: print("NO_PASSWORD_IN_DSN",file=sys.stderr); sys.exit(2)
def q(s): return "'" + str(s).replace("'","'\\''") + "'"
lines=["export PGHOST=%s"%q(u.hostname or "127.0.0.1"),
       "export PGPORT=%s"%q(u.port or 5432),
       "export PGUSER=%s"%q(urllib.parse.unquote(u.username or "")),
       "export PGDATABASE=%s"%q((u.path or "/").lstrip("/")),
       "export PGPASSWORD=%s"%q(urllib.parse.unquote(u.password))]
fd=os.open(out,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
os.write(fd,("\n".join(lines)+"\n").encode()); os.close(fd)
print("wrote %s mode=600 source=%s"%(out,source))
