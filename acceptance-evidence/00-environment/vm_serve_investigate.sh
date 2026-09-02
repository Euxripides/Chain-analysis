#!/usr/bin/env bash
set +e
echo "=== frontend process cmdline (pid 1598016) ==="
ps -p 1598016 -o pid,lstart,cmd 2>/dev/null
tr '\0' ' ' < /proc/1598016/cmdline 2>/dev/null; echo
echo "=== listeners on 3000 ==="
ss -lntp | grep ':3000'
echo "=== any systemd service? ==="
systemctl list-units --type=service 2>/dev/null | grep -iE 'blockchain|frontend|api|worker' || echo "no systemd services"
echo "=== go toolchain ==="
which go && go version
echo "=== node toolchain ==="
which node npm && node --version && npm --version
echo "=== frontend node_modules present? ==="
ls -d /home/euripides/projects/blockchain-platform/frontend/node_modules 2>/dev/null && echo yes || echo no
echo "=== how was frontend started (nohup/screen)? ==="
ls /home/euripides/projects/blockchain-platform/*.sh /home/euripides/projects/blockchain-platform/scripts/* 2>/dev/null
grep -rln '3000|serve' /home/euripides/projects/blockchain-platform/scripts/ 2>/dev/null | head -3
echo "=== scripts dir content ==="
ls -la /home/euripides/projects/blockchain-platform/scripts/ 2>/dev/null
