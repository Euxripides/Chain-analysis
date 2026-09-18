ls -la /home/euripides/backups/blockchain-platform/ 2>/dev/null | tail -5
echo "--- current backup progress ---"
ls -la /home/euripides/backups/blockchain-platform/$(ls -t /home/euripides/backups/blockchain-platform/ 2>/dev/null | head -1)/ 2>/dev/null
echo "--- CH backup dir size ---"
sudo -n docker exec deploy-clickhouse-1 sh -c 'du -sh /var/lib/clickhouse/backups/* 2>/dev/null' | tail -3
echo "--- process ---"
ps aux | grep -E 'backup-all|mc ' | grep -v grep | head -3 | cut -c1-120
echo SCRIPT_END
