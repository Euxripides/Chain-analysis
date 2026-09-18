#!/usr/bin/env python3
"""Second, small patch step for the P4 matrix runner v2.

The v2 pair set changed route 36 to send an UNQUOTED placeholder for the provider
priority so the JSON body would carry a number. A route body is a Python literal,
so the unquoted placeholder is evaluated at import time and the runner dies with
NameError before it starts. This step restores the quoted placeholder and instead
teaches fill_body to substitute JSON-native values for placeholders that occupy a
whole string, which is the general fix (a numeric field cannot be carried as a
Python literal, only as data).

v1 is still never touched; this transforms the v2 output of
p4_route_matrix_v2_patch.py in place, and every replacement must match once.
"""
import sys

R = "/home/euripides/projects/blockchain-platform"
DST = R + "/scripts/s7/p4_route_matrix_gate_v2.py"

OLD_ROUTE = ('body={"reason": "p4 matrix probe", "priority": {provider_priority}}, mode="ab", restore="provider",')
NEW_ROUTE = ('body={"reason": "p4 matrix probe", "priority": "{provider_priority}"}, mode="ab", restore="provider",')

OLD_FILL = '''def fill_body(body, params):
    if not isinstance(body, dict):
        return body
    raw = json.dumps(body)
    for k, v in params.items():
        raw = raw.replace("{" + k + "}", str(v))
    return json.loads(raw)'''

NEW_FILL = '''def fill_body(body, params):
    """Substitute placeholders, keeping JSON-native types.

    A placeholder that occupies a WHOLE string becomes the JSON encoding of the
    value, so a numeric field really is a number: v1 sent
    "priority": "{provider_priority}" and the API answered 400 because a JSON
    string arrived where an integer was required. A placeholder inside a longer
    string keeps the plain textual substitution.
    """
    if not isinstance(body, dict):
        return body
    raw = json.dumps(body)
    for k, v in params.items():
        raw = raw.replace(chr(34) + "{" + k + "}" + chr(34), json.dumps(v))
        raw = raw.replace("{" + k + "}", str(v))
    return json.loads(raw)'''


def main():
    with open(DST, encoding="utf-8") as fh:
        out = fh.read()
    for idx, (old, new) in enumerate(((OLD_ROUTE, NEW_ROUTE), (OLD_FILL, NEW_FILL))):
        n = out.count(old)
        if n != 1:
            print("STEP_%d_COUNT=%d expected exactly 1" % (idx, n))
            return 2
        out = out.replace(old, new)
    with open(DST, "w", encoding="utf-8", newline=chr(10)) as fh:
        fh.write(out)
    print("PATCHED_V2B_OK bytes=%d" % len(out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
