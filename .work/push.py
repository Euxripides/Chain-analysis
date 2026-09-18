"""把指定文件本地 -> VM 推送（paramiko SFTP）。用法：python .work/push.py <相对路径> [...]"""
import os, shlex, sys
import paramiko

HOST = os.environ.get("BLOCKCHAIN_VM_HOST", "192.168.31.152")
USER = os.environ.get("BLOCKCHAIN_VM_USER", "euripides")
REMOTE = os.environ.get("BLOCKCHAIN_VM_PATH", "/home/euripides/projects/blockchain-platform")
LOCAL = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

def main() -> int:
    paths = sys.argv[1:]
    if not paths:
        print("usage: push.py <relpath> [...]"); return 2
    c = paramiko.SSHClient(); c.load_system_host_keys()
    c.connect(hostname=HOST, username=USER, timeout=15, look_for_keys=True, allow_agent=True,
              password=os.environ.get("BLOCKCHAIN_VM_PASSWORD"))
    try:
        _, o, e = c.exec_command("mkdir -p " + shlex.quote(REMOTE))
        if o.channel.recv_exit_status() != 0:
            raise RuntimeError(e.read().decode())
        sftp = c.open_sftp()
        try:
            for rel in paths:
                lp = os.path.join(LOCAL, rel.replace("/", os.sep))
                rp = REMOTE + "/" + rel
                d = os.path.dirname(rp)
                c.exec_command("mkdir -p " + shlex.quote(d))[1].channel.recv_exit_status()
                sftp.put(lp, rp)
                if rel.endswith(".sh"):
                    sftp.chmod(rp, 0o755)
                print("OK", rel, os.path.getsize(lp), "bytes")
        finally:
            sftp.close()
    finally:
        c.close()
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
