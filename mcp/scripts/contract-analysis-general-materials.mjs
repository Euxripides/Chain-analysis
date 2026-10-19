import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {createRequire} from 'node:module';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {StreamableHTTPClientTransport} from '@modelcontextprotocol/sdk/client/streamableHttp.js';

const root=new URL('../../',import.meta.url),out=new URL('acceptance/contract-analysis-general/corpus/materials/',root);
mkdirSync(out,{recursive:true});
const require=createRequire(new URL('analysis-engines/package.json',root)),{keccak_256}=require('@noble/hashes/sha3.js');
const vars=Object.fromEntries(readFileSync('/home/euripides/projects/blockchain-platform/deploy/.env','utf8').split('\n').filter(l=>l.includes('=')&&!l.startsWith('#')).map(l=>{const i=l.indexOf('=');return[l.slice(0,i),l.slice(i+1).trim().replace(/^["']|["']$/g,'')]}));
const sha=b=>createHash('sha256').update(b).digest('hex');
const payload=r=>{const e=r.structuredContent||JSON.parse(r.content.find(c=>c.type==='text').text);if(r.isError||e.success===false)throw Error(JSON.stringify(e.error||e));return e.data??e;};
const samples=[
 {key:'usdt',port:18085,id:'c1b1c2a0-e654-4f57-870a-01274798563c'},
 {key:'weth',port:18085,id:'7892339f-b9b7-4cc7-835d-c590f54f3aa9'},
 {key:'pepe',port:18085,id:'5611309e-14b5-44db-a0c5-5a8426be5350'},
 {key:'uniswap-v2-pair',port:18092,id:'1900c588-10c3-42b1-a3e9-6a67de38aeac'},
];
const records=[];
for(const sample of samples){
 const client=new Client({name:'general-analysis-existing-materials',version:'1.0.0'});
 try{
  await client.connect(new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${sample.port}/mcp`),{requestInit:{headers:{Authorization:'Bearer '+vars.MCP_API_KEY}}}));
  const call=async(name,args)=>payload(await client.callTool({name,arguments:args}));
  const job=await call('get_contract_analysis',{analysis_id:sample.id});if(job.status!=='completed')throw Error('SAVED_JOB_NOT_COMPLETED');
  const files=[];let cursor;
  do{const p=await call('get_contract_analysis',{analysis_id:sample.id,section:'artifacts',page_size:200,...(cursor?{cursor}:{})});files.push(...p.items);cursor=p.has_more?p.next_cursor:null;}while(cursor);
  async function download(file){
   let offset=0;const parts=[];
   for(;;){const r=await client.callTool({name:'get_contract_analysis_artifact',arguments:{analysis_id:sample.id,artifact_id:file.artifact_id,offset,chunk_size:1<<20}}),meta=payload(r),bytes=Buffer.from(r.content.find(c=>c.type==='resource').resource.blob,'base64');
    if(meta.offset!==offset||bytes.length!==meta.byte_length||sha(bytes)!==meta.chunk_sha256)throw Error('MATERIAL_CHUNK_INTEGRITY_FAILED');parts.push(bytes);if(meta.complete)break;offset=meta.next_offset;
   }
   const bytes=Buffer.concat(parts);if(bytes.length!==file.byte_size||sha(bytes)!==file.sha256)throw Error('MATERIAL_WHOLE_INTEGRITY_FAILED');return bytes;
  }
  const snap=job.metadata.snapshot,sourceFile=files.find(a=>a.kind==='source_provider_document');if(!sourceFile)throw Error('SAVED_SOURCE_MISSING');
  const sourceBytes=await download(sourceFile),source=JSON.parse(sourceBytes);let codeBytes,codeFile;
  for(const file of files.filter(a=>a.kind==='bytecode')){const b=await download(file),code=b.toString('utf8').trim();if(/^0x[0-9a-f]+$/i.test(code)&&'0x'+Buffer.from(keccak_256(Buffer.from(code.slice(2),'hex'))).toString('hex')===snap.code_hash){codeBytes=b;codeFile=file;break;}}
  if(!codeBytes)throw Error('SAVED_DEPLOYED_CODE_HASH_MISMATCH');
  const dir=new URL(sample.key+'/',out);mkdirSync(dir,{recursive:true});
  writeFileSync(new URL('source-provider.json',dir),sourceBytes);writeFileSync(new URL('runtime.hex',dir),codeBytes);writeFileSync(new URL('original-job.json',dir),JSON.stringify(job,null,2));
  const record={key:sample.key,analysis_id:sample.id,snapshot:snap,source:{artifact_id:sourceFile.artifact_id,sha256:sha(sourceBytes),bytes:sourceBytes.length},runtime:{artifact_id:codeFile.artifact_id,sha256:sha(codeBytes),bytes:codeBytes.length},compiler:source.compilation?.compilerVersion,target:source.compilation?.fullyQualifiedName,provider_requests:0,source_basis:'existing_scoped_MCP_artifacts_with_chunk_and_whole_checksums'};
  writeFileSync(new URL('material.json',dir),JSON.stringify(record,null,2));records.push(record);console.log('EXISTING_MATERIAL_VERIFIED',sample.key,record.compiler,record.target);
 }finally{await client.close();}
}
writeFileSync(new URL('existing-materials.json',out),JSON.stringify({records,additional_provider_requests:0},null,2));
