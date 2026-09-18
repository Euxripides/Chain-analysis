D=/home/euripides/backups/blockchain-platform/20260912T171120Z
echo "=== tar.gz integrity ==="
gzip -t $D/clickhouse.tar.gz && echo "gzip OK"
tar tzf $D/clickhouse.tar.gz 2>/dev/null | head -5
echo "entries: $(tar tzf $D/clickhouse.tar.gz 2>/dev/null | wc -l)"
echo "=== extract into container ==="
cat $D/clickhouse.tar.gz | sudo -n docker exec -i deploy-clickhouse-1 sh -c 'cd /var/lib/clickhouse/backups && tar xzf -'
echo "extract rc=$?"
sudo -n docker exec deploy-clickhouse-1 sh -c 'du -sh /var/lib/clickhouse/backups/* 2>/dev/null'
echo "=== minio drill bucket ==="
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
sudo -n docker run --rm --network host -e MC_HOST_s3="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001" minio/mc:latest ls --recursive s3/blockchain-reports-drill-20260912T173634z 2>&1 | head -4
echo "count: $(sudo -n docker run --rm --network host -e MC_HOST_s3="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001" minio/mc:latest ls --recursive s3/blockchain-reports-drill-20260912T173634z 2>/dev/null | wc -l)"
echo SCRIPT_END
