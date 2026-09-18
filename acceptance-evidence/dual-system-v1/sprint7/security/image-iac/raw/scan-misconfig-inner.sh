set -x
echo "== trivy version =="
trivy --version
echo
echo "== vuln DB metadata =="
cat /root/.cache/trivy/db/metadata.json 2>&1
echo
echo "== misconfig scanners in use =="
echo "misconfig-scanners = azure-arm,cloudformation,dockerfile,helm,kubernetes,terraform,terraformplan-json,terraformplan-snapshot,ansible"
echo "checks-bundle-repository = docker.m.daocloud.io/aquasec/trivy-checks:2"
echo
echo "== [A] misconfig JSON: repo root /w =="
trivy config /w   --misconfig-scanners "azure-arm,cloudformation,dockerfile,helm,kubernetes,terraform,terraformplan-json,terraformplan-snapshot,ansible"   --checks-bundle-repository "docker.m.daocloud.io/aquasec/trivy-checks:2"   --skip-dirs '**/node_modules' --skip-dirs '**/.git' --skip-dirs '**/dist'   --skip-dirs '**/build' --skip-dirs '**/vendor' --skip-dirs '**/acceptance-evidence'   --skip-dirs '**/.venv' --skip-dirs '**/coverage'   --timeout 30m --cache-dir /root/.cache/trivy   --format json --output /out/misconfig/trivy-config.json
echo "CONFIG_JSON_EXIT=0"
echo
echo "== [B] misconfig TABLE: repo root /w =="
trivy config /w   --misconfig-scanners "azure-arm,cloudformation,dockerfile,helm,kubernetes,terraform,terraformplan-json,terraformplan-snapshot,ansible"   --checks-bundle-repository "docker.m.daocloud.io/aquasec/trivy-checks:2"   --skip-dirs '**/node_modules' --skip-dirs '**/.git' --skip-dirs '**/dist'   --skip-dirs '**/build' --skip-dirs '**/vendor' --skip-dirs '**/acceptance-evidence'   --skip-dirs '**/.venv' --skip-dirs '**/coverage'   --timeout 30m --cache-dir /root/.cache/trivy   --format table --output /out/misconfig/trivy-config.txt
echo "CONFIG_TABLE_EXIT=0"
echo
echo "== [C] misconfig JSON: deploy/ only =="
trivy config /w/deploy   --misconfig-scanners "azure-arm,cloudformation,dockerfile,helm,kubernetes,terraform,terraformplan-json,terraformplan-snapshot,ansible"   --checks-bundle-repository "docker.m.daocloud.io/aquasec/trivy-checks:2"   --timeout 30m --cache-dir /root/.cache/trivy   --format json --output /out/misconfig/trivy-config-deploy.json
echo "CONFIG_DEPLOY_JSON_EXIT=0"
echo
echo "== [D] misconfig TABLE: deploy/ only =="
trivy config /w/deploy   --misconfig-scanners "azure-arm,cloudformation,dockerfile,helm,kubernetes,terraform,terraformplan-json,terraformplan-snapshot,ansible"   --checks-bundle-repository "docker.m.daocloud.io/aquasec/trivy-checks:2"   --timeout 30m --cache-dir /root/.cache/trivy   --format table --output /out/misconfig/trivy-config-deploy.txt
echo "CONFIG_DEPLOY_TABLE_EXIT=0"
echo
echo "== [E] github actions / workflows dir present? =="
ls -la /w/.github/workflows 2>&1 || echo "(no .github/workflows)"
echo "INNER_DONE=1"
