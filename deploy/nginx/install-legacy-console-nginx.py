#!/usr/bin/env python3
"""Install the LEGACY-CONSOLE-01 entry wiring for nginx (run with sudo).

Exactly-once anchors everywhere: a silently unapplied or doubly applied edit is
impossible. The token is read from deploy/legacy-console.env and never printed.
"""
import os
import re
import shutil
import sys
import time

REPO = "/home/euripides/projects/blockchain-platform"
TOKEN_FILE = os.path.join(REPO, "deploy", "legacy-console.env")
CONF = "/etc/nginx/sites-enabled/blockchain-platform.conf"
INCLUDE = "/etc/nginx/conf.d/legacy-console.conf"
TEMPLATE = "/tmp/legacy-console.nginx.conf"
BACKUP_DIR = "/home/euripides/legacy-console-backups"
ANCHOR = "    location ^~ /api/ {\n"
SET_LINE = "        proxy_set_header X-Legacy-Console-Token $legacy_console_value;\n"


def read_token():
    for line in open(TOKEN_FILE):
        line = line.strip()
        if line.startswith("LEGACY_CONSOLE_TOKEN="):
            token = line.split("=", 1)[1].strip()
            if len(token) < 32:
                raise SystemExit("TOKEN_TOO_SHORT")
            return token
    raise SystemExit("TOKEN_NOT_FOUND")


def main():
    token = read_token()
    os.makedirs(BACKUP_DIR, exist_ok=True)
    stamp = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())

    template = open(TEMPLATE).read()
    if template.count("__LEGACY_CONSOLE_TOKEN__") != 1:
        raise SystemExit("TEMPLATE_TOKEN_PLACEHOLDER_NOT_EXACTLY_ONCE")
    rendered = template.replace("__LEGACY_CONSOLE_TOKEN__", token)
    if os.path.exists(INCLUDE):
        shutil.copy2(INCLUDE, os.path.join(BACKUP_DIR, "legacy-console.conf.%s" % stamp))
    with open(INCLUDE, "w") as handle:
        handle.write(rendered)
    os.chmod(INCLUDE, 0o600)
    os.chown(INCLUDE, 0, 0)
    print("INCLUDE_WRITTEN=%s mode=%o" % (INCLUDE, os.stat(INCLUDE).st_mode & 0o777))

    src = open(CONF).read()
    if "X-Legacy-Console-Token" in src:
        print("SITES_CONF_ALREADY_PATCHED")
        return 0
    if src.count(ANCHOR) != 1:
        print("ANCHOR_NOT_EXACTLY_ONCE=%d" % src.count(ANCHOR))
        return 2
    shutil.copy2(CONF, os.path.join(BACKUP_DIR, "blockchain-platform.conf.%s" % stamp))
    with open(CONF, "w") as handle:
        handle.write(src.replace(ANCHOR, ANCHOR + SET_LINE, 1))
    print("SITES_CONF_PATCHED backup=%s" % os.path.join(BACKUP_DIR, "blockchain-platform.conf.%s" % stamp))
    return 0


if __name__ == "__main__":
    sys.exit(main())
