set -x
echo "== trivy version =="
trivy --version
echo
cat /root/.cache/trivy/db/metadata.json 2>&1
echo
scan_one() {
  REF="$1"; OUT="$2"
  echo "########## IMAGE: $REF ##########"
  echo "--- docker image digest ---"
  docker image inspect "$REF" --format '{{json .RepoDigests}}|{{.Id}}|{{.Created}}' 2>&1 || echo "inspect_failed"
  echo "--- trivy image JSON ---"
  trivy image --scanners vuln --image-src docker \
    --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
    --db-repository docker.m.daocloud.io/aquasec/trivy-db \
    --timeout 30m --cache-dir /root/.cache/trivy \
    --format json --output "/out/images/$OUT.json" "$REF"
  echo "JSON_EXIT_$OUT=$?"
  echo "--- trivy image TABLE ---"
  trivy image --scanners vuln --image-src docker \
    --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
    --db-repository docker.m.daocloud.io/aquasec/trivy-db \
    --timeout 30m --cache-dir /root/.cache/trivy \
    --format table --output "/out/images/$OUT.txt" "$REF"
  echo "TABLE_EXIT_$OUT=$?"
  echo
}
scan_one "postgres:18"                       "postgres-18"
scan_one "clickhouse/clickhouse-server:26.7" "clickhouse-clickhouse-server-26.7"
scan_one "nats:2-alpine"                     "nats-2-alpine"
scan_one "minio/minio:latest"                "minio-minio-latest"
echo "INNER_DONE=1"
