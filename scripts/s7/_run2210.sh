set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go build -o bin/api ./cmd/api && go build -o bin/worker ./cmd/worker && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service blockchain-worker.service
for i in $(seq 1 90); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
systemctl --user is-active blockchain-api blockchain-worker
set -a; . deploy/.env; set +a
echo "=== 连接账号核对（应为 blockchain_api） ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select usename, count(*) conns from pg_stat_activity where datname='$POSTGRES_DB' group by 1 order by 2 desc"
echo "=== 请求与写操作 ==="
curl -s http://127.0.0.1:8080/health/ready; echo
. /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8080/api/auth/v1/login
CS=$(grep -i 'csrf' "$J" | awk '{print $7}')
printf "audit/verify -> "; curl -s -b "$J" -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/verify | python3 -c "import sys,json;d=json.load(sys.stdin);x=d.get('data') or {};print('HTTP200 valid=%s integrity=%s checked=%s' % (x.get('valid'), x.get('integrity_status'), x.get('checked')))"
echo "=== 审计链写入是否仍正常（登录事件应新增） ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from login_events"
echo SCRIPT_END
