cd /home/euripides/projects/blockchain-platform
echo "=== env files present ==="
ls -la /tmp/*env* /tmp/fin-*.log 2>/dev/null | head
echo "--- s6-e2e-env.sh ---"; cat /tmp/s6-e2e-env.sh 2>/dev/null | sed 's/=.*/=<redacted>/'
echo "=== fixture binaries ==="
ls -la backend/bin/ | head -20
echo "=== users in DB ==="
set -a; . deploy/.env; set +a
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select u.email, i.status, i.id::text from users u join identities i on i.user_id=u.id order by u.email limit 20"
echo "=== roles assigned ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select ro.key, count(*) from member_role_bindings b join roles ro on ro.id=b.role_id where b.revoked_at is null group by 1 order by 1"
echo "=== tests dir ==="
ls -la acceptance-evidence/dual-system-v1/tests/ | head -20
echo "=== last s6 env hints ==="
head -30 acceptance-evidence/dual-system-v1/tests/run-sprint6-e2e.sh | grep -n 'env\|PASSWORD' | head