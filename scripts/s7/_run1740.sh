find /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1 -maxdepth 2 -type d -name 'sprint7*' -o -maxdepth 2 -type d -name db | head
echo "---"
ls -la /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/ 2>&1
echo "--- db ---"
ls -la /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/db/ 2>&1
echo "--- tests/sprint7 ---"
ls -la /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/ 2>&1 | head