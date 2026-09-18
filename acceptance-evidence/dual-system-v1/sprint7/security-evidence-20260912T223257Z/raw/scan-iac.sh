#!/bin/bash
EV="/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security-evidence-20260912T223257Z"; R="/home/euripides/projects/blockchain-platform"; I="$EV/iac"; mkdir -p "$I"
exec > "$I/iac-run.log" 2>&1
set -x

echo "=== PROVENANCE ==="
date -u +%Y-%m-%dT%H:%M:%SZ
echo "--- scanner images (local, NOT pulled) ---"
for img in bridgecrew/checkov:latest checkmarx/kics:latest aquasec/trivy:latest; do
  docker image inspect "$img" --format '{{.RepoTags}}|{{json .RepoDigests}}|{{.Id}}|{{.Created}}'
done
echo "--- checkov version ---"
docker run --rm bridgecrew/checkov:latest --version
echo "--- kics version ---"
docker run --rm checkmarx/kics:latest version
echo "--- trivy supported misconfig scanners ---"
docker run --rm aquasec/trivy:latest config --help 2>&1 | grep -A3 'misconfig-scanners' | head -12

echo
echo "=== TARGET FILE INVENTORY (repo deploy/) ==="
find "$R/deploy" -type f | sort
echo "--- sha256 of each target ---"
find "$R/deploy" -type f -print0 | sort -z | xargs -0 sha256sum

echo
echo "=== [1] CHECKOV on deploy/ (json) ==="
docker run --rm -v "$R:/w:ro" bridgecrew/checkov:latest -d /w/deploy -o json --compact > "$I/checkov-deploy.json" 2> "$I/checkov-deploy.stderr.txt"
echo "CHECKOV_DEPLOY_JSON_EXIT=$?"
head -c 400 "$I/checkov-deploy.json"; echo
echo "--- checkov stderr ---"; cat "$I/checkov-deploy.stderr.txt"

echo
echo "=== [2] CHECKOV on deploy/ (cli table) ==="
docker run --rm -v "$R:/w:ro" bridgecrew/checkov:latest -d /w/deploy --compact > "$I/checkov-deploy.txt" 2>&1
echo "CHECKOV_DEPLOY_TABLE_EXIT=$?"

echo
echo "=== [3] CHECKOV docker_compose framework explicit ==="
docker run --rm -v "$R:/w:ro" bridgecrew/checkov:latest -d /w/deploy --framework docker_compose -o json --compact > "$I/checkov-compose.json" 2>"$I/checkov-compose.stderr.txt"
echo "CHECKOV_COMPOSE_EXIT=$?"
head -c 400 "$I/checkov-compose.json"; echo

echo
echo "=== [4] CHECKOV secrets framework on repo (source) ==="
docker run --rm -v "$R:/w:ro" bridgecrew/checkov:latest -d /w --framework secrets --compact -o json > "$I/checkov-secrets.json" 2>"$I/checkov-secrets.stderr.txt"
echo "CHECKOV_SECRETS_EXIT=$?"
head -c 400 "$I/checkov-secrets.json"; echo

echo
echo "=== [5] KICS on deploy/ ==="
docker run --rm -v "$R/deploy:/path:ro" -v "$I:/out" checkmarx/kics:latest scan -p /path -o /out --report-formats json,html --no-progress > "$I/kics-deploy.log" 2>&1
echo "KICS_DEPLOY_EXIT=$?"
tail -25 "$I/kics-deploy.log"

echo
echo "=== [6] TRIVY CONFIG on deploy/ (daocloud checks-bundle mirror) ==="
docker run --rm -v "$R:/w:ro" -v "$I:/out" aquasec/trivy:latest config /w/deploy \
  --checks-bundle-repository docker.m.daocloud.io/aquasec/trivy-checks:2 \
  --timeout 10m --format json --output /out/trivy-config-deploy.json 2> "$I/trivy-config-deploy.stderr.txt"
echo "TRIVY_CONFIG_JSON_EXIT=$?"
tail -8 "$I/trivy-config-deploy.stderr.txt"
docker run --rm -v "$R:/w:ro" -v "$I:/out" aquasec/trivy:latest config /w/deploy \
  --checks-bundle-repository docker.m.daocloud.io/aquasec/trivy-checks:2 \
  --timeout 10m > "$I/trivy-config-deploy.txt" 2>&1
echo "TRIVY_CONFIG_TABLE_EXIT=$?"
tail -30 "$I/trivy-config-deploy.txt"

echo
echo "=== DONE ==="
ls -la "$I"
echo "IAC_DONE=1"
