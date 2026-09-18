D=/home/euripides/backups/blockchain-platform/20260912T171120Z
ls -la $D/ | tail -4
du -sh $D/minio/* 2>/dev/null | tail -6
ps aux | grep -E '[t]ar czf|[m]irror|[d]ocker run' | head -3 | cut -c1-120
df -h / | tail -1
echo SCRIPT_END
