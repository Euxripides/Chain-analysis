ls -la /usr/bin/chromium* /usr/bin/google-chrome* /snap/bin/chromium 2>/dev/null
echo "---"
which chromium chromium-browser 2>/dev/null
echo "--- iam SMOKE-09 context ---"
sed -n '45,60p' /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/run-iam-e2e.sh
echo SCRIPT_END
