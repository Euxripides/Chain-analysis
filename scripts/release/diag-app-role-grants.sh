set -uo pipefail
cd /home/euripides/projects/blockchain-platform
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
q(){ printf "%s\n" "$1" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -F'|'; }
echo "=== ACL on download_jobs ==="
q "SELECT relacl::text FROM pg_class WHERE relname='download_jobs';"
echo "=== RLS on download_jobs ==="
q "SELECT relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname='download_jobs';"
echo "=== policies ==="
q "SELECT polname, polroles::regrole[]::text, polcmd FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid WHERE c.relname='download_jobs';"
echo "=== grants to blockchain_app on download_jobs ==="
q "SELECT grantee, privilege_type FROM information_schema.role_table_grants WHERE table_name='download_jobs' ORDER BY 1,2;"
echo "=== is blockchain_api member with inherit? ==="
q "SELECT r.rolname, m.inherit_option, m.set_option, m.admin_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname='blockchain_api';"
echo "=== what does APP actually query? sample of API process env/user ==="
q "SELECT usename, application_name, client_addr, count(*) FROM pg_stat_activity WHERE usename='blockchain_api' GROUP BY 1,2,3;"
echo "=== can admin role select download_jobs ==="
q "SELECT count(*) FROM download_jobs;"
echo "=== API log tail (last 15) ==="
journalctl --user -u blockchain-api.service -n 15 --no-pager 2>&1 | tail -15
echo "=== recent API 500/denied in log? ==="
journalctl --user -u blockchain-api.service --since '-40min' --no-pager 2>&1 | grep -iE 'permission denied|error|fatal' | tail -10
