sudo -n docker exec deploy-clickhouse-1 sh -c 'rm -rf /var/lib/clickhouse/backups/s7-probe; ls /var/lib/clickhouse/backups/ | head'
echo SCRIPT_END
