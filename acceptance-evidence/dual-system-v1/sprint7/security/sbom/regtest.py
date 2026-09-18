import json, urllib.request, ssl, socket, sys

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE
socket.setdefaulttimeout(20)

REPOS = ["anchore/syft", "aquasec/trivy"]
HOSTS = [
    ("registry-1.docker.io", "direct-dockerhub"),
    ("docker.m.daocloud.io", "mirror-daocloud"),
    ("docker.1ms.run", "mirror-1ms"),
    ("docker.1panel.live", "mirror-1panel"),
    ("docker.rainbond.cc", "mirror-rainbond"),
    ("docker.nju.edu.cn", "mirror-nju"),
    ("dockerproxy.net", "mirror-dockerproxy"),
    ("hub.rat.dev", "mirror-rat"),
]

def fetch(url, headers=None):
    req = urllib.request.Request(url, headers=headers or {})
    try:
        with urllib.request.urlopen(req, context=ctx) as r:
            return r.status, r.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()[:300]
    except Exception as e:
        return -1, repr(e).encode()

MAN_ACCEPT = ", ".join([
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.docker.distribution.manifest.v2+json",
])

for repo in REPOS:
    print("=" * 72)
    print("REPO: " + repo)
    for host, label in HOSTS:
        st, body = fetch("https://%s/v2/" % host)
        if st == 401:
            # parse WWW-Authenticate
            req = urllib.request.Request("https://%s/v2/" % host)
            try:
                urllib.request.urlopen(req, context=ctx)
            except urllib.error.HTTPError as e:
                wa = e.headers.get("WWW-Authenticate", "")
            except Exception as e:
                wa = ""
            realm = ""
            service = ""
            for part in wa.replace("Bearer ", "").split(","):
                part = part.strip()
                if part.startswith("realm="):
                    realm = part.split("=", 1)[1].strip('"')
                if part.startswith("service="):
                    service = part.split("=", 1)[1].strip('"')
            tok = ""
            if realm:
                q = "%s?service=%s&scope=repository:%s:pull" % (realm, service, repo)
                ts, tb = fetch(q)
                if ts == 200:
                    try:
                        tok = json.loads(tb.decode()).get("token") or json.loads(tb.decode()).get("access_token") or ""
                    except Exception:
                        tok = ""
            h = {"Accept": MAN_ACCEPT}
            if tok:
                h["Authorization"] = "Bearer " + tok
            ms, mb = fetch("https://%s/v2/%s/manifests/latest" % (host, repo), h)
            note = ""
            if ms == 200:
                try:
                    d = json.loads(mb.decode())
                    if "manifests" in d:
                        note = "index_platforms=%d" % len(d["manifests"])
                    elif "layers" in d:
                        note = "layers=%d total_compressed=%d" % (
                            len(d["layers"]), sum(l.get("size", 0) for l in d["layers"]))
                except Exception as e:
                    note = "parse_err " + repr(e)
            print("  %-22s /v2/=%s token=%s manifest=%s %s" % (label, st, "yes" if tok else "no", ms, note))
        else:
            print("  %-22s /v2/=%s body=%s" % (label, st, body[:80]))
