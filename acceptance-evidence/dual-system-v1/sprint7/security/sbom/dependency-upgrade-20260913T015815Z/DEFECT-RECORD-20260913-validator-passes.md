# DEFECT RECORD - scoped SBOM validator passes (2026-09-13 dependency-upgrade scope)

Scope of this record: the **validator** steps of the scoped SBOM task for
`acceptance-evidence/dual-system-v1/sprint7/security/sbom/dependency-upgrade-20260913T015815Z/`.
It documents two defects that I introduced in my own validation code, found and corrected
during this work. Nothing here affects the six SBOM documents themselves.

Every superseded artifact is **preserved byte-for-byte**; nothing was deleted, moved,
truncated or overwritten. Never edited an already-written file: each correction is a new
file with a new name.

## D1 - validation pass 0 crashed (embedded in the generator)

- Where: the inline `python3 -` validator inside
  `generate-sbom-dependency-upgrade-20260913T015815Z.sh`.
- Symptom (verbatim, `logs/validator.log`):

      Traceback (most recent call last):
        File "<stdin>", line 146, in <module>
        File "<stdin>", line 20, in sha256f
      TypeError: expected str, bytes or os.PathLike object, not dict

- Cause: one local name (`p`) was used both for the SBOM file path and for the SPDX
  package loop variable, so `sha256f()` was handed a package dictionary.
- Effect: `validation.txt` and `module-crosscheck.json` were never produced;
  `module-crosscheck.txt` was left behind as a 0-byte file; `commands.txt` records the
  failure (`EXIT_CODE: 1` plus the `tail: cannot open ... validation.txt` line).
- Correction: a separate, standalone validator was written as a **new** file
  (`validate-sbom-scope-dependency-upgrade-20260913T015815Z.py`, pass 1).
- Preservation: the 0-byte `module-crosscheck.txt` and the failure record in
  `commands.txt` are kept as-is.

## D2 - validation pass 1 produced a misleading cross-check and crashed while serialising

- Where: `validate-sbom-scope-dependency-upgrade-20260913T015815Z.py` (pass 1).
- Symptom A (verbatim, from the run console):

      File ".../validate-sbom-scope-dependency-upgrade-20260913T015815Z.py", line 427, in main
        json.dump(result, handle, indent=2, sort_keys=True)
      TypeError: '<' not supported between instances of 'str' and 'NoneType'

  Cause: `primaryPackagePurpose` and licence counter maps legitimately contain a `None`
  key (SPDX leaves the field unset for ordinary Go packages), and `sort_keys=True`
  cannot order `None` against `"FILE"`.
- Symptom B (in the written `validation.txt`): the go.mod / go.sum / buildinfo
  cross-check printed

      go.sum h1     github.com/go-chi/chi/v5@5.3.2 = None
      github.com/go-chi/chi/v5       expected=5.3.2 linked=True ... h1_match_go_sum=False

  for modules that are in fact correct. Cause: the expected version strings are stored
  without the `v` prefix (`5.3.2`) while go.mod/go.sum/buildinfo all carry it
  (`v5.3.2`), so every lookup missed. **These rows in `validation.txt` are a false
  negative of the checker, not a finding about the binaries.**
- Effect: pass 1 wrote a complete but partly misleading `validation.txt` (183 lines),
  then crashed, leaving a truncated `validation.json` (2045 bytes, not valid JSON) and no
  `module-crosscheck-summary.txt`.
- Correction: `validate-sbom-scope-dependency-upgrade-20260913T015815Z-v2.py` (pass 2,
  new file, new outputs `validation-v2.txt` / `validation-v2.json` /
  `module-crosscheck-summary.txt`). It normalises `v` prefixes for every lookup,
  stringifies counter keys before sorting, and classifies each missing module as
  `REAL GAP` (present at a different version) or `NOT-LINKED` (not part of that binary).
  It also re-hashes the six SBOM documents and proves they are unchanged since generation.
- Pass-2 result: `validation_result = PASS`, `real_gaps = 0`, `not_linked = 8`.
- Preservation: `validation.txt` and the truncated `validation.json` from pass 1 are kept
  unmodified as the record of that pass. Read `validation-v2.txt` for the corrected
  go.mod/go.sum h1 cross-check.

## Impact on the deliverables

None on the SBOM content:

- all six documents were produced by syft **before** any validation ran, and
  `validation-v2.txt` re-hashes them and confirms each sha256 is still identical to the
  value recorded at generation time in `artifact-hashes.txt`
  (`sboms_unchanged_by_validator` in `validation-v2.json` is `true` for all six);
- the two defects are confined to validator bookkeeping.

Written after the fact from the actual console output preserved in
`logs/validator.log`, `logs/module-crosscheck.err.log`, `generator-console.txt` and
`commands.txt`.
