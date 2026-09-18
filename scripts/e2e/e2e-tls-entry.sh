#!/usr/bin/env bash
# 双系统浏览器验收专用：本地回环 TLS 入口的安装 / 校验 / 启停。
#
# 背景与安全边界见 deploy/blockchain-e2e-tls.service 的文件头注释。
# 一句话：验收必须跑在**真实 https 源**上，否则 Playwright 的 APIRequestContext
# 不会回送 Secure/__Host- Cookie，用例内独立 API 断言会 401（假失败）。
#
# 用法：
#   bash scripts/e2e/e2e-tls-entry.sh install   # 生成自签证书 + 安装并启动用户单元
#   bash scripts/e2e/e2e-tls-entry.sh verify    # 校验入口、证书、Cookie 属性、公网入口未变
#   bash scripts/e2e/e2e-tls-entry.sh stop      # 停止并禁用（恢复原状）
#   bash scripts/e2e/e2e-tls-entry.sh uninstall # 上面 + 删除证书与单元文件
set -uo pipefail

PROJECT="${PROJECT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
UNIT_NAME=blockchain-e2e-tls.service
PORT="${E2E_TLS_PORT:-8448}"
UPSTREAM="${E2E_TLS_UPSTREAM:-127.0.0.1:8088}"
CERTDIR="${E2E_TLS_CERTDIR:-$HOME/.local/share/blockchain-platform/e2e-tls}"
UNIT_SRC="$PROJECT/deploy/$UNIT_NAME"
UNIT_DST="$HOME/.config/systemd/user/$UNIT_NAME"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"

fail() { echo "失败：$*" >&2; exit 1; }

gen_cert() {
  mkdir -p "$CERTDIR"
  if [ -s "$CERTDIR/e2e-loopback.crt" ] && [ -s "$CERTDIR/e2e-loopback.key" ]; then
    echo "证书已存在：$CERTDIR/e2e-loopback.crt"
    return 0
  fi
  command -v openssl >/dev/null || fail "缺少 openssl"
  openssl req -x509 -newkey rsa:2048 -nodes -days 825 \
    -keyout "$CERTDIR/e2e-loopback.key" -out "$CERTDIR/e2e-loopback.crt" \
    -subj "/CN=blockchain-e2e-loopback" \
    -addext "subjectAltName=IP:127.0.0.1,DNS:localhost" \
    -addext "basicConstraints=critical,CA:FALSE" \
    -addext "keyUsage=critical,digitalSignature,keyEncipherment" \
    -addext "extendedKeyUsage=serverAuth" >/dev/null 2>&1 || fail "证书生成失败"
  chmod 600 "$CERTDIR/e2e-loopback.key"
  chmod 644 "$CERTDIR/e2e-loopback.crt"
  echo "已生成自签证书：$CERTDIR"
}

cmd_install() {
  command -v socat >/dev/null || fail "缺少 socat（apt-get install -y socat）"
  [ -f "$UNIT_SRC" ] || fail "找不到 $UNIT_SRC"
  gen_cert
  mkdir -p "$(dirname "$UNIT_DST")"
  cp "$UNIT_SRC" "$UNIT_DST"
  systemctl --user daemon-reload
  systemctl --user enable --now "$UNIT_NAME" || fail "启动 $UNIT_NAME 失败"
  sleep 1
  cmd_verify
}

cmd_verify() {
  local rc=0
  echo "== 监听（8448 必须只在回环；8088 是公网 tunnel 目标，不应被本脚本改动）=="
  ss -ltn | grep -E ":${PORT}\b|:8088\b" || true
  ss -ltn | grep -qE "127\.0\.0\.1:${PORT}\b" || { echo "  入口未监听 127.0.0.1:${PORT}"; rc=1; }
  if ss -ltn | grep -E "0\.0\.0\.0:${PORT}\b|\[::\]:${PORT}\b" >/dev/null; then
    echo "  拒绝：入口监听了非回环地址（与公网隔离的前提被破坏）"; rc=1
  fi
  echo "== 健康检查（https）=="
  local code
  code=$(curl -sk -o /dev/null -w "%{http_code}" "https://127.0.0.1:${PORT}/health/live" || echo 000)
  echo "  https://127.0.0.1:${PORT}/health/live -> $code"
  [ "$code" = "200" ] || rc=1
  echo "== Cookie 属性（必须仍是 Secure；__Host- 前缀只在 https 入口才被浏览器接受）=="
  curl -sk -i -X POST "https://127.0.0.1:${PORT}/api/auth/v1/login" \
    -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
    -d '{"identifier":"__e2e_entry_probe__","password":"__e2e_entry_probe__","audience":"user"}' 2>/dev/null \
    | grep -i "^set-cookie" | sed -E "s/=[^;]+/=<redacted>/" || echo "  （无 Set-Cookie：探测账号不存在，属预期）"
  echo "== 公网入口未被改动 =="
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:8088/health/live" || echo 000)
  echo "  http://127.0.0.1:8088/health/live -> $code"
  [ "$code" = "200" ] || rc=1
  echo "== 证书 =="
  openssl x509 -in "$CERTDIR/e2e-loopback.crt" -noout -subject -dates -fingerprint -sha256 2>/dev/null || true
  if [ "$rc" = "0" ]; then echo "结论：PASS（验收入口可用，公网入口未变）"; else echo "结论：FAIL"; fi
  return $rc
}

cmd_stop() {
  systemctl --user disable --now "$UNIT_NAME" 2>/dev/null || true
  echo "已停止 $UNIT_NAME（公网入口 8088 不受影响）"
}

cmd_uninstall() {
  cmd_stop
  rm -f "$UNIT_DST"
  systemctl --user daemon-reload 2>/dev/null || true
  rm -rf "$CERTDIR"
  echo "已删除单元与证书"
}

case "${1:-verify}" in
  install)   cmd_install ;;
  verify)    cmd_verify ;;
  stop)      cmd_stop ;;
  uninstall) cmd_uninstall ;;
  *) echo "用法：$0 {install|verify|stop|uninstall}" >&2; exit 2 ;;
esac
