"""Bounded public corpus acquisition; every attempted external HTTP request is durable."""
import datetime,hashlib,json,pathlib,subprocess,sys,urllib.request,urllib.error,urllib.parse,os

ROOT=pathlib.Path(__file__).resolve().parents[2]
assert ROOT.name=='contract-permit-nonces-20261016'
OUT=ROOT/'acceptance/contract-analysis-general/corpus/materials'
OUT.mkdir(parents=True,exist_ok=True)
START='2026-10-16T22:30:47+00:00'
BLOCK=19000000
BLOCK_HASH='0xcf384012b91b081230cdf17a3f7dd370d8e67056058af6b272b3d54aa2714fac'
RPC='https://ethereum-rpc.publicnode.com'
sha=lambda b:hashlib.sha256(b).hexdigest()
SAMPLES=[
 ('ampl','0xd46ba6d942050d489dbd938a2c909a5d5039a161','rebase','development'),
 ('steth','0xae7ab96520de3a18e5e111b5eaab095312d7fe84','rebase','blind'),
 ('sdai','0x83f20f44975d03b1b09e64809b757c47f942beea','vault_shares','development'),
 ('yearn-dai-v2','0xda816459f1ab5631232fe5e97a05bbbb94970c95','vault_shares','supplemental_source_provider_unavailable'),
 ('sushi-bar','0x8798249c2e607446efb7ad49ec89dd1865ff4272','vault_shares','blind'),
 ('uniswap-v3-pool','0x8ad599c3a0ff1de082011efddc58f1908eb6e6d8','amm','blind'),
 ('sushi-masterchef','0xc2edad668740f1aa35e4d8f227fb8e17dca888cd','rewards','development'),
 ('uniswap-v3-staker','0xe34139463ba50bd61336e0c446bd8c0867c6fe65','rewards','blind'),
 ('compound-ceth','0x4ddc2d193948926d02f9b1fe9e1daa0718270ed5','lending_liquidation','development'),
 ('maker-vat','0x35d1b3f3d7966a1dfe207aa4514c12a259a0492b','lending_liquidation','blind'),
 ('usdc-proxy','0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48','proxy_factory','development'),
 ('uniswap-v2-factory','0x5c69bee701ef814a2b6a3edd4b1652cb9cc5aa6f','proxy_factory','blind'),
 ('permit2','0x000000000022d473030f116ddee9f6b43ac78ba3','signature_authority','development'),
 ('safe-singleton','0xd9db270c1b5e3bd161e8c8503c55ceabee709552','signature_authority','blind'),
]
LEDGER=OUT/'external-http-ledger.json'
ledger=json.loads(LEDGER.read_text())if LEDGER.exists()else{'started_at_utc':START,'http_limit':18,'requests':[],'billing_basis':'public_unauthenticated_endpoints_no_billed_account','credits':0,'estimated_usd':'0'}

def campaign_usage():
 from qa_support import dotenv
 values=dotenv('/home/euripides/.config/blockchain-platform/contract-permit-nonces-qa-20261016.env')
 d=urllib.parse.urlparse(values['MIGRATION_DATABASE_URL']);assert d.path=='/contract_analysis_qa_20261014'
 query="SELECT coalesce(sum(http_requests),0)FROM provider_usage WHERE(usage_type='contract_analysis'AND request_time>='2026-10-14T05:33:05.139606+00:00')OR(usage_type<>'contract_analysis'AND request_time>='"+START+"');"
 raw=subprocess.check_output(['docker','exec','-i','-e','PGPASSWORD','deploy-postgres-1','psql','-X','-A','-t','-v','ON_ERROR_STOP=1','-U',d.username,'-d',d.path[1:]],input=query.encode(),env={**os.environ,'PGPASSWORD':urllib.parse.unquote(d.password)})
 return int(raw)+123+len(ledger['requests'])

class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs):return None

def request(key,url,payload=None):
 path=OUT/(key+'.json')
 previous=next((r for r in ledger['requests']if r['key']==key and r.get('status')=='completed'),None)
 if previous:
  data=path.read_bytes();assert sha(data)==previous['response_sha256'];return json.loads(data)
 assert urllib.parse.urlparse(url).hostname in ['sourcify.dev','ethereum-rpc.publicnode.com']
 assert len(ledger['requests'])<ledger['http_limit']
 used=campaign_usage();assert used+1<=2000 and used+1-1929<=70
 body=json.dumps(payload,separators=(',',':')).encode()if payload is not None else None
 record={'key':key,'url':url,'method':'POST'if body else'GET','request_sha256':sha(body or b''),'started_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'status':'reserved','campaign_http_before':used}
 ledger['requests'].append(record);LEDGER.write_text(json.dumps(ledger,indent=2))
 req=urllib.request.Request(url,data=body,headers={'Content-Type':'application/json','Accept':'application/json','User-Agent':'contract-general-analysis-corpus/1'})
 try:
  with urllib.request.build_opener(NoRedirect).open(req,timeout=55)as response:
   data=response.read((24<<20)+1);assert len(data)<=24<<20;record['http_status']=response.status
  value=json.loads(data);path.write_bytes(data);record.update({'status':'completed','response_sha256':sha(data),'response_bytes':len(data)})
 except Exception as error:
  record.update({'status':'failed','error':str(error)[:500]})
  if isinstance(error,urllib.error.HTTPError):
   data=error.read(1<<20);(OUT/(key+'-error.bin')).write_bytes(data);record['http_status']=error.code;record['error_sha256']=sha(data)
  raise
 finally:
  record['finished_at_utc']=datetime.datetime.now(datetime.timezone.utc).isoformat();LEDGER.write_text(json.dumps(ledger,indent=2))
 return value

def source(key,address):
 value=request('source-'+key,'https://sourcify.dev/server/v2/contract/1/'+address+'?fields=all')
 assert str(value.get('chainId'))=='1'and value.get('address','').lower()==address
 assert value.get('stdJsonInput')and value.get('compilation')
 d=OUT/key;d.mkdir(exist_ok=True);(d/'source-provider.json').write_bytes((OUT/('source-'+key+'.json')).read_bytes())
 print('CORPUS_SOURCE',key,value['compilation'].get('compilerVersion'),value['compilation'].get('fullyQualifiedName'),flush=True)

def rpc_batch(key,calls):
 assert calls and all(x['method']in ['eth_getCode','eth_getBlockByNumber','eth_getStorageAt','eth_call']for x in calls)
 value=request(key,RPC,[{'jsonrpc':'2.0','id':i+1,**c}for i,c in enumerate(calls)])
 assert isinstance(value,list)and len(value)==len(calls)
 by_id={r['id']:r for r in value};assert len(by_id)==len(calls)
 for i in range(1,len(calls)+1):assert 'result'in by_id[i],by_id[i]
 return [by_id[i]['result']for i in range(1,len(calls)+1)]

mode=sys.argv[1]
(OUT/'new-root-candidates.json').write_text(json.dumps([{'key':k,'address':a,'group':g,'set':s}for k,a,g,s in SAMPLES],indent=2))
if mode=='sources':
 (OUT/'new-root-candidates.json').write_text(json.dumps([{'key':k,'address':a,'group':g,'set':s}for k,a,g,s in SAMPLES],indent=2))
 failures=[]
 for key,address,group,split in SAMPLES:
  try:source(key,address)
  except Exception as e:failures.append({'key':key,'error':str(e)[:500]});print('CORPUS_SOURCE_FAILED',key,str(e)[:250],flush=True)
 (OUT/'source-acquisition-failures.json').write_text(json.dumps(failures,indent=2))
 if failures:raise SystemExit(1)
elif mode=='runtimes':
 calls=[{'method':'eth_getBlockByNumber','params':[hex(BLOCK),False]}]+[{'method':'eth_getCode','params':[a,hex(BLOCK)]}for _,a,_,_ in SAMPLES]
 calls.extend([{'method':'eth_getStorageAt','params':['0xd46ba6d942050d489dbd938a2c909a5d5039a161','0x7050c9e0f4ca769c69bd3a8ef740bc37934f8e2c036e5a723fd8ee048ed3f8c3',hex(BLOCK)]},{'method':'eth_call','params':[{'to':'0xae7ab96520de3a18e5e111b5eaab095312d7fe84','data':'0x5c60da1b'},hex(BLOCK)]}])
 values=rpc_batch('root-runtime-batch',calls);assert values[0]['hash']==BLOCK_HASH and int(values[0]['number'],16)==BLOCK
 (OUT/'pinned-block.json').write_text(json.dumps(values[0],indent=2))
 for(key,address,group,split),code in zip(SAMPLES,values[1:1+len(SAMPLES)]):
  assert code.startswith('0x')and len(code)>2,key
  d=OUT/key;d.mkdir(exist_ok=True);(d/'runtime.hex').write_text(code)
  (d/'snapshot.json').write_text(json.dumps({'chain':'ethereum','chain_id':'1','block_number':BLOCK,'block_hash':BLOCK_HASH,'block_time':int(values[0]['timestamp'],16),'entry_address':address,'code_address':address,'storage_address':address,'source':'public_RPC_exact_block_batch','group':group,'set':split},indent=2))
  print('CORPUS_RUNTIME',key,(len(code)-2)//2,flush=True)
 proxies=[]
 for key,word,root_address,basis in [('ampl',values[-2],'0xd46ba6d942050d489dbd938a2c909a5d5039a161','source_verified_implementation_slot'),('steth',values[-1],'0xae7ab96520de3a18e5e111b5eaab095312d7fe84','source_verified_public_implementation_getter')]:
  assert len(word)==66 and word[:26]=='0x'+'0'*24 and int(word,16)>0
  proxies.append({'key':key+'-implementation','code_address':'0x'+word[-40:],'storage_address':root_address,'entry_address':root_address,'block_number':BLOCK,'block_hash':BLOCK_HASH,'basis':basis})
 (OUT/'proxy-code-bindings.json').write_text(json.dumps(proxies,indent=2))
elif mode=='source-one':
 key=sys.argv[2];sample=next(x for x in SAMPLES if x[0]==key);source(key,sample[1])
elif mode=='dependency-sources':
 for proxy in json.loads((OUT/'proxy-code-bindings.json').read_text()):source(proxy['key'],proxy['code_address'])
elif mode=='dependencies':
 proxies=json.loads((OUT/'proxy-code-bindings.json').read_text())
 codes=rpc_batch('dependency-runtime-batch',[{'method':'eth_getCode','params':[p['code_address'],hex(BLOCK)]}for p in proxies])
 for p,code in zip(proxies,codes):
  assert code.startswith('0x')and len(code)>2
  d=OUT/p['key'];d.mkdir(exist_ok=True);(d/'runtime.hex').write_text(code);(d/'snapshot.json').write_text(json.dumps({'chain':'ethereum','chain_id':'1',**p},indent=2));source(p['key'],p['code_address'])
else:raise SystemExit('unknown acquisition phase')
print('CORPUS_EXTERNAL_HTTP_USED',len(ledger['requests']),'CAMPAIGN_HTTP',campaign_usage(),flush=True)
