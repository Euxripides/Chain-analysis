echo "=== restart policies ==="
for c in deploy-postgres-1 deploy-clickhouse-1 deploy-minio-1 deploy-nats-1; do
  printf '%-22s %s  %s\n' "$c" "$(sudo -n docker inspect -f '{{.HostConfig.RestartPolicy.Name}}' $c 2>/dev/null)" "$(sudo -n docker inspect -f '{{.State.Status}}' $c 2>/dev/null)"
done
echo "=== nats logs tail ==="
sudo -n docker logs deploy-nats-1 2>&1 | tail -5
echo SCRIPT_END
