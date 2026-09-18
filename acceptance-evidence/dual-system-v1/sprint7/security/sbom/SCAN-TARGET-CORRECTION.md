# S7-SEC-01 correction record: backend scan target and syft digest semantics

Author: S7-SEC-01 SBOM executor (subagent)
Time: 2026-09-12T17:00Z

Supersedes the technical explanation in `SCAN-TARGET-STABILITY-REVIEW-NOTE.md` (left by the parent
agent at ~17:05Z). That note's conclusion - the scan target was stable - is correct, but its stated
reason is wrong, and its statement of which build the artifacts describe is now outdated.

## 1. What was actually wrong: the scan target path, not syft's digest semantics

The first pass ran exactly the command in the task brief:

    sudo -n docker run --rm -v <project>:/w -w /w <syft> bin/api -o cyclonedx-json=.../backend.cdx.json

With -w /w the relative file target "bin/api" resolves to /w/bin/api = <project>/bin/api,
NOT <project>/backend/bin/api. Those are two different files:

| Path | size | sha256 | mtime |
|---|---|---|---|
| <project>/bin/api  (what the first pass scanned) | 50347852 | 33440571c848795bc787d437d32ec07f6c3621f57d44adddd5be4f440dccf5ad | 2026-09-08 22:37 |
| <project>/backend/bin/api  (the deliverable) | 55518192 | bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142 | 2026-09-13 00:00:03 |

## 2. Measured syft semantics (control experiments, not assumptions)

* Control: a plain 23-byte text file scanned by the same syft image produced
  metadata.component.version = sha256:97e13733e97d8615239a6fe42c9058bec1bec4e8b72dfc3c14caf5d100d99f23,
  which is byte-for-byte that file's own sha256.
  (logs/probe-plainfile.log, logs/digest-semantics-probe.log)
* The first pass wrote sha256:33440571c848..., exactly the sha256 of the stale <project>/bin/api
  it had been pointed at.
* The corrected pass (target backend/bin/api) wrote
  sha256:bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142, exactly the deliverable's sha256.

Therefore metadata.component.version IS the true sha256 of the scanned file, including for Go
binaries. The claim in the review note that it is "a Go build-info derived digest that will never
equal the file sha256" is disproven by these three measurements. Hashing the Go build ID string in
four ways (raw, newline, action id, content id) reproduces the observed value in none of them.

Consequently the earlier STABILITY_VERDICT=no had a different root cause than "wrong metric":
the metric was right, and it was correctly reporting that the file being compared against was not
the file that had been scanned.

## 3. Which build the final artifacts describe

backend/bin/api is being rebuilt by the concurrent deploy loop. Observed sequence:

    23:28:04 local : 753ba724335e4be4231d40ca65809f22403eb3323b79f144b1161f88a6f21c78  (55370073 bytes)
    23:54:22 local : 6106d8e4b09ed881864c9bc9212b29a45b698dc78825dcf936d317b6ed890a10  (55370073 bytes)
    00:00:03 local : bae72e0a5063643866d5fe2e259ba4239d8f6ca94ab0191b7c59496fcc040142  (55518192 bytes)  <-- FINAL SBOMs describe THIS build

The final backend SBOMs were generated at 17:00:19Z with the file hash verified identical immediately
before and after the syft run, and syft's recorded version equals that hash
(see backend-scan-stability.txt).

## 4. Action taken

* Backend SBOMs regenerated with the correct target backend/bin/api.
* Superseded wrong-target outputs preserved in discarded-wrong-target/ with a README.
* Any consumer must re-verify "sha256sum backend/bin/api" against metadata.component.version in
  backend.cdx.json before treating the SBOM as describing the running build, because the deploy
  loop can rebuild the binary at any time.
