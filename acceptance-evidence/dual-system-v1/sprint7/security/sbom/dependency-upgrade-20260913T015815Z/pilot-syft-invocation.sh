#!/usr/bin/env bash
# PILOT (read-only w.r.t. the repo): determine a non-sudo syft invocation + validate file-target scanning.
# Writes ONLY into the staging dir passed as $1 (never into the repo).
set -u
STAGE="$1"
PROJ=/home/euripides/projects/blockchain-platform
IMG=docker.1ms.run/anchore/syft:latest
UIDG="$(id -u):$(id -g)"
echo "=== IMAGE INSPECT ==="
docker image inspect "$IMG" --format 'id={{.Id}}
repodigests={{json .RepoDigests}}
created={{.Created}}
size={{.Size}}'
echo
echo "=== SYFT BINARY SHA256 ATTEMPT (no sudo, --user) ==="
docker run --rm --user "$UIDG" --entrypoint sha256sum "$IMG" /syft 2>&1 | head -5
echo "attempt1_exit=$?"
echo "--- entrypoint /bin/sh probe ---"
docker run --rm --user "$UIDG" --entrypoint /bin/sh "$IMG" -c 'echo SH_OK; ls -l /syft; command -v sha256sum || echo NO_SHA256SUM' 2>&1 | head -10
echo
echo "=== SYFT VERSION (no sudo, --user) ==="
docker run --rm --user "$UIDG" -e HOME=/tmp "$IMG" version 2>&1 | tee "$STAGE/pilot-syft-version.txt"
echo "version_exit=$?"
echo
echo "=== FILE TYPE OF TARGETS ==="
file "$PROJ/backend/bin/api" "$PROJ/backend/bin/worker" "$PROJ/backend/bin/migrate" 2>&1
echo
echo "=== PILOT SCAN A: explicit file: scheme, api ==="
docker run --rm --user "$UIDG" -e HOME=/tmp -v "$PROJ":/w -v "$STAGE":/stage -w /w "$IMG" \
  file:/w/backend/bin/api -o cyclonedx-json=/stage/pilot-A-file.cdx.json 2>&1 | tail -5
echo "A_exit=${PIPESTATUS[0]}"
echo
echo "=== PILOT SCAN B: bare relative path (old-flow style), api ==="
docker run --rm --user "$UIDG" -e HOME=/tmp -v "$PROJ":/w -v "$STAGE":/stage -w /w "$IMG" \
  backend/bin/api -o cyclonedx-json=/stage/pilot-B-bare.cdx.json 2>&1 | tail -5
echo "B_exit=${PIPESTATUS[0]}"
echo
echo "=== PILOT OUTPUTS ==="
ls -l "$STAGE"/pilot-*.json 2>&1
sha256sum "$STAGE"/pilot-*.json 2>&1
echo
echo "=== PILOT COMPARE ==="
python3 - "$STAGE" <<'PY'
import json, sys, collections, os
stage = sys.argv[1]
def load(p):
    with open(p) as f: return json.load(f)
for name in ("pilot-A-file.cdx.json", "pilot-B-bare.cdx.json"):
    p = os.path.join(stage, name)
    if not os.path.exists(p):
        print(name, "MISSING"); continue
    d = load(p)
    comps = d.get("components", [])
    mc = d.get("metadata", {}).get("component", {})
    print(name, "components=", len(comps),
          "types=", dict(collections.Counter(c.get("type") for c in comps)))
    print("   meta.component=", json.dumps({k: mc.get(k) for k in ("bom-ref","type","name","version")}))
    print("   meta.source? ", json.dumps(d.get("metadata", {}).get("properties", []))[:300])
    purls = [c.get("purl","") for c in comps]
    for want in ("github.com/go-chi/chi/v5", "github.com/xuri/excelize/v2", "github.com/jackc/pgx/v5", "golang.org/x/crypto"):
        hits = [q for q in purls if q.startswith("pkg:golang/"+want+"@")]
        print("   ", want, "->", hits)
PY
echo "PILOT_DONE"
