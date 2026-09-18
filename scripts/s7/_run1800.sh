set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
echo "=== CH config: disks/backups ==="
sudo -n docker exec deploy-clickhouse-1 sh -c 'ls /etc/clickhouse-server/config.d/ 2>/dev/null; grep -rn -A6 "<storage_configuration>\|<disks>\|backups" /etc/clickhouse-server/config.xml 2>/dev/null | head -40'
echo "=== CH allowed path ==="
sudo -n docker exec deploy-clickhouse-1 sh -c 'grep -n "path\|tmp_path" /etc/clickhouse-server/config.xml | head -6'
echo "=== CH version ==="
sudo -n docker exec deploy-clickhouse-1 clickhouse-client --user $CLICKHOUSE_USER --password $CLICKHOUSE_PASSWORD --query "select version()"
echo "=== minio containers/images ==="
sudo -n docker images | grep -i 'minio\|mc' | head -5
echo "=== buckets ==="
sudo -n docker exec deploy-minio-1 sh -c 'ls -la /data 2>/dev/null | head -12'
echo "=== disk free ==="; df -h /var/lib/docker 2>/dev/null | tail -1
echo SCRIPT_END
