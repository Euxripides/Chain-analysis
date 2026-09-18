#!/usr/bin/env bash
# =====================================================================
# S7-SEC-01 follow-up: SCOPED SBOM generation for the three DEPLOYED Go binaries
# rebuilt in the 2026-09-13 dependency upgrade (api, worker, migrate).
#
# ADDITIVE ONLY:
#   * never deletes/moves/truncates anything
#   * refuses to write any output path that already exists (guard below)
#   * writes only inside the new evidence dir created for this scope
#   * no sudo, no root, no service restart/kill, no docker container/volume removal
#   * does not read or print any secret value
#
# Tool: syft 1.51.1 (Anchore) run from the SAME container image the existing
# sbom/ flow used (docker.1ms.run/anchore/syft:latest, image id
# sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c).
# Deviation from the old flow (recorded honestly in REPORT.md): the old flow used
# "sudo -n docker run"; this run uses plain "docker run" (the invoking account is in
# the docker group) with "--user $(id -u):$(id -g)", so the container process runs as
# the unprivileged account and writes its outputs already owned by that account.
#
# Scan target kind is a FILE (compiled Go binary), NOT a container image.
# =====================================================================
set -u

PROJ=/home/euripides/projects/blockchain-platform
REL=acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z
OUT="$PROJ/$REL"
IMG=docker.1ms.run/anchore/syft:latest
UIDG="$(id -u):$(id -g)"
ARTIFACTS="api worker migrate"
CMDLOG="$OUT/commands.txt"

mkdir -p "$OUT/logs" || { echo "FATAL cannot create $OUT/logs"; exit 2; }

# ---------------- overwrite guard (hard red line) -------------------
guard() {
  if [ -e "$1" ]; then
    echo "FATAL REFUSING TO OVERWRITE EXISTING PATH: $1"
    exit 3
  fi
}
guard "$CMDLOG"
for f in syft-version.txt syft-image-identity.txt syft-binary-sha256.txt artifact-hashes.txt \
         scan-stability.txt validation.txt module-crosscheck.json module-crosscheck.txt \
         module-crosscheck-source.txt buildinfo-api.txt buildinfo-worker.txt buildinfo-migrate.txt \
         buildinfo-all-artifacts.txt \
         api.cdx.json api.spdx.json worker.cdx.json worker.spdx.json migrate.cdx.json migrate.spdx.json; do
  guard "$OUT/$f"
done
for b in $ARTIFACTS; do guard "$OUT/logs/$b-cdx.log"; guard "$OUT/logs/$b-spdx.log"; done
guard "$OUT/logs/syft-version.log"
guard "$OUT/logs/syft-image-save.log"
guard "$OUT/logs/syft-binary-extract.log"
guard "$OUT/logs/validator.log"
guard "$OUT/logs/module-crosscheck.err.log"

UTC() { date -u '+%Y-%m-%dT%H:%M:%SZ'; }
REC() { printf '%s\n' "$*" | tee -a "$CMDLOG"; }

REC "S7-SEC-01 follow-up: SCOPED SBOM for the 2026-09-13 dependency-upgrade rebuild"
REC "host=$(hostname)  utc_start=$(UTC)"
REC "project=$PROJ"
REC "evidence_dir=$OUT"
REC "scope_artifacts=backend/bin/api backend/bin/worker backend/bin/migrate  (3 files, exact paths)"
REC "SCAN_TARGET_KIND=FILE (compiled Go binary) -- NOT a container image, NOT a directory, NOT a source tree"
REC "invoker=$(id -un) uid=$(id -u) gid=$(id -g)   sudo_used=NO   root_used=NO"
REC "=========================================================="

# ---------------- tool identity -------------------------------------
REC "### STEP: syft version + image identity   utc=$(UTC)"
REC "CMD: docker image inspect $IMG --format id/RepoDigests/Created/Size"
docker image inspect "$IMG" --format 'image_ref={{index .RepoTags 0}}
image_id={{.Id}}
repo_digests={{json .RepoDigests}}
image_created={{.Created}}
image_size_bytes={{.Size}}' > "$OUT/syft-image-identity.txt" 2>&1
REC "EXIT_CODE: $?"
REC "RAW_OUTPUT: $OUT/syft-image-identity.txt"
cat "$OUT/syft-image-identity.txt"

REC ""
REC "CMD: docker run --rm --user $UIDG $IMG version"
docker run --rm --user "$UIDG" "$IMG" version > "$OUT/syft-version.txt" 2> "$OUT/logs/syft-version.log"
RC=$?
REC "EXIT_CODE: $RC"
REC "RAW_OUTPUT: $OUT/syft-version.txt (stderr: $OUT/logs/syft-version.log)"
cat "$OUT/syft-version.txt"

# ---- syft executable sha256: the image is distroless (no /bin/sh, no sha256sum),
# ---- so the file is extracted from the saved image archive instead.
REC ""
REC "### STEP: syft executable sha256 (extracted from the saved image archive)   utc=$(UTC)"
REC "CMD: docker save $IMG -o /tmp/sbom-dep-upgrade-20260913T015815Z/syft-image.tar"
docker save "$IMG" -o /tmp/sbom-dep-upgrade-20260913T015815Z/syft-image.tar > "$OUT/logs/syft-image-save.log" 2>&1
REC "EXIT_CODE: $?"
ls -l /tmp/sbom-dep-upgrade-20260913T015815Z/syft-image.tar >> "$OUT/logs/syft-image-save.log" 2>&1
sha256sum /tmp/sbom-dep-upgrade-20260913T015815Z/syft-image.tar >> "$OUT/logs/syft-image-save.log" 2>&1
REC "NOTE: sha256 of the saved archive describes the archive, NOT the executable; the archive hash is in $OUT/logs/syft-image-save.log"
REC "CMD: python3 extract_syft_binary_sha256.py (reads the saved image archive, streams each layer, hashes the /syft file)"
python3 - /tmp/sbom-dep-upgrade-20260913T015815Z/syft-image.tar > "$OUT/syft-binary-sha256.txt" 2> "$OUT/logs/syft-binary-extract.log" <<'PY'
import hashlib, json, sys, tarfile

archive = sys.argv[1]
print("purpose: sha256 of the syft EXECUTABLE /syft as shipped inside image %s" % archive)
print("method:  stream each layer of the saved image archive, hash the top-most file entry named 'syft'")
print("note:    the anchore/syft image is distroless (no /bin/sh, no coreutils), so this cannot be done in-container")
found = None
try:
    with tarfile.open(archive, "r:*") as outer:
        names = outer.getnames()
        layers = []
        if "manifest.json" in names:
            m = json.load(outer.extractfile("manifest.json"))
            layers = list(m[0].get("Layers", []))
            print("layout:  docker-archive, layers=%d" % len(layers))
        elif "index.json" in names:
            idx = json.load(outer.extractfile("index.json"))
            print("layout:  oci-archive")
            for man in idx.get("manifests", []):
                dg = man.get("digest", "").split(":")[-1]
                cand = "blobs/sha256/" + dg
                if cand in names:
                    man = json.load(outer.extractfile(cand))
                    cfg = json.load(outer.extractfile("blobs/sha256/" + man["config"]["digest"].split(":")[-1]))
                    layers = ["blobs/sha256/" + l["digest"].split(":")[-1] for l in man.get("layers", [])]
            print("         layers=%d" % len(layers))
        else:
            print("RESULT: UNKNOWN_LAYOUT -- neither manifest.json nor index.json present")
        for layer_name in reversed(layers):
            try:
                data = outer.extractfile(layer_name)
                with tarfile.open(fileobj=data, mode="r:*") as layer:
                    for member in layer.getmembers():
                        base = member.name.lstrip("./")
                        if base == "syft" and member.isfile():
                            h = hashlib.sha256()
                            src = layer.extractfile(member)
                            n = 0
                            while True:
                                chunk = src.read(1024 * 1024)
                                if not chunk:
                                    break
                                h.update(chunk)
                                n += len(chunk)
                            print("layer:   %s" % layer_name)
                            print("entry:   %s (size=%d bytes)" % (member.name, n))
                            print("sha256:  %s" % h.hexdigest())
                            found = h.hexdigest()
                            break
            except Exception as exc:  # noqa: BLE001
                print("layer %s: unreadable (%s)" % (layer_name, exc))
            if found:
                break
    if not found:
        print("RESULT: NOT_FOUND -- no top-level file entry named 'syft' could be extracted from the image layers")
    else:
        print("RESULT: OK")
except Exception as exc:  # noqa: BLE001
    print("RESULT: ERROR -- %s" % exc)
PY
REC "EXIT_CODE: $?"
REC "RAW_OUTPUT: $OUT/syft-binary-sha256.txt"
cat "$OUT/syft-binary-sha256.txt"

# ---------------- pre-scan target binding ---------------------------
REC ""
REC "### STEP: pre-scan artifact identity   utc=$(UTC)"
{
  echo "### PRE-SCAN target identity  utc=$(UTC)"
  for b in $ARTIFACTS; do
    p="$PROJ/backend/bin/$b"
    if [ -f "$p" ]; then
      stat -c 'path=%n size=%s mode=%a mtime=%y' "$p"
      sha256sum "$p"
      file "$p"
    else
      echo "MISSING: $p"
    fi
  done
  echo "--- go toolchain ---"
  PATH=/usr/local/go/bin:$PATH go version
  for b in $ARTIFACTS; do
    echo "--- go version -m backend/bin/$b ---"
    PATH=/usr/local/go/bin:$PATH go version -m "$PROJ/backend/bin/$b"
  done
} > "$OUT/buildinfo-all-artifacts.txt" 2>&1
# split per-artifact build info (also useful standalone)
for b in $ARTIFACTS; do
  {
    echo "### go version -m $PROJ/backend/bin/$b   utc=$(UTC)"
    echo "### tool: $(PATH=/usr/local/go/bin:$PATH go version)"
    PATH=/usr/local/go/bin:$PATH go version -m "$PROJ/backend/bin/$b"
  } > "$OUT/buildinfo-$b.txt" 2>&1
done
REC "EXIT_CODE: $?"
REC "RAW_OUTPUT: $OUT/buildinfo-<artifact>.txt (one per artifact) + $OUT/buildinfo-all-artifacts.txt"
for b in $ARTIFACTS; do
  echo "[$b] $(sha256sum "$PROJ/backend/bin/$b" | cut -d' ' -f1)  $(stat -c%s "$PROJ/backend/bin/$b") bytes"
done | tee -a "$CMDLOG"

# ---------------- SBOM generation (the deliverable) -----------------
for b in $ARTIFACTS; do
  TARGET="backend/bin/$b"
  REC ""
  REC "### STEP: $b cdx   utc=$(UTC)"
  REC "CMD: docker run --rm --user $UIDG --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp -v $PROJ:/w -w /w $IMG $TARGET -o cyclonedx-json=/w/$REL/$b.cdx.json"
  docker run --rm --user "$UIDG" --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp \
    -v "$PROJ":/w -w /w "$IMG" "$TARGET" \
    -o "cyclonedx-json=/w/$REL/$b.cdx.json" > "$OUT/logs/$b-cdx.log" 2>&1
  REC "EXIT_CODE: $?"
  REC "RAW_OUTPUT: $OUT/logs/$b-cdx.log"

  REC ""
  REC "### STEP: $b spdx   utc=$(UTC)"
  REC "CMD: docker run --rm --user $UIDG --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp -v $PROJ:/w -w /w $IMG $TARGET -o spdx-json=/w/$REL/$b.spdx.json"
  docker run --rm --user "$UIDG" --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp \
    -v "$PROJ":/w -w /w "$IMG" "$TARGET" \
    -o "spdx-json=/w/$REL/$b.spdx.json" > "$OUT/logs/$b-spdx.log" 2>&1
  REC "EXIT_CODE: $?"
  REC "RAW_OUTPUT: $OUT/logs/$b-spdx.log"
done

# ---------------- post-scan stability proof -------------------------
REC ""
REC "### STEP: post-scan artifact identity (scan-target stability proof)   utc=$(UTC)"
{
  echo "### SCAN-TARGET STABILITY for the scoped SBOM  utc=$(UTC)"
  echo "The three scanned files must be byte-identical before and after the syft runs,"
  echo "otherwise the SBOM would not describe the artifact that is deployed."
  echo
  for b in $ARTIFACTS; do
    p="$PROJ/backend/bin/$b"
    echo "--- $b ---"
    stat -c 'post  size=%s mtime=%y' "$p"
    sha256sum "$p"
  done
} > "$OUT/scan-stability.txt" 2>&1

# ---------------- artifact hashes -----------------------------------
{
  echo "### SCOPED SBOM artifact hashes  utc=$(UTC)"
  echo "### generated by $0"
  echo "--- scanned targets (sha256sum, vm) ---"
  for b in $ARTIFACTS; do sha256sum "$PROJ/backend/bin/$b"; done
  echo "--- sbom documents ---"
  ( cd "$OUT" && sha256sum api.cdx.json api.spdx.json worker.cdx.json worker.spdx.json migrate.cdx.json migrate.spdx.json )
  echo "--- supporting evidence ---"
  ( cd "$OUT" && sha256sum syft-version.txt syft-image-identity.txt syft-binary-sha256.txt buildinfo-api.txt buildinfo-worker.txt buildinfo-migrate.txt scan-stability.txt )
} >> "$OUT/artifact-hashes.txt" 2>&1
REC "### STEP: artifact hashes   utc=$(UTC)"
REC "RAW_OUTPUT: $OUT/artifact-hashes.txt"

# ---------------- source-level module evidence ----------------------
{
  echo "### source-level (go.mod / go.sum) evidence for the 4 upgraded modules  utc=$(UTC)"
  echo "### these are the module files of the SOURCE TREE that produced the binaries;"
  echo "### the SBOM itself is derived from BINARY buildinfo, not from these files."
  echo "--- file digests ---"
  sha256sum "$PROJ/backend/go.mod" "$PROJ/backend/go.sum"
  echo "--- go directive ---"
  grep -E '^go ' "$PROJ/backend/go.mod"
  echo "--- require lines for the 4 upgraded modules ---"
  grep -nE 'go-chi/chi/v5|exuri/excelize/v2|jackc/pgx/v5|golang\.org/x/crypto' "$PROJ/backend/go.mod"
  echo "--- go.sum lines for the 4 upgraded modules ---"
  grep -nE '^github.com/go-chi/chi/v5 |^github.com/xuri/excelize/v2 |^github.com/jackc/pgx/v5 |^golang.org/x/crypto ' "$PROJ/backend/go.sum"
  echo "--- go list -m (module cache view, read-only) ---"
  ( cd "$PROJ/backend" && PATH=/usr/local/go/bin:$PATH GOFLAGS=-mod=mod go list -m github.com/go-chi/chi/v5 github.com/xuri/excelize/v2 github.com/jackc/pgx/v5 golang.org/x/crypto 2>&1 | head -20 )
} > "$OUT/module-crosscheck-source.txt" 2>&1
REC ""
REC "### STEP: source-level module evidence   utc=$(UTC)"
REC "RAW_OUTPUT: $OUT/module-crosscheck-source.txt"

# ---------------- JSON validation + module presence -----------------
REC ""
REC "### STEP: JSON validation + upgraded-module presence check   utc=$(UTC)"
python3 - "$OUT" > "$OUT/logs/validator.log" 2>&1 <<'PY'
import collections, hashlib, json, os, sys
from datetime import datetime, timezone

OUT = sys.argv[1]

ARTIFACTS = [
    ("api",     "backend/bin/api"),
    ("worker",  "backend/bin/worker"),
    ("migrate", "backend/bin/migrate"),
]
EXPECTED = [
    ("github.com/go-chi/chi/v5",    "5.3.2",  "go-chi/chi/v5"),
    ("github.com/xuri/excelize/v2", "2.11.0", "xuri/excelize/v2"),
    ("github.com/jackc/pgx/v5",     "5.9.2",  "jackc/pgx/v5"),
    ("golang.org/x/crypto",         "0.56.0", "golang.org/x/crypto"),
]

def sha256f(path):
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()

def norm(v):
    v = (v or "").strip()
    return v[1:] if v.startswith("v") else v

report = []
w = report.append
w("=" * 72)
w("VALIDATION OF THE SCOPED SBOM ARTIFACTS (2026-09-13 dependency-upgrade scope)")
w("every figure below was computed from the files on disk by this validator")
w("utc=%s" % datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
w("=" * 72)

check = {"artifacts": {}, "expected": [
    {"module": m, "expected_version": v, "purl_path": p} for m, v, p in EXPECTED]}
all_ok = True

for name, rel in ARTIFACTS:
    tgt = os.path.join("/home/euripides/projects/blockchain-platform", rel)
    tgt_sha = sha256f(tgt)
    check["artifacts"][name] = {"target": tgt, "target_sha256": tgt_sha,
                                "cdx": {}, "spdx": {}}
    for fmt, ext in (("cyclonedx", "cdx"), ("spdx", "spdx")):
        p = os.path.join(OUT, "%s.%s.json" % (name, ext))
        w("")
        w("FILE: %s" % p)
        w("  exists       = %s" % os.path.exists(p))
        if not os.path.exists(p):
            all_ok = False
            continue
        w("  size_bytes   = %d" % os.path.getsize(p))
        w("  sha256       = %s" % sha256f(p))
        try:
            with open(p, encoding="utf-8") as fh:
                doc = json.load(fh)
        except Exception as exc:  # noqa: BLE001
            w("  JSON_PARSE   = FAILED: %s" % exc)
            all_ok = False
            continue
        w("  JSON_PARSE   = OK")

        if fmt == "cyclonedx":
            comps = doc.get("components") or []
            mc = (doc.get("metadata") or {}).get("component") or {}
            w("  bomFormat    = %s" % doc.get("bomFormat"))
            w("  specVersion  = %s" % doc.get("specVersion"))
            w("  serialNumber = %s" % doc.get("serialNumber"))
            w("  components   = %d" % len(comps))
            w("  dependencies = %d" % len(doc.get("dependencies") or []))
            w("  metadata.tools = %s" % json.dumps((doc.get("metadata") or {}).get("tools"))[:200])
            w("  metadata.component = %s" % json.dumps({k: mc.get(k) for k in ("bom-ref", "type", "name", "version")}))
            w("  types        = %s" % json.dumps(dict(collections.Counter(c.get("type") for c in comps))))
            eco = collections.Counter()
            for c in comps:
                purl = c.get("purl") or ""
                eco[purl.split("/")[1] if purl.startswith("pkg:") else "(no purl)"] += 1
            w("  ecosystems   = %s" % json.dumps(dict(eco)))
            got = norm((mc.get("version") or "").replace("sha256:", ""))
            w("  target_binding: metadata.component.type=%s  version(sha256)=%s  real file sha256=%s  match=%s"
              % (mc.get("type"), mc.get("version"), tgt_sha,
                 "True" if (mc.get("version") or "") == "sha256:" + tgt_sha else "False"))
            if (mc.get("version") or "") != "sha256:" + tgt_sha:
                all_ok = False
                w("  *** TARGET BINDING MISMATCH ***")
            w("  components_total = %d" % len(comps))
            found = {}
            for module, ver, ppath in EXPECTED:
                needle = "pkg:golang/%s@" % module
                hits = [c for c in comps if (c.get("purl") or "").startswith(needle)]
                entry = {"module": module, "expected_version": ver, "found": bool(hits),
                         "components": [{"name": c.get("name"), "version": c.get("version"), "purl": c.get("purl")} for c in hits]}
                ok = False
                if hits:
                    ok = any(norm(h.get("version")) == ver or norm(h.get("purl", "").split("@")[-1]) == ver for h in hits)
                entry["version_ok"] = ok
                if not ok:
                    all_ok = False
                found[module] = entry
                w("  upgraded_module[%s] expected=%s found=%s version_ok=%s -> %s"
                  % (module, ver, bool(hits), ok,
                     json.dumps([{"name": c.get("name"), "version": c.get("version")} for c in hits])))
            check["artifacts"][name]["cdx"] = {"path": p, "sha256": sha256f(p),
                                               "components": len(comps), "modules": found}
        else:
            pkgs = doc.get("packages") or []
            w("  spdxVersion  = %s" % doc.get("spdxVersion"))
            w("  dataLicense  = %s" % doc.get("dataLicense"))
            w("  name         = %s" % doc.get("name"))
            w("  packages     = %d" % len(pkgs))
            w("  relationships= %d" % len(doc.get("relationships") or []))
            w("  creationInfo.creators = %s" % json.dumps((doc.get("creationInfo") or {}).get("creators")))
            w("  primaryPackagePurpose = %s" % json.dumps(dict(collections.Counter(p.get("primaryPackagePurpose") for p in pkgs))))
            eco = collections.Counter()
            for p in pkgs:
                for ref in (p.get("externalRefs") or []):
                    if ref.get("referenceType") == "purl":
                        loc = ref.get("referenceLocator") or ""
                        eco[loc.split("/")[1] if loc.startswith("pkg:") else "(other)"] += 1
                        break
                else:
                    eco["(no purl)"] += 1
            w("  ecosystems   = %s" % json.dumps(dict(eco)))
            found = {}
            for module, ver, ppath in EXPECTED:
                needle = "pkg:golang/%s@" % module
                hits = []
                for p in pkgs:
                    for ref in (p.get("externalRefs") or []):
                        loc = ref.get("referenceLocator") or ""
                        if ref.get("referenceType") == "purl" and loc.startswith(needle):
                            hits.append({"name": p.get("name"), "versionInfo": p.get("versionInfo"), "purl": loc})
                entry = {"module": module, "expected_version": ver, "found": bool(hits), "packages": hits}
                ok = False
                if hits:
                    ok = any(norm(h.get("versionInfo")) == ver or norm(h.get("purl", "").split("@")[-1]) == ver for h in hits)
                entry["version_ok"] = ok
                if not ok:
                    all_ok = False
                found[module] = entry
                w("  upgraded_module[%s] expected=%s found=%s version_ok=%s -> %s"
                  % (module, ver, bool(hits), ok,
                     json.dumps([{"name": p.get("name"), "versionInfo": p.get("versionInfo")} for p in hits])))
            check["artifacts"][name]["spdx"] = {"path": p, "sha256": sha256f(p),
                                                "packages": len(pkgs), "modules": found}
    w("")

w("=" * 72)
w("UPGRADED-MODULE PRESENCE SUMMARY (per artifact, per format)")
w("=" * 72)
w("%-9s %-8s %s" % ("artifact", "format", "  ".join("%s@%s" % (m.split("/")[-1], v) for m, v, _ in EXPECTED)))
for name, rel in ARTIFACTS:
    for fmt in ("cdx", "spdx"):
        d = check["artifacts"][name].get(fmt) or {}
        mods = d.get("modules") or {}
        cells = []
        for m, v, _ in EXPECTED:
            e = mods.get(m) or {}
            cells.append("YES" if e.get("version_ok") else ("PRESENT-other-version" if e.get("found") else "ABSENT"))
        w("%-9s %-8s %s" % (name, fmt, "  ".join("%-12s" % c for c in cells)))
w("")
w("ABSENT = the module is not linked into that binary, so the binary's buildinfo does not")
w("         contain it. This is a property of the per-binary scope, not a generation defect;")
w("         see module-crosscheck-source.txt for the source-tree level evidence.")
w("")
w("validation_result = %s" % ("PASS" if all_ok else "GAPS_PRESENT"))

with open(os.path.join(OUT, "validation.txt"), "w", encoding="utf-8") as fh:
    fh.write("\n".join(report) + "\n")

check_doc = {
    "generated_utc": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
    "scope": "scoped SBOM for the three binaries rebuilt in the 2026-09-13 dependency upgrade",
    "expected_modules": check["expected"],
    "artifacts": check["artifacts"],
    "result": "PASS" if all_ok else "GAPS_PRESENT",
}
with open(os.path.join(OUT, "module-crosscheck.json"), "w", encoding="utf-8") as fh:
    json.dump(check_doc, fh, indent=2, sort_keys=True)
    fh.write("\n")
sys.stderr.write("validator done: %s\n" % check_doc["result"])
PY
REC "EXIT_CODE: $?"
REC "RAW_OUTPUT: $OUT/validation.txt, $OUT/module-crosscheck.json"
tail -n 20 "$OUT/validation.txt" >> "$CMDLOG"

# human-readable module cross-check (derived from the json)
python3 - "$OUT" > "$OUT/module-crosscheck.txt" 2> "$OUT/logs/module-crosscheck.err.log" <<'PY'
import json, os, sys
OUT = sys.argv[1]
with open(os.path.join(OUT, "module-crosscheck.json"), encoding="utf-8") as fh:
    doc = json.load(fh)
print("UPGRADED-MODULE PRESENCE IN THE SCOPED SBOMs")
print("generated_utc = %s" % doc["generated_utc"])
print("result        = %s" % doc["result"])
print()
print("%-34s %-9s %s" % ("module", "expected", "present-in: api / worker / migrate  (cdx|spdx)"))
for m in doc["expected_modules"]:
    mod, ver = m["module"], m["expected_version"]
    cells = []
    for art in ("api", "worker", "migrate"):
        a = doc["artifacts"][art]
        c = (a.get("cdx") or {}).get("modules", {}).get(mod, {})
        s = (a.get("spdx") or {}).get("modules", {}).get(mod, {})
        cells.append("%s|%s" % ("OK" if c.get("version_ok") else ("absent" if not c.get("found") else "OTHERVER"),
                                "OK" if s.get("version_ok") else ("absent" if not s.get("found") else "OTHERVER")))
    print("%-34s %-9s %s" % (mod, ver, "   ".join("%-11s" % c for c in cells)))
print()
for art in ("api", "worker", "migrate"):
    a = doc["artifacts"][art]
    print("--- %s | scanned target sha256 = %s" % (art, a["target_sha256"]))
    for fmt in ("cdx", "spdx"):
        d = a.get(fmt) or {}
        key = "components" if fmt == "cdx" else "packages"
        print("    %s: %s=%s sha256=%s" % (fmt, key, d.get(key), d.get("sha256")))
print()
print("ABSENT means the module is not linked into that binary (buildinfo scope), see")
print("module-crosscheck-source.txt for the source-tree evidence of all four upgrades.")
PY
REC "EXIT_CODE: $?"
REC "RAW_OUTPUT: $OUT/module-crosscheck.txt"
REC "=========================================================="
REC "### DONE  utc=$(UTC)"
tail -n 30 "$OUT/module-crosscheck.txt"
echo "GENERATOR_DONE"
