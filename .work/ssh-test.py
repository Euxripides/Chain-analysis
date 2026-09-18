import os, sys
try:
    import paramiko
except Exception as e:
    print("no paramiko:", e); sys.exit(2)
c = paramiko.SSHClient(); c.load_system_host_keys()
try:
    c.connect(hostname=os.environ.get("BLOCKCHAIN_VM_HOST","192.168.31.152"), username=os.environ.get("BLOCKCHAIN_VM_USER","euripides"), timeout=10, look_for_keys=True, allow_agent=True, password=os.environ.get("BLOCKCHAIN_VM_PASSWORD"))
    print("connected OK")
    _, o, e = c.exec_command("hostname; id -un")
    print(o.read().decode().strip())
except Exception as e:
    print("connect failed:", type(e).__name__, e)
finally:
    c.close()
