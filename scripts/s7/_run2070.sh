set -e
cd /home/euripides/projects/blockchain-platform
cp deploy/.env deploy/.env.bak-s7-cookie-$(date -u +%Y%m%dT%H%M%SZ)
echo "=== before ==="; grep -E '^AUTH_COOKIE' deploy/.env || echo "(none)"
python3 - <<'PY'
import re
p = "/home/euripides/projects/blockchain-platform/deploy/.env"
lines = open(p, encoding="utf-8").read().splitlines()
out, seen_secure, seen_prefix = [], False, False
for ln in lines:
    if ln.startswith("AUTH_COOKIE_SECURE="):
        out.append("AUTH_COOKIE_SECURE=auto"); seen_secure = True
    elif ln.startswith("AUTH_COOKIE_PREFIX="):
        out.append("AUTH_COOKIE_PREFIX=__Host-"); seen_prefix = True
    else:
        out.append(ln)
if not seen_secure:
    out.append("AUTH_COOKIE_SECURE=auto")
if not seen_prefix:
    out.append("AUTH_COOKIE_PREFIX=__Host-")
open(p, "w", encoding="utf-8").write("\n".join(out) + "\n")
print("updated")
PY
echo "=== after ==="; grep -E '^AUTH_COOKIE' deploy/.env
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
echo SCRIPT_END
