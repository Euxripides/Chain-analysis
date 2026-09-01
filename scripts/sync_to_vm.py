import os
import shlex
import sys

import paramiko

HOST = os.environ.get("BLOCKCHAIN_VM_HOST", "192.168.31.152")
USER = os.environ.get("BLOCKCHAIN_VM_USER", "euripides")
PASSWORD = os.environ.get("BLOCKCHAIN_VM_PASSWORD")
REMOTE = os.environ.get("BLOCKCHAIN_VM_PATH", "/home/euripides/projects/blockchain-platform")
LOCAL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP = {".git", "node_modules", "dist", "bin", ".DS_Store", "__pycache__"}


def main() -> int:
    client = paramiko.SSHClient()
    client.load_system_host_keys()
    connect_args = {
        "hostname": HOST,
        "username": USER,
        "timeout": 15,
        "look_for_keys": True,
        "allow_agent": True,
    }
    if PASSWORD:
        connect_args["password"] = PASSWORD
    client.connect(**connect_args)

    try:
        _, stdout, stderr = client.exec_command("mkdir -p " + shlex.quote(REMOTE))
        if stdout.channel.recv_exit_status() != 0:
            raise RuntimeError(stderr.read().decode("utf-8", "replace"))
        sftp = client.open_sftp()
        files = 0
        bytes_ = 0
        failures = []
        try:
            for root, dirs, names in os.walk(LOCAL):
                dirs[:] = [d for d in dirs if d not in SKIP]
                rel = os.path.relpath(root, LOCAL).replace(os.sep, "/")
                remote_root = REMOTE if rel == "." else REMOTE + "/" + rel
                try:
                    sftp.mkdir(remote_root)
                except IOError:
                    pass
                for name in names:
                    if name in SKIP:
                        continue
                    local_path = os.path.join(root, name)
                    remote_path = remote_root + "/" + name
                    try:
                        sftp.put(local_path, remote_path)
                        files += 1
                        bytes_ += os.path.getsize(local_path)
                    except Exception as exc:  # report all failed files before exiting
                        failures.append((local_path, str(exc)))
        finally:
            sftp.close()

        for path, error in failures:
            print("FAIL", path, error, file=sys.stderr)
        if failures:
            print(f"sync failed: {len(failures)} file(s) were not uploaded", file=sys.stderr)
            return 1
        print(f"uploaded {files} files, {bytes_} bytes -> {USER}@{HOST}:{REMOTE}")
        return 0
    finally:
        client.close()


if __name__ == "__main__":
    raise SystemExit(main())
