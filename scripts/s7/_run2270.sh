D=/home/euripides/backups/blockchain-platform/20260912T171120Z
ls -la $D/ | tail -5
echo "--- minio ---"; du -sh $D/minio/* 2>/dev/null | tail -6
echo "--- procs ---"; ps aux | grep -E '[b]ackup-all|[t]ar czf|[m]irror|[d]ocker run' | head -4 | cut -c1-130
df -h / | tail -1
echo SCRIPT_END
