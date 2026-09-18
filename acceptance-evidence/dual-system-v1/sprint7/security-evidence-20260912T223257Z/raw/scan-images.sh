#!/bin/bash
set -x
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"
mkdir -p "$EV/images"
TRIVY="aquasec/trivy:latest"
MIRROR="docker.m.daocloud.io/aquasec/trivy-db"

echo "=== trivy version ==="
docker run --rm $TRIVY --version
echo "=== trivy db metadata (from cache volume) ==="
docker run --rm -v trivy-cache:/root/.cache/trivy --entrypoint cat $TRIVY /root/.cache/trivy/db/metadata.json

scan_one() {
  REF="$1"; OUT="$2"
  echo "########## IMAGE: $REF ##########"
  echo "--- docker image inspect ---"
  docker image inspect "$REF" --format '{{json .RepoDigests}}|{{.Id}}|{{.Created}}|{{json .Config.Labels}}' 2>&1 || echo "inspect_failed"
  echo "--- trivy image JSON ---"
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v trivy-cache:/root/.cache/trivy \
    -v "$EV/images:/out" $TRIVY image --scanners vuln --image-src docker \
    --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
    --db-repository $MIRROR --timeout 30m --cache-dir /root/.cache/trivy \
    --format json --output "/out/$OUT.json" "$REF" 2>&1
  echo "JSON_EXIT_$OUT=$?"
  echo "--- trivy image TABLE ---"
  docker run --rm -v /var/run/docker.sock:/var/run/docker.sock -v trivy-cache:/root/.cache/trivy \
    -v "$EV/images:/out" $TRIVY image --scanners vuln --image-src docker \
    --severity UNKNOWN,LOW,MEDIUM,HIGH,CRITICAL \
    --db-repository $MIRROR --timeout 30m --cache-dir /root/.cache/trivy \
    --format table --output "/out/$OUT.txt" "$REF" 2>&1
  echo "TABLE_EXIT_$OUT=$?"
}

scan_one "postgres:18"                       "postgres-18"
scan_one "clickhouse/clickhouse-server:26.7" "clickhouse-clickhouse-server-26.7"
scan_one "nats:2-alpine"                     "nats-2-alpine"
scan_one "minio/minio:latest"                "minio-minio-latest"
scan_one "opensecurity/mobile-security-framework-mobsf:latest" "mobsf-latest"
echo "INNER_DONE=1"
