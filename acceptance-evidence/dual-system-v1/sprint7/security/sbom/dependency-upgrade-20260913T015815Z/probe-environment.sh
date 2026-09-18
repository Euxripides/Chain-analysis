#!/usr/bin/env bash
# S7-SEC-01 follow-up: read-only environment probe for the 2026-09-13 dependency-upgrade SBOM scope.
# ADDITIVE / READ-ONLY: performs no writes outside stdout, runs no sudo, touches no service.
set -u
PROJ=/home/euripides/projects/blockchain-platform
echo "=== HOST ==="
hostname
uname -a
date -u '+utc=%Y-%m-%dT%H:%M:%SZ'
date '+local=%Y-%m-%dT%H:%M:%S%z'
echo
echo "=== IDENTITY (no secrets) ==="
id
echo "HOME=$HOME"
echo
echo "=== PROJECT ==="
ls -ld "$PROJ"
cd "$PROJ" || exit 1
echo "git_toplevel=$(git rev-parse --show-toplevel 2>&1 | head -1)"
echo
echo "=== BIN DIR LISTING (read-only) ==="
ls -la "$PROJ/backend/bin/" 2>&1 | head -80
echo
echo "=== TARGET BINARIES ==="
for b in api worker migrate; do
  p="$PROJ/backend/bin/$b"
  if [ -f "$p" ]; then
    stat -c 'path=%n size=%s mode=%a mtime=%y' "$p"
    sha256sum "$p"
  else
    echo "MISSING: $p"
  fi
done
echo
echo "=== PROCESS EXE IDENTITY ==="
found=0
for pid in $(pgrep -f "$PROJ/backend/bin/" 2>/dev/null); do
  found=1
  echo "pid=$pid cmd=$(tr '\0' ' ' < /proc/$pid/cmdline 2>/dev/null | head -c 200)"
  echo "pid=$pid exe=$(readlink -f /proc/$pid/exe 2>/dev/null)"
done
[ "$found" = "0" ] && echo "no running processes matching backend/bin/"
echo
echo "=== GO TOOLCHAIN ==="
export PATH=/usr/local/go/bin:$PATH
command -v go || echo "go NOT FOUND"
go version 2>&1 | head -3
echo
echo "=== GO LANG / PY / JQ ==="
command -v python3 && python3 -V 2>&1
command -v jq && jq --version 2>&1
echo
echo "=== GO BUILDINFO (go version -m) ==="
for b in api worker migrate; do
  p="$PROJ/backend/bin/$b"
  [ -f "$p" ] || continue
  echo "--- go version -m $p ---"
  go version -m "$p" 2>&1 | head -100
  echo
done
echo
echo "=== SYFT BINARY CANDIDATES ==="
command -v syft || echo "syft NOT on PATH"
ls -l /usr/local/bin/syft /usr/bin/syft "$HOME/.local/bin/syft" "$HOME/go/bin/syft" /opt/syft/syft 2>&1 | head -20
echo
echo "=== DOCKER WITHOUT SUDO ==="
command -v docker || echo "docker NOT on PATH"
docker version --format 'client={{.Client.Version}} server={{.Server.Version}}' 2>&1 | head -5
echo
echo "=== DOCKER IMAGE LIST (read-only) ==="
docker image ls --format '{{.Repository}}:{{.Tag}} {{.ID}} {{.Size}}' 2>&1 | head -40
echo
echo "=== SYFT IMAGE PRESENT? ==="
docker image inspect docker.1ms.run/anchore/syft:latest --format 'id={{.Id}} created={{.Created}} size={{.Size}}' 2>&1 | head -5
echo
echo "=== DEPENDENCY-REMEDIATION EVIDENCE ROOT (cross-reference target) ==="
ls -la "$PROJ/acceptance-evidence/dual-system-v1/sprint7/security/dependency-remediation-20260913T014342Z/" 2>&1 | head -60
echo "--- fallback: search for the name anywhere under sprint7 ---"
find "$PROJ/acceptance-evidence/dual-system-v1/sprint7" -maxdepth 4 -name '*dependency-remediation*' 2>/dev/null | head -20
echo
echo "=== BACKEND MODULE FILES ==="
sha256sum "$PROJ/backend/go.mod" "$PROJ/backend/go.sum" 2>&1
echo
echo "=== DISK ==="
df -h "$PROJ" | head -5
echo "PROBE_DONE"
