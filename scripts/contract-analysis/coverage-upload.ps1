param([switch]$NoExtract)
$ErrorActionPreference='Stop'
$ccRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$ccTar=Join-Path $ccRoot '.work\coverage\updates.tar'
$ccPython=@'
from pathlib import Path
import subprocess,tarfile
r=Path.cwd()
files=set()
for cmd in [['git','diff','--name-only','-z'],['git','ls-files','--others','--exclude-standard','-z']]:
 for x in subprocess.check_output(cmd).decode('utf-8').split('\0'):
  if x and x.split('/')[0] in ['analysis-engines','backend','mcp','frontend','scripts','docs','openapi']:
   p=r/x
   if p.is_file() and not p.is_symlink():files.add(x)
p=r/'.work/coverage/updates.tar';p.parent.mkdir(parents=True,exist_ok=True)
with tarfile.open(p,'w') as t:
 for x in sorted(files):t.add(r/x,arcname=x)
print('COVERAGE_UPLOAD_FILES',len(files),'BYTES',p.stat().st_size)
'@
Push-Location -LiteralPath $ccRoot
try {
 $ccPython | python -X utf8 -
 if ($LASTEXITCODE -ne 0) { throw 'Archive preparation failed' }
 scp -O -o BatchMode=yes $ccTar aurore:/tmp/contract-coverage-updates-20261014.tar
 if ($LASTEXITCODE -ne 0) { throw 'Candidate upload failed' }
 if (-not $NoExtract) {
  $ccExtract=@'
from pathlib import Path
import tarfile
r=Path('/home/euripides/projects/blockchain-platform-worktrees/contract-coverage-20261014').resolve()
assert r.name=='contract-coverage-20261014' and (r/'.git').exists()
with tarfile.open('/tmp/contract-coverage-updates-20261014.tar') as t:
 for m in t.getmembers():assert m.isfile() and (r/m.name).resolve().is_relative_to(r),m.name
 t.extractall(r,filter='data')
p=r/'analysis-engines/adapters/solc-shim.mjs'
if p.exists():p.chmod(0o755)
print('CANDIDATE_UPDATED')
'@
  $ccEncoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($ccExtract))
  ssh -o BatchMode=yes aurore "echo $ccEncoded | base64 -d | python3 -"
  if ($LASTEXITCODE -ne 0) { throw 'Candidate extraction failed' }
 }
} finally { Pop-Location }
