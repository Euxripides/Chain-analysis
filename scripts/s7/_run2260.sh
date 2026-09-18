D=/home/euripides/backups/blockchain-platform/20260912T171120Z
ls -la $D/ | tail -6
du -sh $D/minio/* 2>/dev/null
echo "--- backup proc ---"; ps aux | grep -E '[b]ackup-all|[t]ar czf|[m]irror' | head -3 | cut -c1-140
echo "--- ch backup ---"; sudo -n docker exec deploy-clickhouse-1 sh -c 'du -sh /var/lib/clickhouse/backups/*' | tail -2
echo SCRIPT_END
