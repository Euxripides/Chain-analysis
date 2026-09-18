cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
echo "=== 公网 HTTPS 端到端（Cloudflare -> tunnel -> nginx -> api） ==="
curl -s -D /tmp/ph -o /tmp/pb -c /tmp/pj -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" \
  https://aurore.online/api/auth/v1/login
echo "login status: $(head -1 /tmp/ph | tr -d '\r')"
echo "set-cookie:"; grep -i '^set-cookie' /tmp/ph | sed 's/=[^;]*/=<redacted>/' | tr -d '\r'
CS=$(grep -i '__Host-cs_admin_session_csrf' /tmp/pj | awk '{print $7}')
echo "=== 带 __Host- 会话 Cookie 调 /session（HTTPS，curl 会发送 Secure Cookie） ==="
curl -s -o /tmp/ps -w 'session %{http_code}\n' -b /tmp/pj -H "X-CSRF-Token: $CS" https://aurore.online/api/auth/v1/session
head -c 200 /tmp/ps; echo
echo "=== 后台只读接口 ==="
curl -s -o /dev/null -w 'admin overview %{http_code}\n' -b /tmp/pj https://aurore.online/api/admin/v1/overview
echo "=== 退出并确认清理 ==="
curl -s -D /tmp/pl -o /dev/null -b /tmp/pj -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST https://aurore.online/api/auth/v1/logout
grep -ci 'set-cookie' /tmp/pl
curl -s -o /dev/null -w 'after logout session %{http_code}\n' -b /tmp/pj https://aurore.online/api/auth/v1/session
echo SCRIPT_END
