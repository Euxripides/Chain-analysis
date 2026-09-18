echo "=== audit/integrity 500 详情 ==="
grep -F '/api/admin/v1/audit/integrity' /tmp/api.log | tail -2 | cut -c1-700
echo "=== ERROR 级日志 ==="
grep '"level":"ERROR"' /tmp/api.log | tail -5 | cut -c1-400
echo SCRIPT_END
