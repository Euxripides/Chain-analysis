# S7-SEC-01 follow-up - scoped SBOM for the 2026-09-13 dependency-upgrade binaries

Scope: a NEW, deliberately narrow SBOM set that describes exactly the three Go binaries
rebuilt and deployed in the 2026-09-13 dependency upgrade, so the SBOM is bound to the
artifacts that are actually running rather than to a superseded build.

| Item | Value |
|---|---|
| Host | `dataserver` |
| Generated (UTC) | 2026-09-13T02:16:10Z |
| Project root | `/home/euripides/projects/blockchain-platform` |
| Evidence dir (new) | `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z` |
| Tool | syft 1.51.1 (Anchore), container image `docker.1ms.run/anchore/syft:latest` |
| Scan target kind | **file** (compiled Go binary) - not an image, not a directory |
| Artifacts | `backend/bin/api`, `backend/bin/worker`, `backend/bin/migrate` |
| Documents | 6 (CycloneDX JSON + SPDX JSON per artifact) |
| Cross-reference | `/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z` |
| Overall verification | GAPS PRESENT - see below |

Companion machine-readable manifest: `sbom-manifest.json` (same directory).
The existing SBOMs in the parent directory (`backend.*`, `frontend.*`,
`discarded-wrong-target/`) were NOT touched; they are left byte-for-byte as they were.

---

## 1. Target identity (what was scanned)

Each SBOM was generated from the deployed binary file itself, on the acceptance VM:

| Artifact | Scoped target path | Size (bytes) | sha256 |
|---|---|---|---|
| api | `/home/euripides/projects/blockchain-platform/backend/bin/api` | 56147343 | `8993916fc5a6abd02ed04896d84ccf56c9882078a6261b4b3f5543a569b2982a` |
| worker | `/home/euripides/projects/blockchain-platform/backend/bin/worker` | 42685594 | `513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48` |
| migrate | `/home/euripides/projects/blockchain-platform/backend/bin/migrate` | 30859300 | `28cee869e44fb60840daeb78f8860afa62d7dd41d8f1301c2ab7ce83d64664c4` |

These three digests are the ones given for the deployed binaries in the task brief, and they
were re-verified here with `sha256sum` on the VM (see `artifact-hashes.txt` and `scan-stability.txt`). The files were hashed again immediately before this
report was written and did not change during the whole run.

Live process binding at report time (identity of what is deployed, read from /proc):

| Artifact | Live process |
|---|---|
| api | pid 3965182 -> `/home/euripides/projects/blockchain-platform/backend/bin/api` |
| worker | pid 3965195 -> `/home/euripides/projects/blockchain-platform/backend/bin/worker` |
| migrate | no live process at report time |

`migrate` is a one-shot CLI, so it is normally not running; that is expected and is
not reported as a failure.

## 2. Tool identity

| Item | Value |
|---|---|
| Tool | syft 1.51.1 (Anchore) |
| Tool git commit | `91a0032987d91b7411b52f6f5c185c5e7f775495` |
| Tool build date | 2026-08-27T16:55:02Z |
| syft schema version | 16.1.10 |
| **syft executable sha256** | `abca2def61de9952fa06d3977bb1e064818facb9badfce502b450d3d6846a91f` |
| Image reference | `docker.1ms.run/anchore/syft:latest` |
| Image id | `sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c` |
| Image repo digests | ["docker.1ms.run/anchore/syft@sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c","anchore/syft@sha256:95fe0835e5bebc6f8b1f8acef68d47d63d594ef4c0f25c097ff853b23cbac74c"] |
| Image created | 2026-08-27T17:00:22.866987799Z |
| Image size (bytes) | 29444736 |

The syft executable hash was obtained by extracting the `/syft` file from
`docker save` output, because the image is distroless (no `/bin/sh`, no
coreutils `sha256sum`) and no syft binary is installed on the host. Method and
result: `syft-binary-sha256.txt`.

**Deviation from the pre-existing flow (recorded honestly):** the older `sbom/` flow
ran `sudo -n docker run`. This task forbids sudo/root, so this scope runs plain
`docker run` as the invoking unprivileged account (uid 1000, member of the `docker` group) with `--user $(id -u):$(id -g)`. Nothing ran as root and no `chown` was needed. The syft version and container image are the same ones the previous flow used.

## 3. Exact reproduction commands

Run on the acceptance VM as `euripides` (no sudo):

```bash
PROJ=/home/euripides/projects/blockchain-platform
REL=acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z
IMG=docker.1ms.run/anchore/syft:latest

# tool identity
docker image inspect $IMG --format '{{.Id}} {{json .RepoDigests}}'
docker run --rm --user $(id -u):$(id -g) $IMG version

# target identity (the three deployed binaries)
sha256sum $PROJ/backend/bin/api $PROJ/backend/bin/worker $PROJ/backend/bin/migrate
PATH=/usr/local/go/bin:$PATH go version -m $PROJ/backend/bin/api

# the six documents: 3 artifacts x (CycloneDX, SPDX)
for b in api worker migrate; do
  docker run --rm --user $(id -u):$(id -g) --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp \
    -v $PROJ:/w -w /w $IMG backend/bin/$b -o cyclonedx-json=/w/$REL/$b.cdx.json
  docker run --rm --user $(id -u):$(id -g) --tmpfs /tmp:rw,mode=1777 -e HOME=/tmp \
    -v $PROJ:/w -w /w $IMG backend/bin/$b -o spdx-json=/w/$REL/$b.spdx.json
done

# validation (pass 2) and the machine-readable result
python3 $PROJ/$REL/validate-sbom-scope-dependency-upgrade-20260913T015815Z-v2.py

# this manifest and report
python3 $PROJ/$REL/build-manifest-and-report-dependency-upgrade-20260913T015815Z.py \
  c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66 false
```

Per-step exit codes of the generation run are in `commands.txt`; the raw console
output is `generator-console.txt`.

## 4. Deliverables and hashes

| File | Format | Target | Size (bytes) | Components/packages | sha256 |
|---|---|---|---|---|---|
| `api.cdx.json` | CycloneDX 1.7 | `backend/bin/api` | 60083 | 56 | `d8cd34d0d56ba18a10eaa363fcd9fdd2c5392faffc231d0dc25f97aa3242096a` |
| `api.spdx.json` | SPDX-2.3 (CC0-1.0) | `backend/bin/api` | 86474 | 56 | `a8e1c7eb6e7ce296735644a7eb5db530f813177efd35ba130f9691588708182c` |
| `worker.cdx.json` | CycloneDX 1.7 | `backend/bin/worker` | 47752 | 45 | `b61c3ef8ead547fe2af4453d1f7f30a3653e1365fbc2a331fa6071de2210fdf2` |
| `worker.spdx.json` | SPDX-2.3 (CC0-1.0) | `backend/bin/worker` | 68963 | 45 | `4daac5ea63d41f895c68c58aae287c37e28e0010fb01a5bca76a3fbe4b31912e` |
| `migrate.cdx.json` | CycloneDX 1.7 | `backend/bin/migrate` | 34611 | 33 | `f4d2ae4f9440a188dec119de551657b613dedfbc99d4e0d5338a98b8d400cfec` |
| `migrate.spdx.json` | SPDX-2.3 (CC0-1.0) | `backend/bin/migrate` | 49939 | 33 | `f53fbf3fa1c5998ae1ce748cc23165b637c8bccd2c6357a99cce7a28c6b708ce` |

Supporting evidence in the same directory: `commands.txt`, `artifact-hashes.txt`,
`scan-stability.txt`, `syft-version.txt`, `syft-image-identity.txt`, `syft-binary-sha256.txt`,
`buildinfo-<artifact>.txt`, `validation.txt` (pass 1), `validation-v2.txt` / `validation-v2.json` (pass 2),
`module-crosscheck*.txt`, `cross-reference-dependency-remediation.txt`, `environment-probe.txt`, and `logs/`.

## 5. Results per artifact

| Artifact | CycloneDX components | SPDX packages | Component types | purl namespaces | Components without licence |
|---|---|---|---|---|---|
| api | 56 | 56 | `{"file": 1, "library": 55}` | `{"(no purl)": 1, "github.com": 45, "go.opentelemetry.io": 2, "golang.org": 5, "google.golang.org": 1, "gopkg.in": 1, "stdlib@1.26.7": 1}` | 55 |
| worker | 45 | 45 | `{"file": 1, "library": 44}` | `{"(no purl)": 1, "github.com": 34, "go.opentelemetry.io": 2, "golang.org": 5, "google.golang.org": 1, "gopkg.in": 1, "stdlib@1.26.7": 1}` | 44 |
| migrate | 33 | 33 | `{"file": 1, "library": 32}` | `{"(no purl)": 1, "github.com": 23, "go.opentelemetry.io": 2, "golang.org": 4, "google.golang.org": 1, "gopkg.in": 1, "stdlib@1.26.7": 1}` | 32 |

Every library component is a Go component (purl type `pkg:golang`); the namespace
column is the purl namespace (`github.com`, `golang.org`, ...), and the
extra `(no purl)` entry is the scanned binary itself, which syft records as a `file` component.

Target binding - the check that each document really describes its binary:

| Artifact | `metadata.component.type` | `metadata.component.version` | Real file sha256 | Match |
|---|---|---|---|---|
| api | `file` | `sha256:8993916fc5a6abd02ed04896d84ccf56c9882078a6261b4b3f5543a569b2982a` | `8993916fc5a6abd02ed04896d84ccf56c9882078a6261b4b3f5543a569b2982a` | **yes** |
| worker | `file` | `sha256:513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48` | `513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48` | **yes** |
| migrate | `file` | `sha256:28cee869e44fb60840daeb78f8860afa62d7dd41d8f1301c2ab7ce83d64664c4` | `28cee869e44fb60840daeb78f8860afa62d7dd41d8f1301c2ab7ce83d64664c4` | **yes** |

Build information read from each binary with `go version -m`:

| Artifact | Go toolchain | Main package | Embedded modules | VCS settings |
|---|---|---|---|---|
| api | go1.26.7 | `github.com/blockchain-platform/blockchain-platform/cmd/api` | 53 | `{}` |
| worker | go1.26.7 | `github.com/blockchain-platform/blockchain-platform/cmd/worker` | 42 | `{}` |
| migrate | go1.26.7 | `github.com/blockchain-platform/blockchain-platform/cmd/migrate` | 30 | `{}` |

Build settings are identical for all three binaries: `{"-buildmode": "exe", "-compiler": "gc", "CGO_CFLAGS": "", "CGO_CPPFLAGS": "", "CGO_CXXFLAGS": "", "CGO_ENABLED": "1", "CGO_LDFLAGS": "", "GOAMD64": "v1", "GOARCH": "amd64", "GOOS": "linux"}`.
No `vcs.*` setting is present and the main module version is `(devel)`: **no commit
hash is embedded in these binaries** (see section 10).

## 6. Upgraded-module verification

Expected upgraded versions: `github.com/go-chi/chi/v5` 5.3.2, `github.com/xuri/excelize/v2` 2.11.0, `github.com/jackc/pgx/v5` 5.9.2, `golang.org/x/crypto` 0.56.0.

| Artifact | Format | v5 5.3.2 | v2 2.11.0 | v5 5.9.2 | crypto 0.56.0 |
|---|---|---|---|---|---|
| api | cdx | YES | YES | YES | YES |
| api | spdx | YES | YES | YES | YES |
| worker | cdx | not linked | not linked | YES | YES |
| worker | spdx | not linked | not linked | YES | YES |
| migrate | cdx | not linked | not linked | YES | YES |
| migrate | spdx | not linked | not linked | YES | YES |

Number of modules found at a WRONG version (the genuine defect case): **0**.

### Honest statement of the gap

- `golang.org/x/crypto v0.56.0` and `github.com/jackc/pgx/v5 v5.9.2` are present at the
  upgraded version in **all six** documents.
- `github.com/go-chi/chi/v5 v5.3.2` and `github.com/xuri/excelize/v2 v2.11.0` are present at the
  upgraded version in the **api** documents only. They are **absent from the worker and
  migrate documents**, because those two modules are not linked into those two binaries:
  `go version -m backend/bin/worker` lists 42 modules and `backend/bin/migrate` lists 30,
  and neither list contains chi or excelize. An SBOM generated from a binary cannot list a
  module the linker never included, so this is a real limitation of the per-binary scope,
  not a generation error - and it is reported here as a gap rather than smoothed over.
- The upgrade of those two modules is still evidenced at source level by `backend/go.mod`
  (sha256 `87356daa3f6ccb99cee5478b82b9c5664332279767eab0c21459a839f1cdd86c`, the post-upgrade digest recorded by the dependency-remediation evidence) and by the two
  api SBOMs.

### Source-level and buildinfo cross-check

| Module | Expected | backend/go.mod | go.sum h1 | buildinfo (api) | h1 == go.sum |
|---|---|---|---|---|---|
| `github.com/go-chi/chi/v5` | 5.3.2 | `v5.3.2` | `h1:5YQkICvTCSZ25hoRsyJazN0scjzKGiu4VAUc7H1o1nY=` | `v5.3.2` | yes |
| `github.com/xuri/excelize/v2` | 2.11.0 | `v2.11.0` | `h1:HxaEFl6sRN2+8J5a8HaKq+0M4FsjBGMnWWtjOCPSG88=` | `v2.11.0` | yes |
| `github.com/jackc/pgx/v5` | 5.9.2 | `v5.9.2` | `h1:3ZhOzMWnR4yJ+RW1XImIPsD1aNSz4T4fyP7zlQb56hw=` | `v5.9.2` | yes |
| `golang.org/x/crypto` | 0.56.0 | `v0.56.0` | `h1:GUh5Ii4J5jtcseSMiRqr1jXCNHoxjeV9Fmekc2oLy6Y=` | `v0.56.0` | yes |

Both source files were read-only and unchanged during this work: `backend/go.mod` sha256 `87356daa3f6ccb99cee5478b82b9c5664332279767eab0c21459a839f1cdd86c`, `backend/go.sum` sha256 `654127a5b1ef7c599f573d8f3259235398eab330d3c1eb9f097dee1c180607cb`.

## 7. Verification status

| Claim | Status | Evidence |
|---|---|---|
| All six documents parse as valid JSON | **VERIFIED** | `validation-v2.txt` (JSON parse = OK for each) |
| Every document is bound to the binary it describes | **VERIFIED** | `metadata.component.version` == real file sha256, all three match |
| The scanned binaries did not change during the run | **VERIFIED** | `scan-stability.txt` |
| The documents were not modified after generation | **VERIFIED** | `validation-v2.json` -> sboms_unchanged_by_validator, all true |
| chi v5.3.2, excelize v2.11.0, pgx v5.9.2, x/crypto v0.56.0 present in the api SBOMs | **VERIFIED** | section 6 |
| The same four modules present in the worker/migrate SBOMs | **NOT POSSIBLE (real gap, reported)** | not linked into those binaries |
| No upgraded module appears at a wrong version | **VERIFIED** | real_gaps = 0 |
| Windows and VM copies of every new file are byte-identical | **VERIFIED** | section 8 |
| Source commit / CI provenance of the binaries | **NOT_VERIFIED** | no vcs.* build setting; VM tree is not a git repository |
| Tool image byte-identical to upstream Docker Hub | **NOT_VERIFIED** | Docker Hub unreachable; mirror digest recorded |
| Vulnerability / CVE analysis | **OUT OF SCOPE** | SBOM generation only |

## 8. Windows <-> VM byte-for-byte cross-check

Every file of this scope was written on the VM, copied to the Windows worktree
(`blockchain-platform` worktree, same relative path), and hashed on both sides.
The Windows-side hashes are in `windows-side-hashes.sha256`, whose own sha256 is
`c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66` as computed on Windows and `c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66` as computed on the VM.

| File | sha256 (identical on both sides) | VM == Windows |
|---|---|---|
| `DEFECT-RECORD-20260913-validator-passes.md` | `c94e1bdef475fa405a15beb7e0813e52dceb04d4a0935cb8f3a7db07cd532a4e` | yes |
| `api.cdx.json` | `d8cd34d0d56ba18a10eaa363fcd9fdd2c5392faffc231d0dc25f97aa3242096a` | yes |
| `api.spdx.json` | `a8e1c7eb6e7ce296735644a7eb5db530f813177efd35ba130f9691588708182c` | yes |
| `artifact-hashes.txt` | `0663f6c97cb11516c4b9f5ed3eb7012aaea4acffa69edc1113b19c2f76355117` | yes |
| `buildinfo-all-artifacts.txt` | `cd5231a58dbe9dc25156936020d52c1519f48d104f6fb585d0a1a879308bcc50` | yes |
| `buildinfo-api.txt` | `3388a5936fd47076b89ef8fa4d1fd8c2053fff29d41b213482b4fb9a4a2c2d82` | yes |
| `buildinfo-migrate.txt` | `36c3e5fbce1a7cf24f25e5907d727a9c5d185ffcf95d34639abde2532ec69980` | yes |
| `buildinfo-worker.txt` | `c56c7a9d23b30ff856f84bb8c528084930a4a98e9e8df3e75090660d09b3c3e1` | yes |
| `capture-cross-reference.sh` | `b83fdc64ae85ad5937bc503cde8f1d5b0810cc6c49da6e67de0167f1b3c59027` | yes |
| `commands.txt` | `db6d3343904ae38a5a6562a8c36e995c06ae9cd64b610d803ae02b5fed432c85` | yes |
| `cross-reference-dependency-remediation.txt` | `3f9ad7273982f2519585b42f557d452d285059f9ff3314c61e1f273a94e9a712` | yes |
| `environment-probe.txt` | `b9d3aadd37d8821bc68415d91b768efcc11c4f76271480ec74f4f382ce0fa7cd` | yes |
| `generate-sbom-dependency-upgrade-20260913T015815Z.sh` | `cc4e5812e05ca69eccb2dbd8eb30719d1b6536dbbdd7eebb73dfb00ca77cc18b` | yes |
| `generator-console.txt` | `f0079555e3a554e8d93aaf3bd0684359ada3626aabfbdf5271f9fc7500bd8236` | yes |
| `logs/api-cdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/api-spdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/migrate-cdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/migrate-spdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/module-crosscheck.err.log` | `50f5d28fcede49a481abee6c1d145e2ddd1113ba459cb52baa4bd85c2b25f389` | yes |
| `logs/syft-binary-extract.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/syft-image-save.log` | `948c1d0d8f1aeb1f58dc6eab263a1443365242f635d1d8521609afedb784ebd4` | yes |
| `logs/syft-version.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/validator.log` | `b2bf5d3699254ccb1f5603a713a51042f9bc2062d25cb34bada7138d3b981b06` | yes |
| `logs/worker-cdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `logs/worker-spdx.log` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `migrate.cdx.json` | `f4d2ae4f9440a188dec119de551657b613dedfbc99d4e0d5338a98b8d400cfec` | yes |
| `migrate.spdx.json` | `f53fbf3fa1c5998ae1ce748cc23165b637c8bccd2c6357a99cce7a28c6b708ce` | yes |
| `module-crosscheck-source.txt` | `66d904c0106c2517d0a30c81c99159335371314d51c2dd8c73f5caad34d14e66` | yes |
| `module-crosscheck-summary.txt` | `b727272df9bd44377e25a89ebad0d1ceec0dcaa4c8c9daca9dabb3ce1c6eee44` | yes |
| `module-crosscheck.txt` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | yes |
| `pilot-syft-invocation.sh` | `efd3f66f2b8cc2c5c6b89fbcea7b2811c6cb6997b1105ad637f160925f18d4c3` | yes |
| `pilot-syft-invocation.txt` | `b0a025a3715f1ae43614e24b1dd2520a0b69257a0db7487f86db22c3b6c9166e` | yes |
| `probe-environment.sh` | `46d1d1dce8d2a09e1b748358a91e40f9589c9f8944abfddca93b5c724258cac5` | yes |
| `scan-stability.txt` | `9b765b21b8acc19331c13af5fb5ba7ade7537c51e530500955a432b58198ba72` | yes |
| `syft-binary-sha256.txt` | `8575f1077f91d61f93cbbd8caa5ac30d9887e9953c8d48121312a8d35df3806a` | yes |
| `syft-image-identity.txt` | `259e3be06e1f8ba0996ceb6bc3bd103afecdcd6d9082414374cb9aaf059cddc5` | yes |
| `syft-version.txt` | `d9fb5be8078be2ac21ff48110999fdffead8ce3a2c8e55ad4412fa878e65dc2e` | yes |
| `validate-sbom-scope-dependency-upgrade-20260913T015815Z-v2.py` | `268153ca6943425f4659b0b68b50b2d749b98f12272002223eea5dd7f66dc652` | yes |
| `validate-sbom-scope-dependency-upgrade-20260913T015815Z.py` | `670fef7d38c470db6d8b8681a218e1ad1cd45327b3156bb681a28eda6139f6bc` | yes |
| `validation-v2.json` | `2b0b46db042235344c2a860d4ae9600603d4a23ae5cccfa19d83d5fd1179e3a1` | yes |
| `validation-v2.txt` | `e2f824fbfc2bcd6faf66a173953dc7d36ce974d03f701c60e90549a522dfcbb3` | yes |
| `validation.json` | `7e61b4e14e37271b9be133b19caa1fb1b868a89a89f8a521fb55db48a91c54cf` | yes |
| `validation.txt` | `f1f9c0c5dd56364d4dd0884711e0ebc500a5afeeecf11834463ec95e86965467` | yes |
| `windows-side-hashes.sha256` | `c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66` | **MISMATCH** |
| `worker.cdx.json` | `b61c3ef8ead547fe2af4453d1f7f30a3653e1365fbc2a331fa6071de2210fdf2` | yes |
| `worker.spdx.json` | `4daac5ea63d41f895c68c58aae287c37e28e0010fb01a5bca76a3fbe4b31912e` | yes |

46 files compared, 1 mismatches.

The builder script itself, `REPORT.md`, `sbom-manifest.json` and `crosscheck-local-vm.sha256` are added
to the directory after this table was built (a file cannot contain its own hash); all of
them are cross-checked in `crosscheck-local-vm.sha256`, produced by the final
verification step on the same two-sided basis.

## 9. Cross-reference to the dependency-upgrade evidence

The upgrade that this SBOM scope describes is documented at

    /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z

A read-only capture of that root (listing plus the small hash/version files; no secret file
is read) is `cross-reference-dependency-remediation.txt`. It shows the chain

- baseline source digests before the upgrade: `go.mod` 4e0f98fa4e9258cd07c1acc6f0ce5bd762cba5ba9343bcbd28b1f1a33644c36c, `go.sum` 34147fb9944ea49b8470e4c0e0da85eb53ce60706975b2d5fe34c2011ee4b6b2;
- post-upgrade digests: `go.mod` `87356daa3f6ccb99cee5478b82b9c5664332279767eab0c21459a839f1cdd86c`, `go.sum` `654127a5b1ef7c599f573d8f3259235398eab330d3c1eb9f097dee1c180607cb`;
- and a live re-hash performed for this report, which still returns the post-upgrade digests.

Honest note: that reference root exists **only on the acceptance VM**. It is not present in
the Windows git worktree, so the Windows copy of this scope carries the cross-reference as a
path plus this capture, not as a resolvable sibling directory.

## 10. Limitations (explicit)

- Scan target kind: a FILE (compiled Go binary). syft reads the module table embedded in the binary, so the inventory is the set of modules LINKED into that binary - not the full go.mod/go.sum closure. The three binaries differ: api 53, worker 42, migrate 30 embedded modules.
- Consequence: modules that are in go.mod but not linked into worker or migrate (here go-chi/chi v5.3.2 and xuri/excelize v2.11.0) cannot appear in those two binaries' SBOMs. Four upgraded-module checks therefore report NOT-LINKED for worker/migrate.
- No VCS metadata is embedded: 'go version -m' shows no vcs.* build setting and the main module version is '(devel)', because the source tree on the VM is not a git repository. Build provenance cannot be tied to a commit hash from the artifacts.
- Module versions come from the binary's own buildinfo (stamped at link time) and were cross-checked against backend/go.mod and backend/go.sum: every linked upgraded module carries the same h1: hash as the go.sum entry for that version. That cross-check is evidence of agreement, not proof that the binary was built from exactly this tree.
- Licences: syft does not resolve licence files for Go modules on a binary target, so the library components carry no licence field and SPDX reports NOASSERTION (55/44/32). This matches the pre-existing sbom/ behaviour and is a tool-capability limit, not an absence of licences in the dependencies.
- No vulnerability analysis is in scope here (SBOM generation only).
- The container image could not be byte-compared with upstream Docker Hub (unreachable); the mirror digest is recorded instead. The syft executable sha256 is the identity anchor for the tool actually executed.
- The cross-reference evidence root dependency-remediation-20260913T014342Z exists only on the acceptance VM (not in the Windows worktree), so the Windows-side copy of this scope cannot resolve that path.

## 11. Defects found and corrected inside this scope

Two defects in my own validator code (not in the SBOMs) are recorded in full, with verbatim
error output, in `DEFECT-RECORD-20260913-validator-passes.md`:

- pass 0 (validator embedded in the generation script) crashed with `TypeError: expected str, bytes or os.PathLike object, not dict` and left a 0-byte `module-crosscheck.txt`;
- pass 1 (`validate-sbom-scope-dependency-upgrade-20260913T015815Z.py`) wrote a complete
  but partly misleading `validation.txt` (its go.mod/go.sum version lookups missed the
  `v` prefix and produced false `h1_match_go_sum=False` rows), then crashed in
  `json.dump(sort_keys=True)` and left a truncated `validation.json`.

Both superseded outputs are preserved unmodified; the corrected results are in
`validation-v2.txt` / `validation-v2.json` (pass 2, `validation_result = PASS`, `real_gaps = 0`).
The six SBOM documents were re-hashed in pass 2 and are unchanged.

## 12. File inventory

| File | Size (bytes) | sha256 |
|---|---|---|
| `DEFECT-RECORD-20260913-validator-passes.md` | 4511 | `c94e1bdef475fa405a15beb7e0813e52dceb04d4a0935cb8f3a7db07cd532a4e` |
| `api.cdx.json` | 60083 | `d8cd34d0d56ba18a10eaa363fcd9fdd2c5392faffc231d0dc25f97aa3242096a` |
| `api.spdx.json` | 86474 | `a8e1c7eb6e7ce296735644a7eb5db530f813177efd35ba130f9691588708182c` |
| `artifact-hashes.txt` | 1743 | `0663f6c97cb11516c4b9f5ed3eb7012aaea4acffa69edc1113b19c2f76355117` |
| `buildinfo-all-artifacts.txt` | 14394 | `cd5231a58dbe9dc25156936020d52c1519f48d104f6fb585d0a1a879308bcc50` |
| `buildinfo-api.txt` | 5251 | `3388a5936fd47076b89ef8fa4d1fd8c2053fff29d41b213482b4fb9a4a2c2d82` |
| `buildinfo-migrate.txt` | 3236 | `36c3e5fbce1a7cf24f25e5907d727a9c5d185ffcf95d34639abde2532ec69980` |
| `buildinfo-worker.txt` | 4260 | `c56c7a9d23b30ff856f84bb8c528084930a4a98e9e8df3e75090660d09b3c3e1` |
| `capture-cross-reference.sh` | 2105 | `b83fdc64ae85ad5937bc503cde8f1d5b0810cc6c49da6e67de0167f1b3c59027` |
| `commands.txt` | 7487 | `db6d3343904ae38a5a6562a8c36e995c06ae9cd64b610d803ae02b5fed432c85` |
| `cross-reference-dependency-remediation.txt` | 5746 | `3f9ad7273982f2519585b42f557d452d285059f9ff3314c61e1f273a94e9a712` |
| `environment-probe.txt` | 26249 | `b9d3aadd37d8821bc68415d91b768efcc11c4f76271480ec74f4f382ce0fa7cd` |
| `generate-sbom-dependency-upgrade-20260913T015815Z.sh` | 24389 | `cc4e5812e05ca69eccb2dbd8eb30719d1b6536dbbdd7eebb73dfb00ca77cc18b` |
| `generator-console.txt` | 8937 | `f0079555e3a554e8d93aaf3bd0684359ada3626aabfbdf5271f9fc7500bd8236` |
| `logs/api-cdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/api-spdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/migrate-cdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/migrate-spdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/module-crosscheck.err.log` | 292 | `50f5d28fcede49a481abee6c1d145e2ddd1113ba459cb52baa4bd85c2b25f389` |
| `logs/syft-binary-extract.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/syft-image-save.log` | 229 | `948c1d0d8f1aeb1f58dc6eab263a1443365242f635d1d8521609afedb784ebd4` |
| `logs/syft-version.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/validator.log` | 176 | `b2bf5d3699254ccb1f5603a713a51042f9bc2062d25cb34bada7138d3b981b06` |
| `logs/worker-cdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `logs/worker-spdx.log` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `migrate.cdx.json` | 34611 | `f4d2ae4f9440a188dec119de551657b613dedfbc99d4e0d5338a98b8d400cfec` |
| `migrate.spdx.json` | 49939 | `f53fbf3fa1c5998ae1ce748cc23165b637c8bccd2c6357a99cce7a28c6b708ce` |
| `module-crosscheck-source.txt` | 2080 | `66d904c0106c2517d0a30c81c99159335371314d51c2dd8c73f5caad34d14e66` |
| `module-crosscheck-summary.txt` | 2138 | `b727272df9bd44377e25a89ebad0d1ceec0dcaa4c8c9daca9dabb3ce1c6eee44` |
| `module-crosscheck.txt` | 0 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` |
| `pilot-syft-invocation.sh` | 2865 | `efd3f66f2b8cc2c5c6b89fbcea7b2811c6cb6997b1105ad637f160925f18d4c3` |
| `pilot-syft-invocation.txt` | 4619 | `b0a025a3715f1ae43614e24b1dd2520a0b69257a0db7487f86db22c3b6c9166e` |
| `probe-environment.sh` | 2953 | `46d1d1dce8d2a09e1b748358a91e40f9589c9f8944abfddca93b5c724258cac5` |
| `scan-stability.txt` | 838 | `9b765b21b8acc19331c13af5fb5ba7ade7537c51e530500955a432b58198ba72` |
| `syft-binary-sha256.txt` | 572 | `8575f1077f91d61f93cbbd8caa5ac30d9887e9953c8d48121312a8d35df3806a` |
| `syft-image-identity.txt` | 401 | `259e3be06e1f8ba0996ceb6bc3bd103afecdcd6d9082414374cb9aaf059cddc5` |
| `syft-version.txt` | 250 | `d9fb5be8078be2ac21ff48110999fdffead8ce3a2c8e55ad4412fa878e65dc2e` |
| `validate-sbom-scope-dependency-upgrade-20260913T015815Z-v2.py` | 26305 | `268153ca6943425f4659b0b68b50b2d749b98f12272002223eea5dd7f66dc652` |
| `validate-sbom-scope-dependency-upgrade-20260913T015815Z.py` | 22859 | `670fef7d38c470db6d8b8681a218e1ad1cd45327b3156bb681a28eda6139f6bc` |
| `validation-v2.json` | 27363 | `2b0b46db042235344c2a860d4ae9600603d4a23ae5cccfa19d83d5fd1179e3a1` |
| `validation-v2.txt` | 15536 | `e2f824fbfc2bcd6faf66a173953dc7d36ce974d03f701c60e90549a522dfcbb3` |
| `validation.json` | 2045 | `7e61b4e14e37271b9be133b19caa1fb1b868a89a89f8a521fb55db48a91c54cf` |
| `validation.txt` | 14884 | `f1f9c0c5dd56364d4dd0884711e0ebc500a5afeeecf11834463ec95e86965467` |
| `windows-side-hashes.sha256` | 4112 | `c46f97a4fbd118d396c253140abcf6572c3ea62a0c75a119f40c32f752811a66` |
| `worker.cdx.json` | 47752 | `b61c3ef8ead547fe2af4453d1f7f30a3653e1365fbc2a331fa6071de2210fdf2` |
| `worker.spdx.json` | 68963 | `4daac5ea63d41f895c68c58aae287c37e28e0010fb01a5bca76a3fbe4b31912e` |

(this builder script, `REPORT.md`, `sbom-manifest.json` and `crosscheck-local-vm.sha256` are inventoried in `crosscheck-local-vm.sha256`.)
