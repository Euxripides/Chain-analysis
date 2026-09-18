import json, importlib.util
base = r"E:\Code\远程虚拟机\harness\blockchain-platform\scripts\s7\"
spec = importlib.util.spec_from_file_location("p", base + "p4_route_matrix_v2_pairs.py")
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
orig = json.load(open(base + "_tmp_pairs.json", encoding="utf-8"))
ok = len(m.PAIRS) == len(orig) and all(list(a) == list(b) for a, b in zip(m.PAIRS, orig))
print("ROUNDTRIP_OK=%s pairs=%d" % (ok, len(m.PAIRS)))
