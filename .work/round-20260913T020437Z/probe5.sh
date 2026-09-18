#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P" || exit 90
PSQL() { docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1 "$@"; }
echo "=== V2 export create payload (ExportsPage 330-372) ==="
sed -n '330,372p' "$P/frontend/apps/user-console/src/pages/ExportsPage.tsx"
echo "=== jobOptions source ==="
grep -n "useApi<" "$P/frontend/apps/user-console/src/pages/ExportsPage.tsx" | head
echo "=== v2 route mount ==="
grep -rn "api/v2" "$P/backend/internal/api/userv2.go" | head -20
echo "=== exports tables columns ==="
PSQL <<'SQL'
SELECT column_name,data_type FROM information_schema.columns WHERE table_name='exports' ORDER BY ordinal_position;
SQL
echo "=== download_subjects columns ==="
PSQL <<'SQL'
SELECT column_name FROM information_schema.columns WHERE table_name='download_subjects' ORDER BY ordinal_position;
SQL
echo "=== investigator user + memberships (names only) ==="
PSQL <<'SQL'
SELECT u.id, u.email, u.display_name FROM users u WHERE u.email = :'em';
SQL
echo "=== workspaces + memberships for fixture org ==="
PSQL <<'SQL'
SELECT w.id, w.slug, w.name, w.organization_id FROM workspaces w ORDER BY w.created_at DESC LIMIT 12;
SQL
echo "=== done ==="
