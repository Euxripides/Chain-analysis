"""Read a UUID's frozen input from the isolated QA database, preserving its SHA."""
import base64,datetime,hashlib,hmac,json,os,pathlib,subprocess,sys,urllib.parse,urllib.request,uuid
ROOT=pathlib.Path(__file__).resolve().parents[2];sys.path.insert(0,str(ROOT/'scripts/contract-analysis'));from qa_support import dotenv
aid=str(uuid.UUID(sys.argv[1]));envfile=os.environ.get('CONTRACT_QA_ENV','/home/euripides/.config/blockchain-platform/contract-coverage-qa-20261014.env')
v=dotenv(envfile);d=urllib.parse.urlparse(v['MIGRATION_DATABASE_URL']);assert d.path=='/contract_analysis_qa_20261014';e=os.environ.copy();e['PGPASSWORD']=urllib.parse.unquote(d.password)
query="SELECT json_build_object('object',input_object,'sha256',input_sha) FROM investigation_analysis_jobs WHERE id='"+aid+"'"
row=json.loads(subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env=e).decode())
settings=dotenv('/home/euripides/projects/blockchain-platform/deploy/.env');endpoint=settings['MINIO_ENDPOINT'];endpoint=endpoint if '://'in endpoint else ('https://' if settings.get('MINIO_SECURE')=='true'else'http://')+endpoint
def read_object(object_key,expected):
 host=urllib.parse.urlparse(endpoint).netloc;path='/'+settings['RAW_BUCKET']+'/'+urllib.parse.quote(object_key,safe='/');now=datetime.datetime.now(datetime.timezone.utc);stamp=now.strftime('%Y%m%dT%H%M%SZ');day=now.strftime('%Y%m%d');empty=hashlib.sha256(b'').hexdigest()
 headers={'Host':host,'x-amz-date':stamp,'x-amz-content-sha256':empty};signed='host;x-amz-content-sha256;x-amz-date';canonical='GET\n'+path+'\n\n'+''.join(k.lower()+':'+headers[k]+'\n'for k in sorted(headers,key=str.lower))+'\n'+signed+'\n'+empty
 scope=day+'/us-east-1/s3/aws4_request';string='AWS4-HMAC-SHA256\n'+stamp+'\n'+scope+'\n'+hashlib.sha256(canonical.encode()).hexdigest();key=('AWS4'+settings['MINIO_ROOT_PASSWORD']).encode()
 for part in [day,'us-east-1','s3','aws4_request']:key=hmac.new(key,part.encode(),hashlib.sha256).digest()
 signature=hmac.new(key,string.encode(),hashlib.sha256).hexdigest();headers['Authorization']='AWS4-HMAC-SHA256 Credential='+settings['MINIO_ROOT_USER']+'/'+scope+', SignedHeaders='+signed+', Signature='+signature
 with urllib.request.urlopen(urllib.request.Request(endpoint+path,headers=headers),timeout=30)as response:raw=response.read(32<<20)
 assert hashlib.sha256(raw).hexdigest()==expected;return raw
raw=read_object(row['object'],row['sha256']);out=ROOT/'acceptance/contract-analysis-coverage';(out/('frozen-'+aid+'.json')).write_bytes(raw);data=json.loads(raw)
print('FROZEN_INPUT_VERIFIED',aid,len(raw),row['sha256']);print('TOP_LEVEL_FIELDS',list(data));print('CONTRACTS',[{'code_address':x['snapshot']['code_address'],'bytes':len(x.get('bytecode',''))//2}for x in data.get('contracts',[])]);print('TRANSACTION_FIELDS',[list(x)if isinstance(x,dict)else type(x).__name__ for x in data.get('transactions',[])])
if len(sys.argv)>2 and sys.argv[2]=='transaction':
 obj=data['transactions'][0]['object'];tx=read_object(obj['object_key'],obj['sha256']);(out/('frozen-transaction-'+aid+'.json')).write_bytes(tx);print('TRANSACTION_OBJECT_FIELDS',list(json.loads(tx)))
