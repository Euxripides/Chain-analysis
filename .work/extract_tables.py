import re, subprocess, os, json, collections
root = r"E:\Code\远程虚拟机\harness\blockchain-platform\backend"
pat = re.compile(r"(?:FROM|JOIN|INTO|UPDATE)\s+([a-z_][a-z0-9_.]*)", re.I)
skip = {"select","values","json_build_object","generate_series","unnest","lateral","each"}
found = collections.defaultdict(set)
for dirpath, _, files in os.walk(root):
    if "node_modules" in dirpath: continue
    for fn in files:
        if not fn.endswith(".go"): continue
        p = os.path.join(dirpath, fn)
        try: txt = open(p, encoding="utf-8", errors="replace").read()
        except Exception: continue
        for m in pat.finditer(txt):
            t = m.group(1).lower().rstrip(",);")
            if t in skip: continue
            found[t].add(os.path.relpath(p, root))
print(json.dumps(sorted(found.keys())))
