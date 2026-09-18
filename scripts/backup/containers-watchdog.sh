#!/usr/bin/env bash
# S7-DR-01：依赖容器看门狗。
#
# 背景（真实演练发现）：deploy-nats-1 在 `docker kill -s KILL` 之后**没有**按
# RestartPolicy=unless-stopped 自动拉起（实测容器停在 exited，且无新日志），
# 平台随后处于"依赖缺失"状态。仅依赖 docker 的 restart 策略不足以满足 RTO 要求，
# 因此增加一个幂等的看门狗：发现任一依赖容器不在 running 就问一次 `docker start`。
#
# 设计：
#   - 只对白名单容器生效（不会误启其他容器）；
#   - 幂等：全部 running 时不做任何写操作，直接退出；
#   - 输出一行状态，便于 systemd journal 与巡检核对；
#   - 需要免密 sudo（本部署已配置 `sudo -n docker`）。
set -uo pipefail
CONTAINERS="${CONTAINERS:-deploy-postgres-1 deploy-clickhouse-1 deploy-minio-1 deploy-nats-1}"
DOCKER="docker"
if ! docker info >/dev/null 2>&1; then DOCKER="sudo -n docker"; fi

# 维护持有位：存在任一候选文件时，看门狗只报告、不拉起。
# 原因（真实教训）：DATA-007 用例会主动 `docker stop deploy-nats-1` 来验证
# "NATS 不可用时事件仍事务性写入 outbox"；看门狗若在用例窗口内把 NATS 拉起来，
# 用例前提就被破坏（表现为 pending=0 → FAIL）。
# 运维做计划内维护（升级镜像、改配置、演练）时同样必须持有，避免被自动拉起打断。
#
# 候选路径（按顺序，取第一个存在的）：
#   1) $WATCHDOG_HOLD_FILE                                  —— 运维显式指定
#   2) $XDG_RUNTIME_DIR/blockchain/containers-watchdog.hold  —— 用户服务默认可写（推荐）
#   3) /run/blockchain/containers-watchdog.hold              —— 历史路径（需 root 或免密 sudo）
#
# 【真实缺陷修复 2026-09-13】原实现只认 3)。普通用户在**没有**免密 sudo 的主机上
# 根本创建不了 /run/blockchain，脚本里的 `|| true` 又把失败吞掉，于是持有位
# **静默失效**：看门狗照样把 NATS 拉起 → DATA-007 pending=0 假失败（实测发生过）。
# 现在改为多候选 + 显式打印命中的路径（`WATCHDOG: hold=<path>`），
# 用例侧据此**证明**持有位真的生效，而不是"以为设置了"。
HOLD_FILES=()
[ -n "${WATCHDOG_HOLD_FILE:-}" ] && HOLD_FILES+=("$WATCHDOG_HOLD_FILE")
HOLD_FILES+=("${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/blockchain/containers-watchdog.hold")
HOLD_FILES+=("/run/blockchain/containers-watchdog.hold")

ACTIVE_HOLD=""
for _f in "${HOLD_FILES[@]}"; do
  if [ -e "$_f" ]; then ACTIVE_HOLD="$_f"; break; fi
done

if [ -n "$ACTIVE_HOLD" ]; then
  echo "WATCHDOG: 存在维护持有位 $ACTIVE_HOLD，本次跳过拉起（只报告状态）"
  echo "WATCHDOG: hold=$ACTIVE_HOLD"
  for c in $CONTAINERS; do
    echo "WATCHDOG: $c 状态=$($DOCKER inspect -f '{{.State.Status}}' "$c" 2>/dev/null || echo missing)"
  done
  exit 0
fi
echo "WATCHDOG: hold=none candidates=${HOLD_FILES[*]}"

recovered=0
for c in $CONTAINERS; do
  status=$($DOCKER inspect -f '{{.State.Status}}' "$c" 2>/dev/null || echo missing)
  case "$status" in
    running)
      ;;
    missing)
      echo "WATCHDOG: $c 不存在（未部署？）"
      ;;
    *)
      echo "WATCHDOG: $c 状态=$status，执行 docker start"
      if $DOCKER start "$c" >/dev/null 2>&1; then
        recovered=$((recovered+1))
        echo "WATCHDOG: $c 已拉起"
      else
        echo "WATCHDOG: $c 拉起失败"
      fi
      ;;
  esac
done
echo "WATCHDOG: checked=$(echo $CONTAINERS | wc -w) recovered=$recovered"
exit 0
