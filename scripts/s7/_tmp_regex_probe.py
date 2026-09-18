import re, importlib.util
spec = importlib.util.spec_from_file_location("scan", r"E:\Code\远程虚拟机\harness\blockchain-platform\scripts\s7\p4_nested_query_scan.py")
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
cases = ["\tq := r.URL.Query()", "\t\tq := r.URL.Query()", "\trows, err := s.deps.Store.Pool.Query(r.Context(),"]
for s in cases:
    a = m.OPEN_ASSIGN.match(s); b = m.OPEN_ASSIGN_BARE.match(s)
    print(repr(s), "OPEN=", a.groups() if a else None, "BARE=", b.groups() if b else None)