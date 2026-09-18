cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/dr
ls -la
echo "=== report tail ==="
tail -n 45 restore-drill-20260912T173634Z.md 2>/dev/null
echo "=== ch restore log ==="
tail -n 10 /tmp/ch_restore.log 2>/dev/null
echo SCRIPT_END
