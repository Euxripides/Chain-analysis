D=/home/euripides/backups/blockchain-platform/20260912T171120Z
ls -la $D/ 2>/dev/null
echo "--- minio ---"; du -sh $D/minio/* 2>/dev/null | tail -6
echo "--- ch backup size ---"; sudo -n docker exec deploy-clickhouse-1 sh -c 'du -sh /var/lib/clickhouse/backups/* 2>/dev/null' | tail -2
echo "--- disk ---"; df -h / | tail -1
echo "--- mc running? ---"; ps aux | grep -c '[m]c:latest'
echo SCRIPT_END
