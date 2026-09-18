set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go build -o bin/auditverify ./cmd/auditverify && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform; set -a; . deploy/.env; set +a
echo "=== live chain verification ==="
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/$POSTGRES_DB?sslmode=disable" ./backend/bin/auditverify 2>&1 | tee /tmp/verify-live.json | head -40
echo "=== restored-copy chain verification ==="
DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/blockchain_platform_dr_restore?sslmode=disable" ./backend/bin/auditverify 2>&1 | tee /tmp/verify-restore.json | head -40