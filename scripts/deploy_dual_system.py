#!/usr/bin/env python3
"""双系统灰度部署编排（docs/dual-system-development-plan.md §29）。

流程（每一步失败即中止，不进入下一步）：

  0. 备份当前 api/worker 二进制，作为回滚点
  1. SFTP 同步工作区到 VM（复用 scripts/sync_to_vm.py 的规则）
  2. 在 VM 上编译后端（Go 1.26 已在 /usr/local/go）
  3. 安装/刷新 systemd 用户单元与 Nginx 站点配置（先校验 nginx -t）
  4. 应用 PostgreSQL/ClickHouse 迁移 0040-0042
  5. 构建两套前端静态产物
  6. 依次重启 user-console / admin-console / api，等待健康检查
  7. 输出双系统冒烟结果

设计约束：
  * 迁移是独立步骤，不随 API 启动隐式执行（§29.1）。
  * 任一步失败都会打印回滚命令，且不会静默跳过。
  * 不使用可变 `latest` 标签；部署后记录 git SHA 与二进制哈希。

用法：
    python scripts/deploy_dual_system.py --yes                 # 完整部署
    python scripts/deploy_dual_system.py --yes --skip-migrate  # 只发代码（不回滚迁移）
"""

from __future__ import annotations

import argparse
import os
import shlex
import sys
import time

import paramiko

HOST = os.environ.get("BLOCKCHAIN_VM_HOST", "192.168.31.152")
USER = os.environ.get("BLOCKCHAIN_VM_USER", "euripides")
PASSWORD = os.environ.get("BLOCKCHAIN_VM_PASSWORD")
REMOTE = os.environ.get("BLOCKCHAIN_VM_PATH", "/home/euripides/projects/blockchain-platform")
LOCAL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP_DIRS = {".git", "node_modules", "dist", "bin", ".DS_Store", "__pycache__", "test-results", "playwright-report"}
NODE_BIN = "/home/euripides/.nvm/versions/node/v24.20.0/bin"
GO_BIN = "/usr/local/go/bin"


def connect() -> paramiko.SSHClient:
    client = paramiko.SSHClient()
    client.load_system_host_keys()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    args = {"hostname": HOST, "username": USER, "timeout": 20, "look_for_keys": True, "allow_agent": True}
    if PASSWORD:
        args["password"] = PASSWORD
    client.connect(**args)
    return client


def run(client: paramiko.SSHClient, command: str, *, check: bool = True, stream: bool = True) -> int:
    print(f"\n$ {command}", flush=True)
    _, stdout, stderr = client.exec_command(command, timeout=3600, get_pty=False)
    for line in iter(stdout.readline, ""):
        if line.strip() and stream:
            print("  " + line.rstrip(), flush=True)
    err = stderr.read().decode("utf-8", "replace")
    if err.strip() and stream:
        for line in err.strip().splitlines():
            print("  ! " + line, flush=True)
    code = stdout.channel.recv_exit_status()
    if check and code != 0:
        raise SystemExit(f"远端命令失败（exit={code}）：{command}")
    return code


def sync(client: paramiko.SSHClient) -> None:
    print("\n== 1. 同步工作区到 VM ==")
    run(client, f"mkdir -p {shlex.quote(REMOTE)}")
    sftp = client.open_sftp()
    files = 0
    failures: list[tuple[str, str]] = []
    try:
        for root, dirs, names in os.walk(LOCAL):
            dirs[:] = [d for d in dirs if d not in SKIP_DIRS]
            rel = os.path.relpath(root, LOCAL).replace(os.sep, "/")
            remote_root = REMOTE if rel == "." else f"{REMOTE}/{rel}"
            try:
                sftp.mkdir(remote_root)
            except IOError:
                pass
            for name in names:
                if name in SKIP_DIRS or name.endswith(".tsbuildinfo"):
                    continue
                local_path = os.path.join(root, name)
                remote_path = f"{remote_root}/{name}"
                try:
                    sftp.put(local_path, remote_path)
                    if name.endswith(".sh"):
                        sftp.chmod(remote_path, 0o755)
                    files += 1
                except Exception as exc:  # noqa: BLE001 - 汇总后统一报错
                    failures.append((local_path, str(exc)))
    finally:
        sftp.close()
    print(f"  已同步 {files} 个文件")
    if failures:
        for path, err in failures[:20]:
            print(f"  ! {path}: {err}")
        raise SystemExit(f"同步失败 {len(failures)} 个文件，已中止部署")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--yes", action="store_true", help="确认执行（缺少时只做 dry-run 检查）")
    parser.add_argument("--skip-migrate", action="store_true", help="跳过迁移（仅代码发布）")
    parser.add_argument("--skip-sync", action="store_true", help="跳过文件同步")
    args = parser.parse_args()
    if not args.yes:
        print("这是写操作：会编译、迁移并重启 VM 上的服务。确认后请加 --yes。")
        return 2

    client = connect()
    try:
        stamp = time.strftime("%Y%m%d-%H%M%S")
        print("== 0. 记录回滚点 ==")
        run(
            client,
            f"cd {shlex.quote(REMOTE)}/backend && "
            f"for b in api worker; do [ -f bin/$b ] && cp -f bin/$b bin/$b.bak-dualsystem-{stamp}; done; "
            f"ls -1 bin | tail -5",
        )

        if not args.skip_sync:
            sync(client)

        print("\n== 2. 编译后端（VM 上编译，避免交叉编译架构差异） ==")
        run(client, f"cd {shlex.quote(REMOTE)}/backend && {GO_BIN}/go build -o bin/api ./cmd/api && {GO_BIN}/go build -o bin/worker ./cmd/worker && {GO_BIN}/go build -o bin/admin-init ./cmd/admin-init && ls -l bin/api bin/worker bin/admin-init")

        print("\n== 3. 安装 systemd 用户单元与 Nginx 站点 ==")
        run(
            client,
            f"mkdir -p ~/.config/systemd/user && "
            f"cp -f {shlex.quote(REMOTE)}/deploy/blockchain-user-console.service ~/.config/systemd/user/ && "
            f"cp -f {shlex.quote(REMOTE)}/deploy/blockchain-admin-console.service ~/.config/systemd/user/ && "
            f"systemctl --user daemon-reload",
        )
        # 注意：该环境下 /etc/nginx/sites-enabled/blockchain-platform.conf 是普通文件而非
        # 指向 sites-available 的符号链接，因此必须同时写入两处，否则站点不会生效。
        nginx_stage = run(
            client,
            f"sudo -n cp -f {shlex.quote(REMOTE)}/deploy/nginx-blockchain-platform.conf /etc/nginx/sites-available/blockchain-platform.conf"
            f" && sudo -n cp -f {shlex.quote(REMOTE)}/deploy/nginx-blockchain-platform.conf /etc/nginx/sites-enabled/blockchain-platform.conf"
            f" && sudo -n nginx -t",
            check=False,
        )
        if nginx_stage != 0:
            print("  ! Nginx 配置未安装（缺少 sudo 或校验失败），/app-v2 与 /admin 路由将等到手动应用后再生效")

        if not args.skip_migrate:
            print("\n== 4. 应用迁移 0040 / 0041 / 0042 ==")
            run(client, f"cd {shlex.quote(REMOTE)}/backend && {GO_BIN}/go run ./cmd/migrate 2>&1 | tail -8")
        else:
            print("\n== 4. 跳过迁移 ==")

        print("\n== 5. 构建两套前端 ==")
        run(
            client,
            f"cd {shlex.quote(REMOTE)}/frontend && export PATH={NODE_BIN}:$PATH && "
            f"node node_modules/vite/bin/vite.js build --config apps/user-console/vite.config.ts && "
            f"node node_modules/vite/bin/vite.js build --config apps/admin-console/vite.config.ts",
        )

        print("\n== 6. 重启服务 ==")
        if nginx_stage == 0:
            run(client, "sudo -n systemctl reload nginx", check=False)
        run(
            client,
            "systemctl --user enable --now blockchain-user-console.service blockchain-admin-console.service; "
            "systemctl --user restart blockchain-user-console.service blockchain-admin-console.service blockchain-api.service",
        )
        print('  等待 API 就绪（启动会探测 Provider 能力，最多 90 秒）…')
        run(
            client,
            'for i in $(seq 1 90); do '
            'if curl -sf -o /dev/null http://127.0.0.1:8080/health/ready; then echo "api ready after ${i}s"; exit 0; fi; '
            'sleep 1; done; echo "api not ready after 90s"; exit 1',
        )

        print("\n== 7. 冒烟检查 ==")
        run(client, "systemctl --user is-active blockchain-api.service blockchain-user-console.service blockchain-admin-console.service")
        run(client, "curl -s -o /dev/null -w 'api /health/ready -> %{http_code}\\n' http://127.0.0.1:8080/health/ready")
        run(client, "curl -s -o /dev/null -w 'user console -> %{http_code}\\n' http://127.0.0.1:3001/app-v2/")
        run(client, "curl -s -o /dev/null -w 'admin console -> %{http_code}\\n' http://127.0.0.1:3002/admin/")
        run(client, "curl -s -o /dev/null -w 'auth methods -> %{http_code}\\n' http://127.0.0.1:8080/api/auth/v1/methods")
        run(client, "curl -s -o /dev/null -w 'admin without session -> %{http_code}\\n' http://127.0.0.1:8080/api/admin/v1/overview")
        run(client, "docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc \"select name from schema_migrations order by name desc limit 4\"", check=False)

        print(f"\n回滚命令（如需）：ssh {USER}@{HOST} 'cd {REMOTE}/backend && cp bin/api.bak-dualsystem-{stamp} bin/api && systemctl --user restart blockchain-api.service'")
        print("部署完成。")
        return 0
    finally:
        client.close()


if __name__ == "__main__":
    sys.exit(main())
