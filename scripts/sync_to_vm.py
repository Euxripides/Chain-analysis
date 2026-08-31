import paramiko, os, sys, getpass
HOST="192.168.31.152"; USER="euripides"; PW="1009538134ldj"; REMOTE="/home/euripides/projects/blockchain-platform"
LOCAL=os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
SKIP={".git", "node_modules", "dist", "bin", ".DS_Store", "__pycache__"}
c=paramiko.SSHClient(); c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
c.connect(HOST, username=USER, password=PW, timeout=15)
sftp=c.open_sftp()
c.exec_command("mkdir -p "+REMOTE)
files=0; bytes_=0
for root, dirs, names in os.walk(LOCAL):
    dirs[:] = [d for d in dirs if d not in SKIP]
    rel = os.path.relpath(root, LOCAL).replace(os.sep, "/")
    remote_root = REMOTE if rel=="." else REMOTE+"/"+rel
    try: sftp.mkdir(remote_root)
    except IOError: pass
    for n in names:
        if n in SKIP: continue
        lp=os.path.join(root,n); rp=remote_root+"/"+n
        try:
            sftp.put(lp, rp)
            files+=1; bytes_+=os.path.getsize(lp)
        except Exception as e:
            print("FAIL", lp, e)
print("uploaded", files, "files,", bytes_, "bytes ->", REMOTE)
c.close()
