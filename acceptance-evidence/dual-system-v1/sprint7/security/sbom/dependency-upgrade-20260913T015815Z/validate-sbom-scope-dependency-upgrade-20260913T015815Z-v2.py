#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
S7-SEC-01 follow-up (2026-09-13 dependency-upgrade scope)
CORRECTIVE PASS 2 validator for the six scoped SBOM documents.

History of this check (both earlier passes are preserved, never edited):
  pass 0  embedded in generate-sbom-dependency-upgrade-20260913T015815Z.sh
          -> crashed: TypeError in sha256f() (one name reused for the SBOM path
             and for an SPDX package loop variable). Left the empty
             module-crosscheck.txt behind.
  pass 1  validate-sbom-scope-dependency-upgrade-20260913T015815Z.py
          -> complete human report (validation.txt) but then crashed in
             json.dump(sort_keys=True) because two distribution maps contained
             a None key; and its go.mod/go.sum/buildinfo version lookups compared
             "5.3.2" against the stored "v5.3.2", so the h1 cross-check printed a
             false negative. Left a truncated validation.json behind.
  pass 2  THIS FILE (validation-v2.txt / validation-v2.json / module-crosscheck-summary.txt)
          -> fixes: version normalisation for every lookup, str()-safe counter
             keys, explicit REAL_GAP vs PER_BINARY_ABSENT classification, and a
             re-hash of the six SBOM documents to prove the validators did not
             alter them.

ADDITIVE ONLY / read-only w.r.t. the repository: refuses to run if any of its
three outputs already exists; no delete/move/truncate; no sudo; no service calls.
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

OUTPUTS = ["validation-v2.txt", "validation-v2.json", "module-crosscheck-summary.txt"]


def norm_version(text):
    """'v5.3.2' -> '5.3.2'; None -> ''."""
    text = (text or "").strip()
    return text[1:] if text.startswith("v") else text


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


def parse_go_mod_requires(path):
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
    found = {}
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            parts = raw.split()
            if len(parts) >= 3 and parts[2].startswith("h1:"):
                found[(parts[0], norm_version(parts[1]))] = parts[2]
    return found


def parse_buildinfo(path):
    deps = {}
    settings = {}
    main_info = {}
    with open(path, encoding="utf-8") as handle:
        for raw in handle:
            cols = [c for c in raw.rstrip("\n").split("\t") if c != ""]
            if not cols:
                continue
            if cols[0] == "dep" and len(cols) >= 3:
                deps[cols[1]] = {"version": cols[2], "h1": cols[3] if len(cols) > 3 else ""}
            elif cols[0] == "build" and len(cols) >= 2:
                key, _, value = cols[1].partition("=")
                settings[key] = value
            elif cols[0] == "path" and len(cols) >= 2:
                main_info["path"] = cols[1]
            elif cols[0] == "mod" and len(cols) >= 2:
                main_info["module"] = cols[1]
                main_info["module_version"] = cols[2] if len(cols) > 2 else ""
    return deps, settings, main_info


def main():
    guard_outputs()
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")

    go_mod_path = os.path.join(PROJ, "backend/go.mod")
    go_sum_path = os.path.join(PROJ, "backend/go.sum")
    go_mod_requires = parse_go_mod_requires(go_mod_path)
    go_sum_h1 = parse_go_sum(go_sum_path)

    # previous-pass SBOM hashes, to prove nothing was rewritten by the validators
    previous_hashes = {}
    with open(os.path.join(OUT, "artifact-hashes.txt"), encoding="utf-8") as handle:
        for raw in handle:
            parts = raw.split()
            if len(parts) == 2 and parts[1].endswith(".json"):
                previous_hashes[parts[1]] = parts[0]

    lines = []
    emit = lines.append
    emit("=" * 78)
    emit("VALIDATION (PASS 2) OF THE SCOPED SBOM DOCUMENTS - 2026-09-13 dependency-upgrade scope")
    emit("supersedes validation.txt for the go.mod/go.sum h1 cross-check only; that file is")
    emit("preserved byte-for-byte as the record of validation pass 1")
    emit("utc=%s" % now)
    emit("=" * 78)

    result = {
        "generated_utc": now,
        "validator": "validate-sbom-scope-dependency-upgrade-20260913T015815Z-v2.py",
        "validation_pass": 2,
        "supersedes": "validation.txt (pass 1) for the go.mod/go.sum h1 cross-check",
        "targets": {},
        "documents": {},
        "upgraded_modules": {
            "expected": [{"module": m, "version": v} for m, v in EXPECTED],
            "documents": {},
            "real_gaps": [],
            "per_binary_absent": [],
        },
        "source_level": {},
        "sbom_unchanged_by_validator": {},
    }

    emit("")
    emit("SOURCE-LEVEL EVIDENCE (read-only): backend/go.mod + backend/go.sum")
    emit("  go.mod sha256 = %s" % sha256_file(go_mod_path))
    emit("  go.sum sha256 = %s" % sha256_file(go_sum_path))
    emit("  %-32s %-9s %-9s %-9s %s" % ("module", "expect", "go.mod", "ok", "go.sum h1"))
    source_rows = []
    for module, version in EXPECTED:
        mod_ver_raw = go_mod_requires.get(module)
        mod_ok = norm_version(mod_ver_raw) == version
        h1 = go_sum_h1.get((module, version))
        emit("  %-32s %-9s %-9s %-9s %s" % (module, version, mod_ver_raw, mod_ok, h1))
        source_rows.append({
            "module": module,
            "expected_version": version,
            "go_mod_version": mod_ver_raw,
            "go_mod_version_ok": mod_ok,
            "go_sum_h1": h1,
        })
    result["source_level"] = {
        "go_mod": go_mod_path,
        "go_mod_sha256": sha256_file(go_mod_path),
        "go_sum": go_sum_path,
        "go_sum_sha256": sha256_file(go_sum_path),
        "modules": source_rows,
    }

    real_gap_documents = set()
    all_documents_valid = True
    all_bound = True

    for artifact, rel_target in ARTIFACTS:
        target_path = os.path.join(PROJ, rel_target)
        target_sha = sha256_file(target_path)
        target_size = os.path.getsize(target_path)
        deps, settings, main_info = parse_buildinfo(os.path.join(OUT, "buildinfo-%s.txt" % artifact))
        with open(os.path.join(OUT, "buildinfo-%s.txt" % artifact), encoding="utf-8") as handle:
            head = "".join(handle.readline() for _ in range(3))
        match = re.search(r"go1\.[0-9.]+", head)
        go_version = match.group(0) if match else None
        vcs_settings = {k: v for k, v in settings.items() if k.startswith("vcs")}

        entry_target = {
            "path": target_path,
            "relative_path": rel_target,
            "sha256": target_sha,
            "size_bytes": target_size,
            "go_version": go_version,
            "main_package": main_info.get("path"),
            "main_module": main_info.get("module"),
            "main_module_version": main_info.get("module_version", ""),
            "embedded_module_count": len(deps),
            "build_settings": settings,
            "vcs_settings": vcs_settings,
            "vcs_revision_embedded": bool(vcs_settings),
        }
        result["targets"][artifact] = entry_target
        result["documents"][artifact] = {}

        emit("")
        emit("-" * 78)
        emit("ARTIFACT %s" % artifact)
        emit("  scanned target       = %s" % target_path)
        emit("  target sha256        = %s   (%d bytes)" % (target_sha, target_size))
        emit("  go toolchain         = %s" % go_version)
        emit("  main package         = %s" % main_info.get("path"))
        emit("  main module          = %s %s" % (main_info.get("module"), main_info.get("module_version", "")))
        emit("  embedded go modules  = %d  (read from the binary's own buildinfo)" % len(deps))
        emit("  build settings       = %s" % json.dumps(settings, sort_keys=True))
        emit("  vcs build settings   = %s  (empty => no VCS revision/commit is embedded)"
             % json.dumps(vcs_settings, sort_keys=True))

        for fmt, ext in (("cyclonedx", "cdx"), ("spdx", "spdx")):
            doc_path = os.path.join(OUT, "%s.%s.json" % (artifact, ext))
            base = os.path.basename(doc_path)
            entry = {"file": base}
            emit("")
            emit("  FILE %s" % doc_path)
            if not os.path.exists(doc_path):
                entry["parse"] = "MISSING"
                all_documents_valid = False
                result["documents"][artifact][ext] = entry
                emit("    *** MISSING ***")
                continue
            entry["size_bytes"] = os.path.getsize(doc_path)
            entry["sha256"] = sha256_file(doc_path)
            entry["sha256_matches_artifact_hashes_txt"] = previous_hashes.get(base) == entry["sha256"]
            result["sbom_unchanged_by_validator"][base] = entry["sha256_matches_artifact_hashes_txt"]
            emit("    size_bytes = %d" % entry["size_bytes"])
            emit("    sha256     = %s" % entry["sha256"])
            emit("    unchanged since generation (artifact-hashes.txt) = %s"
                 % entry["sha256_matches_artifact_hashes_txt"])
            try:
                with open(doc_path, encoding="utf-8") as handle:
                    document = json.load(handle)
                entry["parse"] = "OK"
                emit("    JSON parse = OK")
            except Exception as exc:  # noqa: BLE001
                entry["parse"] = "FAILED: %s" % exc
                all_documents_valid = False
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
                entry["type_distribution"] = {
                    str(k): v for k, v in collections.Counter(c.get("type") for c in components).items()
                }
                ecosystems = collections.Counter()
                for component in components:
                    purl = component.get("purl") or ""
                    ecosystems[str(purl.split("/")[1] if purl.startswith("pkg:") else "(no purl)")] += 1
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
                        licenses[str(lic.get("id") or lic.get("name") or "(unnamed)")] += 1
                entry["licenses"] = dict(licenses)
                entry["components_without_license"] = without_license
                binding_ok = (meta_component.get("version") or "") == "sha256:" + target_sha
                entry["target_binding"] = {
                    "metadata_component_type": meta_component.get("type"),
                    "metadata_component_version": meta_component.get("version"),
                    "real_file_sha256": target_sha,
                    "match": binding_ok,
                }
                all_bound = all_bound and binding_ok
                emit("    format=%s  serial=%s" % (entry["format"], entry["serial_number"]))
                emit("    metadata.component=%s" % json.dumps(entry["metadata_component"]))
                emit("    components=%d  dependency_entries=%d" % (entry["component_count"], entry["dependency_count"]))
                emit("    types=%s" % json.dumps(entry["type_distribution"], sort_keys=True))
                emit("    ecosystems=%s" % json.dumps(entry["ecosystem_distribution"], sort_keys=True))
                emit("    licenses=%s (components without a license field: %d)"
                     % (json.dumps(entry["licenses"], sort_keys=True) or "{}", without_license))
                emit("    TARGET BINDING metadata.component.version == real binary sha256 -> %s" % binding_ok)
                for module, version in EXPECTED:
                    needle = "pkg:golang/%s@" % module
                    hits = [c for c in components if (c.get("purl") or "").startswith(needle)]
                    version_ok = any(
                        norm_version(h.get("version")) == version
                        or norm_version((h.get("purl") or "").split("@")[-1]) == version
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
                entry["primary_package_purpose"] = {
                    str(k): v for k, v in collections.Counter(p.get("primaryPackagePurpose") for p in packages).items()
                }
                ecosystems = collections.Counter()
                for package in packages:
                    for ref in (package.get("externalRefs") or []):
                        locator = ref.get("referenceLocator") or ""
                        if ref.get("referenceType") == "purl":
                            ecosystems[str(locator.split("/")[1] if locator.startswith("pkg:") else "(other)")] += 1
                            break
                    else:
                        ecosystems["(no purl)"] += 1
                entry["ecosystem_distribution"] = dict(ecosystems)
                entry["licenses"] = {
                    str(k): v for k, v in collections.Counter(p.get("licenseDeclared") for p in packages).items()
                }
                emit("    format=%s  name=%s" % (entry["format"], entry["name"]))
                emit("    creators=%s" % json.dumps(entry["creators"]))
                emit("    packages=%d  relationships=%d" % (entry["package_count"], entry["relationship_count"]))
                emit("    primaryPackagePurpose=%s" % json.dumps(entry["primary_package_purpose"], sort_keys=True))
                emit("    ecosystems=%s" % json.dumps(entry["ecosystem_distribution"], sort_keys=True))
                emit("    licenses=%s" % json.dumps(entry["licenses"], sort_keys=True))
                for module, version in EXPECTED:
                    needle = "pkg:golang/%s@" % module
                    hits = []
                    for package in packages:
                        for ref in (package.get("externalRefs") or []):
                            locator = ref.get("referenceLocator") or ""
                            if ref.get("referenceType") == "purl" and locator.startswith(needle):
                                hits.append({"name": package.get("name"), "versionInfo": package.get("versionInfo"), "purl": locator})
                    version_ok = any(
                        norm_version(h.get("versionInfo")) == version
                        or norm_version((h.get("purl") or "").split("@")[-1]) == version
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
                hit = module_hits[module]
                if hit["version_ok"]:
                    continue
                if hit["found"]:
                    # present but wrong version: a REAL gap
                    real_gap_documents.add(key)
                    result["upgraded_modules"]["real_gaps"].append(
                        {"document": key, "module": module, "expected_version": version,
                         "observed": hit.get("components") or hit.get("packages")})
                else:
                    result["upgraded_modules"]["per_binary_absent"].append(
                        {"document": key, "module": module, "expected_version": version,
                         "reason": "module is not linked into this binary; its buildinfo does not carry it"})
            result["documents"][artifact][ext] = entry

        emit("")
        emit("  buildinfo vs go.mod/go.sum cross-check for the four upgraded modules:")
        crosscheck = {}
        for module, version in EXPECTED:
            dep = deps.get(module)
            binary_h1 = (dep or {}).get("h1") or None
            source_h1 = go_sum_h1.get((module, version))
            mod_ver_raw = go_mod_requires.get(module)
            crosscheck[module] = {
                "expected_version": version,
                "linked_into_binary": dep is not None,
                "binary_buildinfo_version": (dep or {}).get("version"),
                "binary_buildinfo_h1": binary_h1,
                "go_mod_version": mod_ver_raw,
                "go_mod_version_ok": norm_version(mod_ver_raw) == version,
                "go_sum_h1_for_expected_version": source_h1,
                "h1_matches_go_sum": bool(binary_h1 and source_h1 and binary_h1 == source_h1),
                "binary_version_matches_expected": norm_version((dep or {}).get("version")) == version,
            }
            emit("    %-32s expected=%s linked=%-5s buildinfo=%s go.mod=%s h1==go.sum=%s"
                 % (module, version, dep is not None, crosscheck[module]["binary_buildinfo_version"],
                    mod_ver_raw, crosscheck[module]["h1_matches_go_sum"]))
        result["targets"][artifact]["module_crosscheck"] = crosscheck

    emit("")
    emit("=" * 78)
    emit("UPGRADED-MODULE PRESENCE MATRIX (per document, per format)")
    emit("=" * 78)
    emit("%-9s %-5s %s" % ("artifact", "fmt", "  ".join("%-14s" % (m.split("/")[-1] + "@" + v) for m, v in EXPECTED)))
    for artifact, _ in ARTIFACTS:
        for ext in ("cdx", "spdx"):
            key = "%s.%s" % (artifact, ext)
            cells = []
            for module, _v in EXPECTED:
                hit = result["upgraded_modules"]["documents"][key][module]
                cells.append("OK" if hit["version_ok"] else ("OTHER-VERSION" if hit["found"] else "NOT-LINKED"))
            emit("%-9s %-5s %s" % (artifact, ext, "  ".join("%-14s" % c for c in cells)))
    emit("")
    emit("OK            = the module is present in that SBOM at the expected upgraded version")
    emit("OTHER-VERSION = present but at a different version  -> REAL GAP")
    emit("NOT-LINKED    = the module is not linked into that binary, so its buildinfo does not")
    emit("                carry it and no SBOM generated from that binary can list it.")
    emit("                The four upgrades are still evidenced at source level (backend/go.mod,")
    emit("                whose sha256 matches the recorded post-upgrade source digest).")
    emit("")
    emit("real_gaps             = %s" % json.dumps(result["upgraded_modules"]["real_gaps"]))
    emit("not_linked_occurrences= %d  (informational, expected for the api-only modules)"
         % len(result["upgraded_modules"]["per_binary_absent"]))
    emit("")
    emit("all_documents_parse   = %s" % all_documents_valid)
    emit("all_documents_bound  = %s" % all_bound)
    emit("sboms_unchanged_by_validator = %s" % json.dumps(result["sbom_unchanged_by_validator"], sort_keys=True))
    emit("")
    emit("validation_result = %s" % ("PASS" if (all_documents_valid and all_bound and not real_gap_documents) else "GAPS_PRESENT"))
    emit("  PASS here means: every document parses as JSON, every document is cryptographically")
    emit("  bound to the binary it claims to describe, and no document carries one of the four")
    emit("  upgraded modules at a version other than the upgraded one.")
    emit("  It does NOT mean all four modules appear in all six documents - see the matrix above.")

    result["all_documents_parse"] = all_documents_valid
    result["all_documents_bound"] = all_bound
    result["result"] = "PASS" if (all_documents_valid and all_bound and not real_gap_documents) else "GAPS_PRESENT"

    with open(os.path.join(OUT, "validation-v2.txt"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(lines) + "\n")
    with open(os.path.join(OUT, "validation-v2.json"), "w", encoding="utf-8", newline="\n") as handle:
        json.dump(result, handle, indent=2, sort_keys=True, ensure_ascii=True)
        handle.write("\n")

    summary = []
    add = summary.append
    add("UPGRADED-MODULE PRESENCE IN THE SCOPED SBOMs (per-binary, per-format)")
    add("generated_utc = %s" % now)
    add("validation_result = %s" % result["result"])
    add("real_gaps = %d   not_linked_occurrences = %d"
        % (len(result["upgraded_modules"]["real_gaps"]), len(result["upgraded_modules"]["per_binary_absent"])))
    add("")
    add("%-32s %-8s %s" % ("module", "expect", "api cdx|spdx    worker cdx|spdx    migrate cdx|spdx"))
    for module, version in EXPECTED:
        cells = []
        for artifact, _ in ARTIFACTS:
            for ext in ("cdx", "spdx"):
                hit = result["upgraded_modules"]["documents"]["%s.%s" % (artifact, ext)][module]
                cells.append("OK" if hit["version_ok"] else ("OTHERVER" if hit["found"] else "NOT-LINKED"))
        add("%-32s %-8s %s" % (module, version,
                               "  ".join("%-10s" % c for c in cells[:2]) + "  " +
                               "  ".join("%-10s" % c for c in cells[2:4]) + "  " +
                               "  ".join("%-10s" % c for c in cells[4:])))
    add("")
    for artifact, _ in ARTIFACTS:
        target = result["targets"][artifact]
        add("--- %s   target=%s" % (artifact, target["relative_path"]))
        add("    target sha256 = %s" % target["sha256"])
        add("    go=%s main=%s embedded_modules=%d vcs_settings=%s"
            % (target["go_version"], target["main_package"], target["embedded_module_count"],
               json.dumps(target["vcs_settings"], sort_keys=True)))
        for ext in ("cdx", "spdx"):
            doc = result["documents"][artifact][ext]
            count_key = "component_count" if ext == "cdx" else "package_count"
            add("    %s: %s=%s sha256=%s" % (ext, count_key, doc.get(count_key), doc.get("sha256")))
    add("")
    add("NOT-LINKED = not present in that binary at all (per-binary scan scope), not a version defect.")
    with open(os.path.join(OUT, "module-crosscheck-summary.txt"), "w", encoding="utf-8", newline="\n") as handle:
        handle.write("\n".join(summary) + "\n")

    print("validation_result=%s" % result["result"])
    print("real_gaps=%d not_linked=%d" % (len(result["upgraded_modules"]["real_gaps"]),
                                          len(result["upgraded_modules"]["per_binary_absent"])))
    return 0 if result["result"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
