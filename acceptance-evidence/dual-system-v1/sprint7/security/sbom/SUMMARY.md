# S7-SEC-01 - SBOM evidence summary

Generated with **syft 1.51.1** run as a container on the acceptance VM.
Every number below was computed by `sbom_report.py` directly from the artifact files on disk.

- Host: `dataserver` (Linux 6.8.0-139-generic x86_64)
- Generated (UTC): 2026-09-12T17:01:05.044587+00:00
- Project root: `/home/euripides/projects/blockchain-platform`
- Evidence dir: `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom`

## 1. Tool and container image provenance

| Item | Value |
|---|---|
| Tool | syft 1.51.1 (Anchore) |
| Container image reference used | `docker.1ms.run/anchore/syft:latest` |
| Fetched image digest | `docker.1ms.run/anchore/syft@sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c` |
| Image ID | `sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c` |
| Image size (bytes) | 29444736 |
| Image created | `2026-08-27T17:00:22.866987799Z` |
| docker server | 29.7.2 |
| syft exit code for every SBOM command | 0 (see `commands.txt`) |

### Registry-mirror deviation (material caveat)

The literal upstream command `sudo -n docker pull anchore/syft:latest` does **not** work on this VM:

1. `registry-1.docker.io` (Docker Hub direct) is **unreachable** - the HTTPS/TLS request times out.
2. The daemon's first registry mirror `https://docker.m.daocloud.io` returns **HTTP 403 Forbidden**
   for `HEAD /v2/anchore/syft/manifests/latest` (the same mirror serves `aquasec/trivy` with 200,
   so this is a repository-specific denial, not a general outage).
3. The mirror `https://docker.1ms.run` returns **HTTP 200** for the same manifest (index_platforms=5),
   and the pull succeeded from there: `PULL_EXIT=0`.

Raw evidence: `logs/registry-probe.log`, `00-pull-syft.log`.
Because Docker Hub itself is unreachable from this host, the pulled image could not be
byte-compared against the upstream `anchore/syft` manifest - see section 9 (NOT_VERIFIED).

## 2. Deliverables

| File | Scan target | Format | Bytes | SHA-256 |
|---|---|---|---|---|
| `backend.cdx.json` | `backend/bin/api` (compiled Go binary) | CycloneDX JSON | 60246 | `a88121ed7c9b7bbdac8f7746d72d8b1b6e95abe9577753ef8d9e48ec6dd72740` |
| `backend.spdx.json` | `backend/bin/api` | SPDX JSON | 86473 | `aa9b008c9da0ebcf3ece0bff161bb681d953ff6117a5dc8b75e53d6e6c75b550` |
| `frontend.cdx.json` | `frontend/` (node_modules + pnpm-lock.yaml) | CycloneDX JSON | 225134 | `3b1f94f01b7ec6aae5f2198d37ffbb05c429f76e50c53565bce28ec749618b46` |
| `frontend.spdx.json` | `frontend/` (node_modules + pnpm-lock.yaml) | SPDX JSON | 382784 | `554ead9edfc7580a608c16c73d3f3794b8d9e5bbb756d39d702adda918fc3199` |
| `syft-version.txt` | syft version block | text | 250 | `d9fb5be8078be2ac21ff48110999fdffead8ce3a2c8e55ad4412fa878e65dc2e` |
| `commands.txt` | every command + exit code + digest | text | 7493 | `b347082638a7510a0c713ffc98a442b6ce6cdaf5f271e79d79c5c0ba921099f6` |
| `validation.txt` | JSON validation output | text | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |

## 3. Headline component counts

| Artifact | Spec | Components / packages | Dependency graph entries |
|---|---|---|---|
| backend.cdx.json | CycloneDX 1.7 | **56** components | 1 |
| backend.spdx.json | SPDX-2.3 | **56** packages | 165 relationships |
| frontend.cdx.json | CycloneDX 1.7 | **184** components | 65 |
| frontend.spdx.json | SPDX-2.3 | **182** packages | 551 relationships |

CDX serial numbers: backend `urn:uuid:e4b14227-2125-4ce2-9728-988d18685842`, frontend `urn:uuid:15424c11-71d1-4939-8f9a-2bf31d31b9fd`.

## 4. Component distribution by type

| Artifact | Distribution |
|---|---|
| backend.cdx.json - CycloneDX `type` | `{"library": 55, "file": 1}` |
| backend.cdx.json - ecosystem (purl type) | `{"golang": 55, "(no purl)": 1}` |
| frontend.cdx.json - CycloneDX `type` | `{"library": 181, "file": 3}` |
| frontend.cdx.json - ecosystem (purl type) | `{"npm": 175, "golang": 6, "(no purl)": 3}` |
| backend.spdx.json - primaryPackagePurpose | `{"(unset)": 55, "FILE": 1}` |
| frontend.spdx.json - primaryPackagePurpose | `{"(unset)": 181, "FILE": 1}` |
| backend.spdx.json - ecosystem (purl) | `{"golang": 55}` |
| frontend.spdx.json - ecosystem (purl) | `{"npm": 175, "golang": 6}` |

## 5. License distribution (top 10)

| Artifact | Top licenses |
|---|---|
| backend.cdx.json | `{"BSD-3-Clause": 1}` |
| frontend.cdx.json | `{"MIT": 14, "Apache-2.0": 4, "BSD-3-Clause": 2, "ISC": 1}` |
| backend.spdx.json (`licenseDeclared`) | `{"NOASSERTION": 55, "BSD-3-Clause": 1}` |
| frontend.spdx.json (`licenseDeclared`) | `{"NOASSERTION": 161, "MIT": 14, "Apache-2.0": 4, "BSD-3-Clause": 2, "ISC": 1}` |

CycloneDX components carrying no license field: backend 55, frontend 163.

## 6. Backend binary build information

| Item | Value |
|---|---|
| Path | `/home/euripides/projects/blockchain-platform/backend/bin/api` |
| Size (bytes) | 55518192 |
| SHA-256 | `bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` |
| mtime (UTC) | 2026-09-12T17:00:03.746839+00:00 |
| Go toolchain used to build | `go1.26.7` |
| Main package path | `github.com/blockchain-platform/blockchain-platform/cmd/api` |
| Go module deps embedded in binary (`go version -m`) | 53 |
| Build settings | `-buildmode=exe; -compiler=gc; CGO_ENABLED=1; CGO_CFLAGS=; CGO_CPPFLAGS=; CGO_CXXFLAGS=; CGO_LDFLAGS=; GOARCH=amd64; GOOS=linux; GOAMD64=v1` |
| Source revision (git commit) | NOT_VERIFIED - project tree is not a git repository |

`file(1)`: ELF 64-bit LSB executable, x86-64, dynamically linked, interpreter /lib64/ld-linux-x86-64.so.2, with debug_info, not stripped.
The binary was NOT stripped, so syft recovered the embedded Go build info (module path + full
dependency list) rather than falling back to symbol heuristics - this is the strongest available
mode for Go SBOM accuracy.

Full `go version -m` output: see `01-target-metadata.txt`.

## 7. Frontend scan target and lockfile cross-check

| Item | Value |
|---|---|
| Scan target | `dir:frontend` |
| `frontend/node_modules` present | yes (268 MB; 9 top-level entries, 126 packages under `.pnpm`) |
| Lockfile | `frontend/pnpm-lock.yaml` (pnpm; 1688 lines, 175 entries under `packages:`) |
| Lockfile SHA-256 | `a11d5a0ec48ef455271427d048a066dc99a2b607f71beaa170188b22766a94b5` |
| `package.json` | blockchain-platform-frontend 0.1.0 |

No `package-lock.json` exists in this repository - the frontend is a pnpm workspace
(`pnpm-workspace.yaml` present), so `pnpm-lock.yaml` is the authoritative lock file and
syft resolved the npm components from the installed tree plus that lockfile.

## 8. Exact commands used

All commands, their exit codes, and the container image digest are recorded verbatim in
`commands.txt`. Core commands:

    sudo -n docker run --rm docker.1ms.run/anchore/syft:latest version

    sudo -n docker run --rm -v /home/euripides/projects/blockchain-platform:/w -w /w docker.1ms.run/anchore/syft:latest backend/bin/api -o cyclonedx-json=/w/acceptance-evidence/dual-system-v1/sprint7/security/sbom/backend.cdx.json

    sudo -n docker run --rm -v /home/euripides/projects/blockchain-platform:/w -w /w docker.1ms.run/anchore/syft:latest backend/bin/api -o spdx-json=/w/acceptance-evidence/dual-system-v1/sprint7/security/sbom/backend.spdx.json

    sudo -n docker run --rm -v /home/euripides/projects/blockchain-platform:/w -w /w docker.1ms.run/anchore/syft:latest dir:frontend -o cyclonedx-json=/w/acceptance-evidence/dual-system-v1/sprint7/security/sbom/frontend.cdx.json

    sudo -n docker run --rm -v /home/euripides/projects/blockchain-platform:/w -w /w docker.1ms.run/anchore/syft:latest dir:frontend -o spdx-json=/w/acceptance-evidence/dual-system-v1/sprint7/security/sbom/frontend.spdx.json

Container writes were performed as root; `chown euripides:euripides` restored host ownership afterwards.

## 8.1 SCAN TARGET CORRECTION (material)

The first generation pass used the command given in the task brief, which mounts the project at
`/w` and passes the file target `bin/api`. Because `-w /w` sets the working directory to the mount
root, that target resolved to `/w/bin/api` = `<project>/bin/api` - a **stale build from
2026-09-08**, NOT the deployable `backend/bin/api`.

| Path | Size | SHA-256 | mtime |
|---|---|---|---|
| `<project>/bin/api` (what the first pass scanned - WRONG) | 50347852 | `33440571c848795bc787d437d32ec07f6c3621f57d44adddd5be4f440dccf5ad` | 2026-09-08 22:37 local |
| `backend/bin/api` (the deliverable - CORRECT) | 55518192 | `bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` | 2026-09-12T17:00:03.746839+00:00 |

Proof that syft records the true sha256 of whatever file it is handed: the first pass wrote
`metadata.component.version` = `sha256:33440571c848...`, which is byte-for-byte the sha256 of
`<project>/bin/api`. A control experiment on a plain 23-byte text file likewise returned
`sha256:97e13733...` = the file's own sha256. The field is therefore a genuine file digest, and
the corrected run shows `sha256:bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` for `backend/bin/api` (matches = True).

Both the SBOMs and this summary were regenerated against `backend/bin/api`; the superseded
first-pass artifacts are preserved under `discarded-wrong-target/` as an audit trail.

### Scan-target timeline for backend/bin/api (concurrent deploy loop is active)

| local mtime | size | sha256 | note |
|---|---|---|---|
| 2026-09-12 23:28:04 +0700 | 55370073 | `753ba724335e4be4231d40ca65809f22403eb3323b79f144b1161f88a6f21c78` | observed at 16:44:04Z, before this task began scanning |
| 2026-09-12 23:54:22 +0700 | 55370073 | `6106d8e4b09ed881864c9bc9212b29a45b698dc78825dcf936d317b6ed890a10` | replaced by the deploy loop mid-run; api restarted (pid 2799693) |
| 2026-09-13 00:00:03 +0700 | 55518192 | `bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` | CURRENT build - this is the build the final SBOMs describe |

Cross-check at summary generation time: sha256 of `backend/bin/api` = `bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142`;
`metadata.component.version` recorded by syft = `sha256:bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142`; equal = **True**.

## 9. Verification status

| Claim | Status | Evidence |
|---|---|---|
| syft container image fetched | VERIFIED (via mirror docker.1ms.run) | `00-pull-syft.log`, `PULL_EXIT=0` |
| Image digest recorded | VERIFIED | `syft-image-info.txt` |
| backend SBOM describes the DEPLOYED deliverable `backend/bin/api` | VERIFIED - the sha256 syft recorded for the scanned file equals the real sha256 of `backend/bin/api` (`bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142`) | `backend.cdx.json` metadata.component.version = `sha256:bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142` |
| backend SBOM built from the COMPILED binary (no degradation to source scan) | VERIFIED | `commands.txt` target `backend/bin/api`, syft exit 0 |
| **TARGET-PATH DEFECT found and corrected during this run** | The first pass used target `bin/api` with `-w /w`, which resolves to `/w/bin/api` = `<project>/bin/api` - a STALE build from 2026-09-08 (50,347,852 B, sha256 `33440571c848795bc787d437d32ec07f6c3621f57d44adddd5be4f440dccf5ad`), NOT the deliverable. That output was discarded and regenerated against `backend/bin/api`. | `discarded-wrong-target/README.txt` |
| frontend SBOMs generated | VERIFIED | `logs/frontend-cdx.log`, `logs/frontend-spdx.log`, exit 0 |
| All four documents parse as valid JSON | VERIFIED | `validation.txt` |
| Image content identical to upstream Docker Hub `anchore/syft` | NOT_VERIFIED - Docker Hub unreachable, no upstream digest available to compare | `logs/registry-probe.log` |
| Source commit / CI provenance of `bin/api` | NOT_VERIFIED - project tree is not a git repository | `01-target-metadata.txt` |
| Vulnerability scanning / CVE findings | OUT OF SCOPE for S7-SEC-01 (SBOM generation only) | - |

## 10. Evidence file inventory

- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/backend.cdx.json` (60246 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/backend.spdx.json` (86473 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/frontend.cdx.json` (225134 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/frontend.spdx.json` (382784 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/syft-version.txt` (250 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/syft-image-info.txt` (467 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/syft-image-digest.txt` (100 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/commands.txt` (7493 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/SUMMARY.md` (11490 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/validation.txt` (0 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/sbom_report.py` (22225 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/00-pull-syft.log` (272794 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/01-target-metadata.txt` (10686 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/sbom-artifact-hashes.txt` (2453 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/logs/registry-probe.log` (1428 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/logs/backend-cdx.log` (0 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/logs/backend-spdx.log` (0 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/logs/frontend-cdx.log` (135 bytes)
- `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/logs/frontend-spdx.log` (135 bytes)

