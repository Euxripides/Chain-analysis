"""Generate ABI standards with the already pinned independent eth-abi runtime."""
import datetime,hashlib,json,pathlib,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-memory'
image=(ROOT/'acceptance/contract-analysis-partial-recovery/engine-image.txt').read_text().strip()
stamp=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ');out=BASE/'abi-reference'/stamp;out.mkdir(parents=True)
program=r'''
import decimal,hashlib,importlib.metadata,json,random
from eth_abi import encode,decode
assert importlib.metadata.version('eth-abi')=='6.0.0'
random.seed(1001716)
def uint(bits=256):return random.getrandbits(bits)
def sint(bits=128):return uint(bits)-(1<<(bits-1))
def address():return'0x'+uint(160).to_bytes(20,'big').hex()
def blob(n=None):return bytes(random.getrandbits(8)for _ in range(random.randrange(0,65)if n is None else n))
def normalize(value):
 if isinstance(value,bool):return value
 if isinstance(value,int):return str(value)
 if isinstance(value,bytes):return'0x'+value.hex()
 if isinstance(value,(list,tuple)):return[normalize(v)for v in value]
 if isinstance(value,decimal.Decimal):return format(value,'f')
 return value
cases=[]
for i in range(16):
 samples=[
  (['uint8','int24','address','bool','bytes5','uint256'],[uint(8),sint(24),address(),i%2==0,blob(5),uint()]),
  (['bytes','string','uint256[]'],[blob(),'unicode-'+str(i)+'-中文',[uint()for _ in range(i%5)]]),
  (['uint256[][]','string[]'],[[[uint()for _ in range((i+j)%4)]for j in range(i%4)],['x'*j+'中文'for j in range(i%4)]]),
  (['(uint256,bytes,string[])'],[(uint(),blob(),['item-'+str(j)for j in range(i%4)])]),
  (['(address,(uint128,int128)[],bytes32)[2]'],[[(address(),[(uint(128),sint())for _ in range((i+j)%3)],blob(32))for j in range(2)]]),
  (['(bytes,uint256[])[2][]'],[[[(blob(),[uint()for _ in range(j+1)])for j in range(2)]for _ in range(i%3)]]),
  (['bytes[][2]'],[[[blob()for _ in range((i+j)%3)]for j in range(2)]]),
  (['uint256[2][3]','(bool,string)[]'],[[[uint(),uint()]for _ in range(3)],[(j%2==0,'entry-'+str(j))for j in range(i%5)]]),
  (['fixed128x18','ufixed128x18'],[decimal.Decimal(i-8)/decimal.Decimal(10**18),decimal.Decimal(i+1)/decimal.Decimal(10**18)])
 ]
 for group,(types,values)in enumerate(samples):
  encoded=encode(types,values);expected=normalize(decode(types,encoded))
  cases.append({'name':str(group)+'-'+str(i),'group':group,'types':types,'encoded':'0x'+encoded.hex(),'expected':expected,'sha256':hashlib.sha256(encoded).hexdigest()})
print(json.dumps({'reference':'eth-abi','version':importlib.metadata.version('eth-abi'),'seed':1001716,'cases':cases}))
'''
command=['docker','run','--rm','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--pids-limit','128','--entrypoint','/opt/contract-python/bin/python',image,'-c',program]
run=subprocess.run(command,capture_output=True,timeout=60);(out/'stderr.log').write_bytes(run.stderr)
if run.returncode:print(run.stderr.decode()[-4000:]);raise SystemExit(run.returncode)
data=json.loads(run.stdout);assert len(data['cases'])==144
(out/'fixtures.json').write_bytes(run.stdout);(out/'generator.py').write_text(program)
manifest={'image':image,'reference_version':data['version'],'fixtures_sha256':hashlib.sha256(run.stdout).hexdigest(),'generator_sha256':hashlib.sha256(program.encode()).hexdigest(),'cases':len(data['cases']),'external_http':0,'status':'independent_reference_generated_not_engine_validation'}
(out/'manifest.json').write_text(json.dumps(manifest,indent=2));(BASE/'abi-reference-current.txt').write_text(str(out)+'\n');print('ABI_INDEPENDENT_REFERENCE_GENERATED',len(data['cases']),str(out),flush=True)
