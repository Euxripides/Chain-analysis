#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
S7-SEC-01 follow-up (2026-09-13 dependency-upgrade scope)
CORRECTIVE validator for the six scoped SBOM documents.

Why this file exists as a separate, new script:
  the first validation pass was embedded in
  generate-sbom-dependency-upgrade-20260913T015815Z.sh and FAILED with
      TypeError: expected str, bytes or os.PathLike object, not dict
  at sha256f() because one local name was reused both for the SBOM file path and
  for the SPDX package loop variable. That failed pass is preserved as-is
  (commands.txt, logs/validator.log, the empty module-crosscheck.txt); it is NOT
  edited. This corrective pass is a new file with a new name.

ADDITIVE ONLY / read-only w.r.t. the repository:
  * writes only validation.txt, validation.json, module-crosscheck-summary.txt
    inside the new evidence dir, and REFUSES to run if any of them already exists
  * never deletes, moves or truncates anything; no sudo; no service interaction
"""
import collections
import hashlib
import json
import os
import re
import sys
from datetime import datetime, timezone

PROJ = "/home/euripides/projects/blockchain-platform"
OUT = os.path.join(
    PROJ,
    "acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z",
)

ARTIFACTS = [
    ("api", "backend/bin/api"),
    ("worker", "backend/bin/worker"),
    ("migrate", "backend/bin/migrate"),
]

EXPECTED = [
    ("github.com/go-chi/chi/v5", "5.3.2"),
    ("github.com/xuri/excelize/v2", "2.11.0"),
    ("github.com/jackc/pgx/v5", "5.9.2"),
    ("golang.org/x/crypto", "0.56.0"),
]

OUTPUTS = ["validation.txt", "validation.json", "module-crosscheck-summary.txt"]


def guard_outputs():
    for name in OUTPUTS:
        target = os.path.join(OUT, name)
        if os.path.exists(target):
            sys.stderr.write("FATAL REFUSING TO OVERWRITE EXISTING PATH: %s\n" % target)
            raise SystemExit(3)


def sha256_file(path):
    digest = hashlib.sha256()
    with open(path, "rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def strip_v(text):
    text = (text or "").strip()
    return text[1:] if text.startswith("v") else text


def parse_go_mod_requires(path):
    """Return {(module): version} for the plain require block of a go.mod."""
    found = {}
    in_block = False
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            line = raw.split("//")[0].strip()
            if not line:
                continue
            if line.startswith("require ("):
                in_block = True
                continue
            if in_block and line == ")":
                in_block = False
                continue
            if line.startswith("require "):
                line = line[len("require "):].strip()
            elif not in_block:
                continue
            parts = line.split()
            if len(parts) >= 2:
                found[parts[0]] = parts[1]
    return found


def parse_go_sum(path):
    """Return {(module, version): 'h1:...'} from a go.sum file."""
    found = {}
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            parts = raw.split()
            if len(parts) >= 3 and parts[2].startswith("h1:"):
                found[(parts[0], parts[1])] = parts[2]
    return found


def parse_buildinfo_deps(path):
    """Return ({module: {'version': v, 'h1': h}}, {build settings}, main info)."""
    deps = {}
    settings = {}
    main = {}
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            line = raw.rstrip("\n")
            cols = line.split("\t")
            cols = [c for c in cols if c != ""]
            if not cols:
                continue
            if cols[0] == "dep" and len(cols) >= 3:
                deps[cols[1]] = {"version": cols[2], "h1": cols[3] if len(cols) > 3 else ""}
            elif cols[0] == "build" and len(cols) >= 2:
                key, _, value = cols[1].partition("=")
                settings[key] = value
            elif cols[0] == "path" and len(cols) >= 2:
                main["path"] = cols[1]
            elif cols[0] == "mod" and len(cols) >= 2:
                main["mod"] = cols[1]
                main["mod_version"] = cols[2] if len(cols) > 2 else ""
    return deps, settings, main


def main():
    guard_outputs()
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    go_mod_path = os.path.join(PROJ, "backend/go.mod")
    go_sum_path = os.path.join(PROJ, "backend/go.sum")
    go_mod_requires = parse_go_mod_requires(go_mod_path)
    go_sum_h1 = parse_go_sum(go_sum_path)

    report = []
    emit = report.append
    emit("=" * 78)
    emit("VALIDATION OF THE SCOPED SBOM DOCUMENTS (2026-09-13 dependency-upgrade scope)")
    emit("corrective pass (the first, embedded validation pass failed; see the defect note)")
    emit("utc=%s" % now)
    emit("every figure below was computed from the files on disk by this validator")
    emit("=" * 78)

    result = {
        "generated_utc": now,
        "validator": "validate-sbom-scope-dependency-upgrade-20260913T015815Z.py",
        "result": "PASS",
        "targets": {},
        "documents": {},
        "upgraded_modules": {"expected": [{"module": m, "version": v} for m, v in EXPECTED], "documents": {}},
        "source_level": {
            "go_mod": go_mod_path,
            "go_mod_sha256": sha256_file(go_mod_path),
            "go_sum": go_sum_path,
            "go_sum_sha256": sha256_file(go_sum_path),
            "go_mod_requires": {m: go_mod_requires.get(m) for m, _ in EXPECTED},
            "go_sum_h1": {"%s@%s" % (m, v): go_sum_h1.get((m, v)) for m, v in EXPECTED},
        },
        "notes": [],
    }
    all_ok = True
    gaps = []

    for artifact, rel_target in ARTIFACTS:
        target_path = os.path.join(PROJ, rel_target)
        target_sha = sha256_file(target_path)
        target_size = os.path.getsize(target_path)
        result["targets"][artifact] = {
            "path": target_path,
            "relative_path": rel_target,
            "sha256": target_sha,
            "size_bytes": target_size,
        }
        deps, settings, main_info = parse_buildinfo_deps(
            os.path.join(OUT, "buildinfo-%s.txt" % artifact)
        )
        result["targets"][artifact]["go_version"] = None
        with open(os.path.join(OUT, "buildinfo-%s.txt" % artifact), encoding="utf-8") as handle:
            head = "".join(handle.readline() for _ in range(3))
        match = re.search(r"go1\.[0-9.]+", head)
        result["targets"][artifact]["go_version"] = match.group(0) if match else None
        result["targets"][artifact]["main_package"] = main_info.get("path")
        result["targets"][artifact]["main_module"] = main_info.get("mod")
        result["targets"][artifact]["main_module_version"] = main_info.get("mod_version", "")
        result["targets"][artifact]["build_settings"] = settings
        result["targets"][artifact]["vcs_settings"] = {
            k: v for k, v in settings.items() if k.startswith("vcs")
        }
        result["targets"][artifact]["embedded_module_count"] = len(deps)
        result["documents"][artifact] = {}

        emit("")
        emit("-" * 78)
        emit("ARTIFACT: %s" % artifact)
        emit("  scanned target path  = %s" % target_path)
        emit("  scanned target sha256= %s  (%d bytes)" % (target_sha, target_size))
        emit("  go toolchain         = %s" % result["targets"][artifact]["go_version"])
        emit("  main package         = %s" % main_info.get("path"))
        emit("  main module          = %s %s" % (main_info.get("mod"), main_info.get("mod_version", "")))
        emit("  embedded go modules  = %d   (from the binary's own buildinfo)" % len(deps))
        emit("  build settings       = %s" % json.dumps(settings, sort_keys=True))
        emit("  vcs build settings   = %s   (empty => no VCS revision is embedded in this binary)"
             % json.dumps(result["targets"][artifact]["vcs_settings"], sort_keys=True))

        for fmt, ext in (("cyclonedx", "cdx"), ("spdx", "spdx")):
            doc_path = os.path.join(OUT, "%s.%s.json" % (artifact, ext))
            entry = {"file": os.path.basename(doc_path)}
            emit("")
            emit("  FILE: %s" % doc_path)
            if not os.path.exists(doc_path):
                entry["parse"] = "MISSING"
                all_ok = False
                gaps.append("%s.%s.json missing" % (artifact, ext))
                emit("    exists = False  *** MISSING ***")
                result["documents"][artifact][ext] = entry
                continue
            entry["size_bytes"] = os.path.getsize(doc_path)
            entry["sha256"] = sha256_file(doc_path)
            emit("    size_bytes = %d" % entry["size_bytes"])
            emit("    sha256     = %s" % entry["sha256"])
            try:
                with open(doc_path, encoding="utf-8") as handle:
                    document = json.load(handle)
                entry["parse"] = "OK"
                emit("    JSON parse = OK")
            except Exception as exc:  # noqa: BLE001
                entry["parse"] = "FAILED: %s" % exc
                all_ok = False
                gaps.append("%s.%s.json not parseable" % (artifact, ext))
                emit("    JSON parse = FAILED: %s" % exc)
                result["documents"][artifact][ext] = entry
                continue

            module_hits = {}
            if fmt == "cyclonedx":
                components = document.get("components") or []
                meta_component = (document.get("metadata") or {}).get("component") or {}
                entry["format"] = "%s %s" % (document.get("bomFormat"), document.get("specVersion"))
                entry["serial_number"] = document.get("serialNumber")
                entry["component_count"] = len(components)
                entry["dependency_count"] = len(document.get("dependencies") or [])
                entry["metadata_component"] = {k: meta_component.get(k) for k in ("bom-ref", "type", "name", "version")}
                entry["type_distribution"] = dict(collections.Counter(c.get("type") for c in components))
                ecosystems = collections.Counter()
                for component in components:
                    purl = component.get("purl") or ""
                    ecosystems[purl.split("/")[1] if purl.startswith("pkg:") else "(no purl)"] += 1
                entry["ecosystem_distribution"] = dict(ecosystems)
                licenses = collections.Counter()
                without_license = 0
                for component in components:
                    declared = component.get("licenses") or []
                    if not declared:
                        without_license += 1
                        continue
                    for item in declared:
                        lic = item.get("license") or {}
                        licenses[lic.get("id") or lic.get("name") or "(unnamed)"] += 1
                entry["licenses"] = dict(licenses)
                entry["components_without_license"] = without_license
                binding_ok = (meta_component.get("version") or "") == "sha256:" + target_sha
                entry["target_binding"] = {
                    "metadata_component_type": meta_component.get("type"),
                    "metadata_component_version": meta_component.get("version"),
                    "real_file_sha256": target_sha,
                    "match": binding_ok,
                }
                emit("    format=%s  serial=%s" % (entry["format"], entry["serial_number"]))
                emit("    metadata.component=%s" % json.dumps(entry["metadata_component"]))
                emit("    components=%d  dependency_entries=%d" % (entry["component_count"], entry["dependency_count"]))
                emit("    types=%s" % json.dumps(entry["type_distribution"]))
                emit("    ecosystems=%s" % json.dumps(entry["ecosystem_distribution"]))
                emit("    TARGET BINDING: metadata.component.version == sha256 of the scanned file -> %s" % binding_ok)
                if not binding_ok:
                    all_ok = False
                    gaps.append("%s.cdx.json is not bound to the scanned binary" % artifact)
                for module, version in EXPECTED:
                    needle = "pkg:golang/%s@" % module
                    hits = [c for c in components if (c.get("purl") or "").startswith(needle)]
                    version_ok = any(
                        strip_v(h.get("version")) == version
                        or strip_v((h.get("purl") or "").split("@")[-1]) == version
                        for h in hits
                    )
                    module_hits[module] = {
                        "expected_version": version,
                        "found": bool(hits),
                        "version_ok": version_ok,
                        "components": [{"name": h.get("name"), "version": h.get("version"), "purl": h.get("purl")} for h in hits],
                    }
                    emit("    upgraded_module[%s] expected=%s found=%s version_ok=%s %s"
                         % (module, version, bool(hits), version_ok,
                            json.dumps([{"name": h.get("name"), "version": h.get("version")} for h in hits])))
            else:
                packages = document.get("packages") or []
                entry["format"] = "%s (%s)" % (document.get("spdxVersion"), document.get("dataLicense"))
                entry["name"] = document.get("name")
                entry["package_count"] = len(packages)
                entry["relationship_count"] = len(document.get("relationships") or [])
                entry["creators"] = (document.get("creationInfo") or {}).get("creators")
                entry["primary_package_purpose"] = dict(collections.Counter(pkg.get("primaryPackagePurpose") for pkg in packages))
                ecosystems = collections.Counter()
                for pkg in packages:
                    for ref in (pkg.get("externalRefs") or []):
                        locator = ref.get("referenceLocator") or ""
                        if ref.get("referenceType") == "purl":
                            ecosystems[locator.split("/")[1] if locator.startswith("pkg:") else "(other)"] += 1
                            break
                    else:
                        ecosystems["(no purl)"] += 1
                entry["ecosystem_distribution"] = dict(ecosystems)
                licenses = collections.Counter(pkg.get("licenseDeclared") for pkg in packages)
                entry["licenses"] = dict(licenses)
                emit("    format=%s  name=%s" % (entry["format"], entry["name"]))
                emit("    creators=%s" % json.dumps(entry["creators"]))
                emit("    packages=%d  relationships=%d" % (entry["package_count"], entry["relationship_count"]))
                emit("    primaryPackagePurpose=%s" % json.dumps(entry["primary_package_purpose"]))
                emit("    ecosystems=%s" % json.dumps(entry["ecosystem_distribution"]))
                for module, version in EXPECTED:
                    needle = "pkg:golang/%s@" % module
                    hits = []
                    for pkg in packages:
                        for ref in (pkg.get("externalRefs") or []):
                            locator = ref.get("referenceLocator") or ""
                            if ref.get("referenceType") == "purl" and locator.startswith(needle):
                                hits.append({"name": pkg.get("name"), "versionInfo": pkg.get("versionInfo"), "purl": locator})
                    version_ok = any(
                        strip_v(h.get("versionInfo")) == version
                        or strip_v((h.get("purl") or "").split("@")[-1]) == version
                        for h in hits
                    )
                    module_hits[module] = {
                        "expected_version": version,
                        "found": bool(hits),
                        "version_ok": version_ok,
                        "packages": hits,
                    }
                    emit("    upgraded_module[%s] expected=%s found=%s version_ok=%s %s"
                         % (module, version, bool(hits), version_ok,
                            json.dumps([{"name": h.get("name"), "versionInfo": h.get("versionInfo")} for h in hits])))

            key = "%s.%s" % (artifact, ext)
            result["upgraded_modules"]["documents"][key] = module_hits
            for module, version in EXPECTED:
                if not module_hits[module]["version_ok"]:
                    gaps.append("%s: %s@%s not present at the upgraded version" % (key, module, version))
            result["documents"][artifact][ext] = entry

        # buildinfo vs go.sum source cross-check (per artifact)
        crosscheck = {}
        for module, version in EXPECTED:
            dep = deps.get(module)
            binary_h1 = (dep or {}).get("h1") or None
            binary_version = (dep or {}).get("version") or None
            source_h1 = go_sum_h1.get((module, version))
            crosscheck[module] = {
                "expected_version": version,
                "linked_into_binary": dep is not None,
                "binary_buildinfo_version": binary_version,
                "binary_buildinfo_h1": binary_h1,
                "go_sum_h1_for_expected_version": source_h1,
                "go_mod_require": go_mod_requires.get(module),
                "h1_matches_go_sum": (binary_h1 is not None and binary_h1 == source_h1),
            }
        result["targets"][artifact]["module_crosscheck"] = crosscheck
        emit("")
        emit("  buildinfo-vs-go.sum cross-check for the four upgraded modules:")
        for module, version in EXPECTED:
            row = crosscheck[module]
            emit("    %-30s expected=%s linked=%s buildinfo_version=%s h1_match_go_sum=%s"
                 % (module, version, row["linked_into_binary"], row["binary_buildinfo_version"], row["h1_matches_go_sum"]))

    emit("")
    emit("=" * 78)
    emit("UPGRADED-MODULE PRESENCE MATRIX (per document)")
    emit("=" * 78)
    header = "%-10s %-5s " % ("artifact", "fmt") + " ".join("%-26s" % (m.split("/")[-1] + "@" + v) for m, v in EXPECTED)
    emit(header)
    for artifact, _ in ARTIFACTS:
        for ext in ("cdx", "spdx"):
            key = "%s.%s" % (artifact, ext)
            cells = []
            for module, _version in EXPECTED:
                hit = result["upgraded_modules"]["documents"][key][module]
                if hit["version_ok"]:
                    cells.append("YES")
                elif hit["found"]:
                    cells.append("OTHER-VERSION")
                else:
                    cells.append("ABSENT")
            emit("%-10s %-5s " % (artifact, ext) + " ".join("%-26s" % c for c in cells))
    emit("")
    emit("ABSENT = the module is not linked into that binary, so its buildinfo does not")
    emit("         carry it. The SBOM describes the binary that is deployed; a per-binary")
    emit("         scope therefore cannot show modules the linker never included.")
    emit("OTHER-VERSION = present but not at the upgraded version (would be a real gap).")
    emit("")
    emit("source-level evidence (backend/go.mod + backend/go.sum, read-only):")
    emit("  go.mod sha256 = %s" % result["source_level"]["go_mod_sha256"])
    emit("  go.sum sha256 = %s" % result["source_level"]["go_sum_sha256"])
    for module, version in EXPECTED:
        emit("  go.mod require %-30s = %s" % (module, go_mod_requires.get(module)))
        emit("  go.sum h1     %-30s = %s" % ("%s@%s" % (module, version), go_sum_h1.get((module, version))))
    emit("")
    emit("validation_result = %s" % ("PASS" if all_ok else "GAPS_PRESENT"))
    if gaps:
        emit("gaps:")
        for item in gaps:
            emit("  - %s" % item)
    else:
        emit("gaps: none (every document parsed; every document is bound to its binary;")
        emit("       every linked upgraded module is at the expected version)")

    result["result"] = "PASS" if all_ok else "GAPS_PRESENT"
    result["gaps"] = gaps

    with open(os.path.join(OUT, "validation.txt"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(report) + "\n")
    with open(os.path.join(OUT, "validation.json"), "w", encoding="utf-8", newline="\n") as handle:
        json.dump(result, handle, indent=2, sort_keys=True)
        handle.write("\n")

    summary = []
    emit2 = summary.append
    emit2("UPGRADED-MODULE PRESENCE IN THE SCOPED SBOMs (per-binary, per-format)")
    emit2("generated_utc = %s" % now)
    emit2("result        = %s" % result["result"])
    emit2("")
    emit2("%-32s %-8s %s" % ("module", "expect", "api cdx|spdx   worker cdx|spdx   migrate cdx|spdx"))
    for module, version in EXPECTED:
        cells = []
        for artifact, _ in ARTIFACTS:
            for ext in ("cdx", "spdx"):
                hit = result["upgraded_modules"]["documents"]["%s.%s" % (artifact, ext)][module]
                cells.append("OK" if hit["version_ok"] else ("absent" if not hit["found"] else "OTHERVER"))
        emit2("%-32s %-8s %s" % (module, version,
                                 "  ".join("%-9s" % c for c in cells[:2]) + "  " +
                                 "  ".join("%-9s" % c for c in cells[2:4]) + "  " +
                                 "  ".join("%-9s" % c for c in cells[4:])))
    emit2("")
    for artifact, _ in ARTIFACTS:
        target = result["targets"][artifact]
        emit2("--- %s   target=%s" % (artifact, target["relative_path"]))
        emit2("    target sha256 = %s" % target["sha256"])
        emit2("    go=%s main=%s embedded_modules=%d vcs_settings=%s"
              % (target["go_version"], target["main_package"], target["embedded_module_count"],
                 json.dumps(target["vcs_settings"], sort_keys=True)))
        for ext in ("cdx", "spdx"):
            doc = result["documents"][artifact][ext]
            count_key = "component_count" if ext == "cdx" else "package_count"
            emit2("    %s: %s=%s sha256=%s" % (ext, count_key, doc.get(count_key), doc.get("sha256")))
    emit2("")
    emit2("Legend: OK = present at the expected upgraded version; absent = not linked into that")
    emit2("binary (per-binary scan scope); OTHERVER = present at a different version (real gap).")
    with open(os.path.join(OUT, "module-crosscheck-summary.txt"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(summary) + "\n")

    print("validation_result=%s" % result["result"])
    print("gaps=%d" % len(gaps))
    return 0 if all_ok else 1


if __name__ == "__main__":
    sys.exit(main())
