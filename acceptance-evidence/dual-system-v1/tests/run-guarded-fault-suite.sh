#!/usr/bin/env bash
# 冻结运行器：把保护壳与套件复制到**唯一时间戳目录**后再执行冻结副本。
#
# 为什么必须这样做（2026-09-13 真实事故）：
#   此前直接在原地用 sftp 覆盖 run-fault-drill-guarded.sh，而该脚本正在执行中。
#   bash 是**边读边执行**的：suite 返回后，文件读取偏移落到了新版本 SQL 的中间，
#   于是报 `syntax error near unexpected token `now` / _char(now...`（旧版本被截断成 `_char`），
#   保护壳以 rc=2 退出而外层包装仍报 0。结论：**运行中的脚本文件绝不能被覆盖**。
#   现在的规则：
#     1) 部署用"写 .tmp + 原子 mv"（保留旧 inode，运行者可读完旧内容）；
#     2) 每次执行都先冻结副本到 run 目录，记录 sha256/size，再执行冻结副本；
#     3) 事后可逐字节复算"这次到底跑的是哪一版"。
#
# 退出码契约（2026-09-14 复核）：本运行器**必须**把冻结副本的真实 rc 原样返回。
#   保护壳 rc 域：0=PASS 1=FAIL 2=REVIEW 3=INVALID（INVALID=未跑套件或无法得出结论）。
#   禁止任何形如 `bash ...; echo EXIT=$?` 的包装：那会让"单元退出码来自 echo"，
#   把保护壳的失败显示成 ExecMainStatus=0（2026-09-13 真实踩过）。
#
# 用法：bash run-guarded-fault-suite.sh <套件文件名> [额外参数...]
#   例：bash run-guarded-fault-suite.sh run-sprint5-e2e.sh
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
TESTS="$PROJECT/acceptance-evidence/dual-system-v1/tests"
SUITE_NAME="${1:?需要套件文件名，例如 run-sprint5-e2e.sh}"; shift || true
SUITE_SRC="$TESTS/$SUITE_NAME"
GUARD_SRC="$TESTS/run-fault-drill-guarded.sh"
[ -f "$SUITE_SRC" ] || { echo "找不到套件：$SUITE_SRC" >&2; exit 2; }
[ -f "$GUARD_SRC" ] || { echo "找不到保护壳：$GUARD_SRC" >&2; exit 2; }

export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then set -a; . /tmp/s6-e2e-env.sh; set +a; fi

RUNROOT="$TESTS/sprint7/qa/guarded-runs"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
RUNDIR="$RUNROOT/${STAMP}-${SUITE_NAME%.sh}"
mkdir -p "$RUNDIR/frozen"
cp -p "$GUARD_SRC" "$RUNDIR/frozen/guard.sh"
cp -p "$SUITE_SRC" "$RUNDIR/frozen/suite.sh"
{
  echo "run_dir=$RUNDIR"
  echo "started_at=$(date -Is)"
  echo "host=$(hostname)"
  echo "branch=n/a (VM 部署目录非 git 仓库)"
  ( cd "$RUNDIR/frozen" && sha256sum guard.sh suite.sh )
  ( cd "$RUNDIR/frozen" && stat -c '%n %s bytes' guard.sh suite.sh )
} > "$RUNDIR/frozen-manifest.txt"
cat "$RUNDIR/frozen-manifest.txt"

export PROJECT
export EVIDENCE_DIR="${EVIDENCE_DIR:-$TESTS/sprint7/qa/${SUITE_NAME%.sh}}"
export DRILL_OUT="$RUNDIR/drill"
mkdir -p "$DRILL_OUT"
echo "EVIDENCE_DIR=$EVIDENCE_DIR DRILL_OUT=$DRILL_OUT"

set +e
bash "$RUNDIR/frozen/guard.sh" "$RUNDIR/frozen/suite.sh" "$@"
RC=$?
echo "$RC" > "$RUNDIR/guard-rc.txt"
# 把保护壳的结论镜像到 run 目录（即使 DRILL_OUT 目录被后处理删掉，本次结论仍可复算）；
# 只读取/复制，不解释、不覆盖 rc。
VERDICT_NAME=unknown
if [ -s "$DRILL_OUT/drill-guard-verdict.json" ]; then
  cp -p "$DRILL_OUT/drill-guard-verdict.json" "$RUNDIR/drill-guard-verdict.json" 2>/dev/null || true
  VERDICT_NAME="$(sed -n 's/.*"verdict"[[:space:]]*:[[:space:]]*"\([A-Z]*\)".*/\1/p' "$DRILL_OUT/drill-guard-verdict.json" | head -n 1)"
  [ -n "$VERDICT_NAME" ] || VERDICT_NAME=unknown
fi
{ echo "finished_at=$(date -Is)"; echo "guard_rc=$RC"; echo "guard_verdict=$VERDICT_NAME"; } >> "$RUNDIR/frozen-manifest.txt"
case "$RC" in
  0|1|2|3) : ;;
  *) echo "WARN: guard rc=$RC 不在保护壳契约域(0/1/2/3) 内，仍按真实 rc 退出（不修正、不吞掉）" >&2 ;;
esac
echo "GUARDED_SUITE_RC=$RC"
echo "GUARD_VERDICT=$VERDICT_NAME"
echo "RUN_DIR=$RUNDIR"
# 真实 rc 原样交给上层（systemd 单元的 ExecMainStatus 就等于它）
exit "$RC"
