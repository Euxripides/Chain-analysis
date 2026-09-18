cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8080/api/auth/v1/login
CR=$(grep -i 'csrf' "$J" | awk '{print $7}')
echo "=== GET /audit/integrity ==="
curl -s -b "$J" -H "X-CSRF-Token: $CR" -o /tmp/ai.json -w 'HTTP %{http_code}\n' "http://127.0.0.1:8080/api/admin/v1/audit/integrity?chain=audit"
head -c 300 /tmp/ai.json; echo
echo "=== POST /audit/integrity/preview（对照组） ==="
curl -s -b "$J" -H "X-CSRF-Token: $CR" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' -o /tmp/ap.json -w 'HTTP %{http_code}\n' "http://127.0.0.1:8080/api/admin/v1/audit/integrity/preview"
head -c 300 /tmp/ap.json; echo
echo "=== MinIO audit 桶对象 ==="
sudo -n docker run --rm --network host -e MC_HOST_s3="http://$MINIO_ROOT_USER:$MINIO_ROOT_PASSWORD@127.0.0.1:9001" minio/mc:latest ls --recursive s3/blockchain-audit 2>&1 | head -5
echo SCRIPT_END
