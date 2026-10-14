"""Acquire unmodified release assets and verify archive and executable digests."""
import argparse,hashlib,io,json,pathlib,platform,tarfile,urllib.request
ROOT=pathlib.Path(__file__).resolve().parents[2];BIN=ROOT/'analysis-engines/bin'
HEIM='8d58c97f56d584d5d0737d0d0895fc8c68b7b8b2b680f2a7842245789f0b4868'
ARCHIVE='6c66ffcc55fa4249197721baa3098bc208014ea1d8aa04b2ed50ac6bccffb226'
ANVIL='00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604'
def digest(data):return hashlib.sha256(data).hexdigest()
def fetch(url,limit):
 request=urllib.request.Request(url,headers={'User-Agent':'chain-data-contract-engines/1.0'})
 with urllib.request.urlopen(request,timeout=120) as response:
  data=response.read(limit+1)
 if len(data)>limit:raise RuntimeError('upstream asset exceeds bound')
 return data
def asset(repo,tag,expected):
 release=json.loads(fetch('https://api.github.com/repos/'+repo+'/releases/tags/'+tag,2<<20))
 found=[item for item in release['assets'] if item.get('digest')=='sha256:'+expected]
 if len(found)!=1:raise RuntimeError('expected release digest not present: '+repo+' '+tag)
 item=found[0];return fetch(item['browser_download_url'],300<<20)
def install(name,data,expected):
 if digest(data)!=expected or not data.startswith(b'\x7fELF'):raise RuntimeError('binary digest or platform mismatch: '+name)
 target=BIN/name
 if not target.resolve().is_relative_to(ROOT.resolve()):raise RuntimeError('binary target outside workspace')
 stage=BIN/(name+'.verified');stage.write_bytes(data);stage.chmod(0o755);stage.replace(target)
def matches(name,expected):
 p=BIN/name;return p.is_file() and digest(p.read_bytes())==expected
def main(check_only):
 if platform.system()!='Linux' or platform.machine() not in ['x86_64','AMD64']:raise RuntimeError('formal release requires Linux amd64')
 BIN.mkdir(exist_ok=True)
 if not matches('heimdall',HEIM):
  if check_only:raise RuntimeError('Heimdall not verified')
  install('heimdall',asset('Jon-Becker/heimdall-rs','0.9.3',HEIM),HEIM)
 if not matches('anvil',ANVIL):
  if check_only:raise RuntimeError('Anvil not verified')
  data=asset('foundry-rs/foundry','v1.8.5',ARCHIVE)
  if digest(data)!=ARCHIVE:raise RuntimeError('Foundry archive digest mismatch')
  with tarfile.open(fileobj=io.BytesIO(data),mode='r:gz') as archive:
   members=[m for m in archive.getmembers() if m.isfile() and pathlib.PurePosixPath(m.name).name=='anvil']
   if len(members)!=1 or members[0].size>200<<20:raise RuntimeError('invalid Anvil archive entry')
   install('anvil',archive.extractfile(members[0]).read(),ANVIL)
 print('HEIMDALL_VERIFIED',HEIM);print('ANVIL_VERIFIED',ANVIL)
if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('--check-only',action='store_true');main(parser.parse_args().check_only)
