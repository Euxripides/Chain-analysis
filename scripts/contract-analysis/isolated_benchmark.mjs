import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {spawnSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {compileFixture} from '../../analysis-engines/test/fixtures.mjs';
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url));
const {keccak_256}=require('@noble/hashes/sha3.js');
const out=new URL('../../acceptance/contract-analysis/isolated-engines/',import.meta.url);mkdirSync(out,{recursive:true});
const image=process.env.CONTRACT_ENGINE_IMAGE||readFileSync(new URL('../engine-image.txt',out),'utf8').trim(),rows=[];
for(const name of ['AnalystVault','NativeLedger','LedgerToken','Misleading','DelegateFixture','Reachability'])for(const optimizer of [false,true])for(const source of [true,false]){
 const f=compileFixture(name,optimizer),input={bytecode:f.bytecode,snapshot:{...f.snapshot,code_hash:'0x'+Buffer.from(keccak_256(Buffer.from(f.bytecode.slice(2),'hex'))).toString('hex')},engines:['whatsabi','sevm','solc-ast','heimdall']};
 if(source)input.source=f.source;
 const key=`${name}-${optimizer?'optimized':'plain'}-${source?'source':'hidden'}`,started=performance.now();
 const run=spawnSync('docker',['run','--rm','--pull','never','-i','--name','contract-benchmark-'+key.toLowerCase(),'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 const row={sample:key,code_hash:input.snapshot.code_hash,image,wall_ms:Math.round(performance.now()-started),exit_code:run.status,error:run.error?.code,stderr:run.stderr?.slice(0,1000)};
 if(run.status===0){const result=JSON.parse(run.stdout);writeFileSync(new URL(key+'.json',out),run.stdout);Object.assign(row,{methods:result.sections.methods.length,engines:result.sections.engines,metrics:result.metrics,unresolved_methods:result.sections.methods.filter(m=>m.completeness.unresolved.length).length,max_row_bytes:Math.max(...Object.entries(result.sections).filter(([k])=>k!=='artifacts').flatMap(([,items])=>items.map(r=>Buffer.byteLength(JSON.stringify(r)))))});
  row.passed=result.sections.engines.every(e=>['completed','partial'].includes(e.status))&&row.max_row_bytes<(4<<20);
 }else row.passed=false;
 rows.push(row);writeFileSync(new URL('../isolated-engine-benchmark.json',out),JSON.stringify(rows,null,2));console.log(key,row.passed?'PASS':'FAIL',row.wall_ms);
 if(!row.passed)process.exitCode=1;
}
