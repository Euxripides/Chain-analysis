"""Observe the complete engine process, including serialization and output.

The observer stays small; it streams bounded output and reports
the final cgroup high-water mark only after the Node process has exited.
"""
import hashlib,json,os,pathlib,subprocess,sys

LIMIT=64<<20
prefix='CONTRACT_CONTAINER_RESOURCE_V1:'
process=subprocess.Popen(['node','/engine/cli.mjs'],stdin=sys.stdin.buffer,stdout=subprocess.PIPE,stderr=sys.stderr.buffer)
size=0;digest=hashlib.sha256()
while True:
 chunk=process.stdout.read(65536)
 if not chunk:break
 size+=len(chunk)
 if size>LIMIT:
  process.kill();process.wait();sys.stderr.write('ENGINE_OUTPUT_LIMIT\n');raise SystemExit(1)
 digest.update(chunk);sys.stdout.buffer.write(chunk)
status=process.wait()
if status:raise SystemExit(status if status>0 else 128-status)
sys.stdout.buffer.flush()
resource={'version':'container-execution-resource-v1','stdout_bytes':size,'stdout_sha256':digest.hexdigest(),'engine_exit_code':status,
 'scope':'after_engine_exit_and_complete_output_forwarding','observer_pid':os.getpid()}
root=pathlib.Path('/sys/fs/cgroup')
try:
 resource['memory_peak_bytes']=int((root/'memory.peak').read_text());resource['memory_source']='cgroup_v2_memory_peak'
 resource['cpu_stat']={k:int(v)for k,v in(line.split()for line in(root/'cpu.stat').read_text().splitlines())}
except FileNotFoundError:
 resource['memory_peak_bytes']=int((root/'memory/memory.max_usage_in_bytes').read_text());resource['memory_source']='cgroup_v1_max_usage'
 resource['cpu_stat']={'usage_nanoseconds':int((root/'cpuacct/cpuacct.usage').read_text())}
sys.stderr.write(prefix+json.dumps(resource,separators=(',',':'))+'\n');sys.stderr.flush()
