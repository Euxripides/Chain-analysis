cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
export XDG_RUNTIME_DIR=/run/user/$(id -u)
sleep 5
echo "=== 服务状态 ==="; systemctl --user is-active blockchain-api blockchain-worker
echo "=== 健康 ==="; curl -s http://127.0.0.1:8080/health/ready; echo
echo "=== 连接账号（应为 blockchain_api） ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select usename, count(*) conns, max(state) from pg_stat_activity where datname='$POSTGRES_DB' group by 1 order by 2 desc"
echo "=== 写请求 + 审计链校验（走 blockchain_api -> SET ROLE blockchain_app） ==="
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8080/api/auth/v1/login
CS=$(grep -i 'csrf' "$J" | awk '{print $7}')
curl -s -b "$J" -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/verify | python3 -c "import sys,json;d=json.load(sys.stdin);x=d.get('data') or {};print('valid=%s integrity=%s checked=%s' % (x.get('valid'), x.get('integrity_status'), x.get('checked')))"
echo "=== 运行角色对审计表的破坏性操作必须仍被拒绝 ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select has_table_privilege('blockchain_app','audit_events_v2','UPDATE') as upd, has_table_privilege('blockchain_app','audit_events_v2','DELETE') as del, has_table_privilege('blockchain_app','audit_events_v2','TRUNCATE') as tr"
echo "=== 清理测试残留：admin@local.display_name 的 XSS 载荷 ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select email, display_name from users where display_name like '%<img%' or display_name like '%onerror%'"
echo SCRIPT_END
