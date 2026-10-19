"""Verify every evidence ZIP member against both manifests, without extraction."""
import hashlib,io,json,pathlib,sys,zipfile
expected=json.loads(sys.stdin.buffer.readline(1<<20));raw=sys.stdin.buffer.read((64<<20)+1)
assert len(raw)<=64<<20
expected={a['artifact_id']:a for a in expected}
with zipfile.ZipFile(io.BytesIO(raw))as archive:
 names=archive.namelist();assert len(names)==len(set(names))
 manifest=json.loads(archive.read('manifest.json'));entries=manifest['entries']
 assert len(entries)==len(expected)and{e['artifact_id']for e in entries}==set(expected)
 assert set(names)=={'manifest.json'}|{e['path']for e in entries}
 assert sum(i.file_size for i in archive.infolist())<=1<<30
 total=0
 for item in entries:
  path=pathlib.PurePosixPath(item['path']);assert not path.is_absolute()and'..'not in path.parts
  record=expected[item['artifact_id']];data=archive.read(item['path']);digest=hashlib.sha256(data).hexdigest()
  assert len(data)==item['byte_size']==record['byte_size']and digest==item['sha256']==record['sha256']
  total+=len(data)
print(json.dumps({'status':'PASS','zip_members':len(names),'artifact_members':len(entries),'expanded_bytes':total}))
