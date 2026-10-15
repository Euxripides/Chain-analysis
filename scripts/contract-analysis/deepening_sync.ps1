$ErrorActionPreference='Stop'
$deepRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$deepBuild=@'
from pathlib import Path
import subprocess,tarfile
r=Path.cwd();files=set()
for cmd in [['git','diff','--name-only','-z'],['git','ls-files','--others','--exclude-standard','-z']]:
 for x in subprocess.check_output(cmd).decode('utf-8').split('\0'):
  if not x or x.split('/')[0] not in ['analysis-engines','backend','mcp','scripts','docs']:continue
  if set(Path(x).parts)&{'node_modules','.work','bin','dist'}:continue
  p=r/x
  if p.is_file() and not p.is_symlink():files.add(x)
p=r/'.work/deepening/updates.tar';p.parent.mkdir(parents=True,exist_ok=True)
with tarfile.open(p,'w') as t:
 for x in sorted(files):t.add(r/x,arcname=x)
print('SYNC',len(files),p.stat().st_size)
'@
$deepExtract=@'
from pathlib import Path
import tarfile
r=Path('/home/euripides/projects/blockchain-platform-worktrees/contract-deepening-20261015').resolve()
assert r.name=='contract-deepening-20261015' and (r/'.git').exists()
with tarfile.open('/tmp/contract-deepening-updates-20261015.tar') as t:
 for m in t.getmembers():assert m.isfile() and (r/m.name).resolve().is_relative_to(r),m.name
 t.extractall(r,filter='data')
print('CANDIDATE_UPDATED')
'@
Push-Location -LiteralPath $deepRoot
try {
 $deepBuild | python -X utf8 -
 if($LASTEXITCODE -ne 0){throw 'Archive failed'}
 scp -O -o BatchMode=yes -o ConnectTimeout=15 (Join-Path $deepRoot '.work/deepening/updates.tar') dataserver-cf:/tmp/contract-deepening-updates-20261015.tar
 if($LASTEXITCODE -ne 0){throw 'Upload failed'}
 $deepEncoded=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($deepExtract))
 ssh -o BatchMode=yes -o ConnectTimeout=15 dataserver-cf "echo $deepEncoded | base64 -d | python3 -"
 if($LASTEXITCODE -ne 0){throw 'Extraction failed'}
} finally {Pop-Location}
