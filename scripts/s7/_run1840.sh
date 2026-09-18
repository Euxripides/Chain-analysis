echo "=== fixture env keys ==="; cut -d= -f1 /tmp/dual-system-fixture.env
echo "=== s6 env keys ==="; cut -d= -f1 /tmp/s6-e2e-env.sh
echo "=== 501 endpoints probe ==="
for ep in /api/auth/v1/oidc/start /api/auth/v1/mfa/verify /api/admin/v1/alerts /api/auth/v1/methods; do
  printf "%-34s " "$ep"; curl -s -o /dev/null -w '%{http_code}\n' --max-time 5 "http://127.0.0.1:8080$ep"
done
echo SCRIPT_END
