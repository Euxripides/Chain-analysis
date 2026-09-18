pkill -f 'run-sprint7-qa.sh' ; sleep 1; pkill -f 'run-sprint5-e2e.sh'; sleep 1
ps aux | grep -E 'run-sprint' | grep -v grep | head -5
rm -rf /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/qa
echo KILLED