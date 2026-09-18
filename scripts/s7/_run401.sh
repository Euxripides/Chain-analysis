cd /home/euripides/projects/blockchain-platform
echo "=== how api/worker are started ==="
ps -o pid,ppid,lstart,args -p $(pgrep -f 'bin/api' | head -1) 2>/dev/null
ps -o pid,ppid,lstart,args -p $(pgrep -f 'bin/worker' | head -1) 2>/dev/null
echo "=== start scripts in repo ==="
ls -la bin/ 2>/dev/null | head; ls deploy/*.sh 2>/dev/null; ls *.sh 2>/dev/null
grep -n 'nohup\|setsid\|systemctl\|bin/api' scripts/deploy_dual_system.py | head -30
echo "=== /tmp api logs ==="
ls -la /tmp/*.log 2>/dev/null | head -12
echo "=== running api env (DB_RUNTIME_ROLE) ==="
sudo -n tr '\0' '\n' < /proc/$(pgrep -f 'bin/api' | head -1)/environ 2>/dev/null | grep -E 'AUTH_MODE|APP_ENV|DB_RUNTIME_ROLE|API_HTTP_PORT|BUILD_SHA' || echo "(no sudo read)"