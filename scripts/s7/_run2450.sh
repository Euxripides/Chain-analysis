set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
echo "=== 1) 重新拉起 NATS ==="
sudo -n docker start deploy-nats-1
T0=$(date +%s.%N)
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8222/healthz && { T1=$(date +%s.%N); python3 -c "print('nats ready after %.2f s' % ($T1-$T0))"; break; }; sleep 1; done
sudo -n docker inspect -f '{{.State.Status}} restarts={{.RestartCount}}' deploy-nats-1
echo "=== 2) 平台恢复 ==="
curl -s http://127.0.0.1:8080/health/ready; echo
echo "=== 3) 重新声明 restart 策略 ==="
for c in deploy-postgres-1 deploy-clickhouse-1 deploy-minio-1 deploy-nats-1; do
  sudo -n docker update --restart=unless-stopped $c >/dev/null && echo "updated $c"
done
echo SCRIPT_END
