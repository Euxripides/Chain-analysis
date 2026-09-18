import json, hashlib, os, sys

SB = "/home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/security/sbom"
BIN = "/home/euripides/projects/blockchain-platform/backend/bin/api"

print("=== TEST A: does the SPDX document carry the REAL file sha256 for the api package? ===")
real = hashlib.sha256(open(BIN, "rb").read()).hexdigest()
print("real file sha256 (python, full read) = " + real)
d = json.load(open(os.path.join(SB, "backend.spdx.json")))
found = 0
for p in d.get("packages", []):
    nm = p.get("name") or ""
    cs = p.get("checksums") or []
    if nm == "api" or nm.lower().endswith("/api") or cs:
        found += 1
        if found <= 6:
            print("  pkg=" + nm + " versionInfo=" + str(p.get("versionInfo")) + " checksums=" + json.dumps(cs))
print("packages carrying checksums: " + str(found))
matched = False
for p in d.get("packages", []):
    for c in (p.get("checksums") or []):
        if c.get("algorithm") == "SHA256":
            if (c.get("checksumValue") or "").lower() == real:
                matched = True
                print("  -> SPDX package " + str(p.get("name")) + " SHA256 checksum EQUALS the real file sha256")
print("SPDX_HAS_REAL_FILE_SHA=" + ("yes" if matched else "no"))

print()
print("=== TEST B: syft metadata.component.version for a NON-Go plain text file ===")
pp = "/tmp/s7sec-probe/plain.txt"
if os.path.exists(pp):
    data = open(pp, "rb").read()
    print("plain.txt sha256 = " + hashlib.sha256(data).hexdigest() + "  bytes=" + str(len(data)))
    pc = "/tmp/s7sec-probe/probe.cdx.json"
    if os.path.exists(pc):
        pd = json.load(open(pc))
        mc = (pd.get("metadata") or {}).get("component") or {}
        print("syft metadata.component = " + json.dumps(mc))
        v = (mc.get("version") or "").replace("sha256:", "")
        print("SYFT_FIELD_EQUALS_FILE_SHA=" + ("yes" if v == hashlib.sha256(data).hexdigest() else "no"))
    else:
        print("probe.cdx.json missing")
else:
    print("plain.txt missing")

print()
print("=== TEST C: is the CDX version field a hash of the Go build id string? ===")
bid = sys.argv[1] if len(sys.argv) > 1 else ""
cd = json.load(open(os.path.join(SB, "backend.cdx.json")))
cdxv = ((cd.get("metadata") or {}).get("component") or {}).get("version", "")
print("cdx metadata.component.version = " + cdxv)
print("current go build id (from file(1)) = " + bid)
cands = {}
if bid:
    for label, s in (("buildid_raw", bid), ("buildid_nl", bid + "\n")):
        cands[label] = hashlib.sha256(s.encode()).hexdigest()
    parts = bid.split("/")
    if len(parts) == 4:
        cands["buildid_actionid"] = hashlib.sha256(parts[1].encode()).hexdigest()
        cands["buildid_contentid"] = hashlib.sha256(parts[2].encode()).hexdigest()
for k, v in cands.items():
    print("  candidate " + k + " = " + v + ("   <== MATCHES" if ("sha256:" + v) == cdxv else ""))
