cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
echo "=== A) 直连 8080（HTTP，内网测试链路）：Cookie 应无 __Host-、无 Secure ==="
curl -s -D /tmp/h1 -o /dev/null -c /tmp/j1 -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8080/api/auth/v1/login
grep -i '^set-cookie' /tmp/h1 | sed 's/=[^;]*/=<redacted>/'
echo "=== B) 经 Nginx 8088（X-Forwarded-Proto: https）：应为 __Host- 前缀 + Secure ==="
curl -s -D /tmp/h2 -o /dev/null -c /tmp/j2 -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -H 'Host: aurore.online' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8088/api/auth/v1/login
grep -i '^set-cookie' /tmp/h2 | sed 's/=[^;]*/=<redacted>/'
echo "=== C) 经 Nginx 的会话是否可用（带 __Host- Cookie 调 /session） ==="
CS=$(grep -i '__Host-cs_admin_session_csrf' /tmp/j2 | awk '{print $7}')
curl -s -o /tmp/s.json -w 'session %{http_code}\n' -b /tmp/j2 -H "X-CSRF-Token: $CS" http://127.0.0.1:8088/api/auth/v1/session
head -c 200 /tmp/s.json; echo
echo "=== D) 退出登录是否清掉两种命名的 Cookie ==="
curl -s -D /tmp/h3 -o /dev/null -b /tmp/j2 -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST http://127.0.0.1:8088/api/auth/v1/logout
grep -ci 'set-cookie' /tmp/h3
grep -i '^set-cookie' /tmp/h3 | sed 's/=[^;]*/=<redacted>/' | head -4
echo SCRIPT_END
