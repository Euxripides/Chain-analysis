#!/usr/bin/env python3
"""Run the actual frozen Python adapters for host-side engine tests."""
import os,pathlib,subprocess,sys
root=pathlib.Path(__file__).resolve().parents[2]
image=os.environ.get('CONTRACT_TEST_IMAGE')or(root/'acceptance/contract-analysis-mechanisms/engine-image.txt').read_text().strip()
payload=sys.stdin.buffer.read(16<<20)
adapter='/engine/properties/solver.py' if len(sys.argv)>1 and pathlib.Path(sys.argv[1]).name=='solver.py' else '/engine/adapters/python-engine.py'
command=['docker','run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m','--entrypoint','/opt/contract-python/bin/python',image,adapter]
result=subprocess.run(command,input=payload,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=100)
sys.stdout.buffer.write(result.stdout);sys.stderr.buffer.write(result.stderr);sys.exit(result.returncode)
