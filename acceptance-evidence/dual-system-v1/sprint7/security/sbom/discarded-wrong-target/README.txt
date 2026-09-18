SUPERSEDED ARTIFACTS - WRONG SCAN TARGET - DO NOT USE AS THE S7-SEC-01 DELIVERABLE

These two files were produced with the command form:

  sudo -n docker run --rm -v /home/euripides/projects/blockchain-platform:/w -w /w \
       docker.1ms.run/anchore/syft:latest bin/api -o cyclonedx-json=.../backend.cdx.json

With -w /w the file target "bin/api" resolves to /w/bin/api, i.e.
/home/euripides/projects/blockchain-platform/bin/api -- a STALE build dated 2026-09-08 22:37,
size 50,347,852 bytes, sha256 33440571c848795bc787d437d32ec07f6c3621f57d44adddd5be4f440dccf5ad.

The deployable deliverable is backend/bin/api (size 55,370,073 bytes,
sha256 6106d8e4b09ed881864c9bc9212b29a45b698dc78825dcf936d317b6ed890a10, built 2026-09-12 23:54 local).

The authoritative artifacts were regenerated with the target "backend/bin/api" and now live at
../backend.cdx.json and ../backend.spdx.json.

Evidence that syft's metadata.component.version IS the true sha256 of the scanned file:
these files carry version sha256:33440571c848... which equals the real sha256 of the stale
<project>/bin/api, and a control run on a plain 23-byte file returned that file's own sha256.
