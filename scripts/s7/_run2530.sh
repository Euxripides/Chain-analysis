cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login
CK=$(awk '/^#HttpOnly_/ {sub(/^#HttpOnly_/,""); printf "%s=%s; ", $6,$7; next} /^#|^$/ {next} {printf "%s=%s; ", $6,$7}' "$J")
for u in \
  "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/activity?limit=200" \
  "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/assets" \
  "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/flow-profile" \
  "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/overview" \
  "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/counterparties" \
  "/api/v2/explorer/addresses/tron/TU585SoBMd3hp9WaLc7yctSbR9fSmXsm32/activity?limit=200" ; do
  printf '%-96s -> ' "$u"
  curl -s -o /tmp/o.json -w '%{http_code} %{time_total}s %{size_download}B' -b "$J" "http://127.0.0.1:8080$u"
  echo " $(head -c 120 /tmp/o.json | tr -d '\n')"
done
echo SCRIPT_END
