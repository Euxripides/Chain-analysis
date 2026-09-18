echo "=== CH backups dir now ==="
sudo -n docker exec deploy-clickhouse-1 sh -c 'ls -la /var/lib/clickhouse/backups/ 2>/dev/null'
echo "=== tar content head ==="
gunzip -c /home/euripides/backups/blockchain-platform/20260912T171120Z/clickhouse.tar.gz 2>/dev/null | tar tzf - 2>/dev/null | head -5
echo "=== tar entries count (first 1000) ==="
gunzip -c /home/euripides/backups/blockchain-platform/20260912T171120Z/clickhouse.tar.gz 2>/dev/null | tar tzf - 2>/dev/null | head -1000 | wc -l
echo "=== minio check ==="
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
sudo -n docker run --rm --network host -e MC_HOST_s3="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001" minio/mc:latest ls s3 2>&1 | head -8
echo SCRIPT_END
