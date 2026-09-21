import json, sys


def die(msg):
    sys.stderr.write('SCHEMA_ERROR: %s\n' % msg)
    sys.exit(3)


def load(path):
    try:
        with open(path) as fh:
            return json.load(fh)
    except Exception as exc:
        die('cannot read %s: %s' % (path, exc))


def failures(doc):
    if not isinstance(doc, dict):
        die('report is not a JSON object')
    if 'failures' not in doc:
        die('failures key missing')
    val = doc['failures']
    if val is None:
        # The migrator serialises an empty failure set as null (Failures []string nil).
        # Treat null as an EXPLICIT EMPTY failure set -- never as "unknown".
        return []
    if not isinstance(val, list):
        die('failures must be a list or null, got %s' % type(val).__name__)
    return val


def main():
    if len(sys.argv) < 2:
        die('usage: rep.py <failures|get|map|allverified|selftest> <report.json> [arg]')
    cmd = sys.argv[1]
    # selftest takes no report path: dispatch it before the report-path guard.
    # v2 round 20260913T150751Z failed here (rc 3) because the guard ran first; the
    # harness reported it as FAIL P6 and refused to touch any bucket -- preserved.
    if cmd == 'selftest':
        return selftest()
    if len(sys.argv) < 3:
        die('usage: rep.py <failures|get|map|allverified> <report.json> [arg]')
    if cmd == 'failures':
        print(len(failures(load(sys.argv[2]))))
        return 0
    if cmd == 'get':
        cur = load(sys.argv[2])
        for part in sys.argv[3].split('.'):
            if not isinstance(cur, dict) or part not in cur:
                die('missing path %s' % sys.argv[3])
            cur = cur[part]
        print(json.dumps(cur, sort_keys=True))
        return 0
    if cmd == 'map':
        doc = load(sys.argv[2])
        val = doc.get(sys.argv[3]) if isinstance(doc, dict) else None
        if not isinstance(val, dict):
            die('map %s missing or not an object' % sys.argv[3])
        print(','.join('%s=%s' % (k, val[k]) for k in sorted(val)))
        return 0
    if cmd == 'allverified':
        doc = load(sys.argv[2])
        fails = failures(doc)
        by = doc.get('by_status') or {}
        total = doc.get('total')
        ok = len(fails) == 0 and (by.get('VERIFIED', 0) + by.get('ALREADY_VERIFIED', 0)) == total
        print('true' if ok else 'false')
        return 0
    die('unknown command %s' % cmd)


def selftest():
    import os
    import tempfile
    tmp = os.environ['REP_TMPDIR']
    cases = [
        ('null-is-empty', {'failures': None}, 0, True),
        ('empty-list-is-empty', {'failures': []}, 0, True),
        ('nonempty-detected', {'failures': ['x']}, 1, True),
        ('missing-key-is-schema-error', {'total': 1}, None, False),
        ('wrong-type-is-schema-error', {'failures': 'abc'}, None, False),
    ]
    bad = 0
    for name, doc, want, ok_case in cases:
        path = os.path.join(tmp, 'selftest-%s.json' % name)
        with open(path, 'w') as fh:
            json.dump(doc, fh)
        try:
            got = len(failures(load(path)))
            rc = 0
        except SystemExit as exc:
            got = None
            rc = exc.code if isinstance(exc.code, int) else 1
        if ok_case:
            good = rc == 0 and got == want
        else:
            good = rc != 0
        print('PARSER_%s %s' % (name, 'PASS' if good else 'FAIL'))
        if not good:
            bad += 1
    return 1 if bad else 0


sys.exit(main())
