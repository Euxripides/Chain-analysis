import { readFileSync, writeFileSync, mkdirSync, readdirSync } from 'node:fs';
import { dirname, resolve, join, relative } from 'node:path';
import { createRequire } from 'node:module';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { analyze } from '../../analysis-engines/analyze.mjs';

const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url));
const root=resolve(new URL('../..',import.meta.url).pathname);
const out=join(root,process.env.CONTRACT_CORPUS_OUT||'acceptance/contract-analysis-coverage/frozen-corpus');mkdirSync(out,{recursive:true});
const vendor=join(root,'.work/coverage/vendors/openzeppelin-v5.4.0');
const sha=value=>createHash('sha256').update(value).digest('hex');
const {keccak_256}=require('@noble/hashes/sha3.js');
const engineFiles=[];function engineScan(dir){for(const e of readdirSync(dir,{withFileTypes:true})){if(['node_modules','bin','notices','sbf','svm'].includes(e.name))continue;const p=join(dir,e.name);if(e.isDirectory())engineScan(p);else if(/\.(mjs|py|json)$/.test(e.name))engineFiles.push(p)}}engineScan(join(root,'analysis-engines'));
const sourceIdentity=sha(engineFiles.sort().map(p=>relative(root,p)+'\0'+sha(readFileSync(p))).join('\n'));
const files=[];function scan(dir){for(const e of readdirSync(dir,{withFileTypes:true})){const p=join(dir,e.name);if(e.isDirectory())scan(p);else if(e.name.endsWith('.sol'))files.push(p)}}
scan(join(vendor,'contracts/mocks'));files.sort();

function sourcesFor(file,store={}) {
  const key=relative(vendor,file).replaceAll('\\','/');if(store[key])return store;
  const content=readFileSync(file,'utf8');store[key]={content};
  for(const [,target] of content.matchAll(/\bimport\s+(?:[^;]*?\sfrom\s*)?["']([^"']+)["']\s*;/g)) {
    if(!target.startsWith('.'))throw new Error('UNRESOLVED_EXTERNAL_IMPORT:'+target);
    const dependency=resolve(dirname(file),target);
    if(!dependency.startsWith(vendor+'/'))throw new Error('IMPORT_OUTSIDE_CORPUS');
    sourcesFor(dependency,store);
  }
  return store;
}

function worker(version,optimizer) {
  const compiler=require('solc-'+version),rows=[],inputs=[];
  for(const file of files) {
    let sources;try{sources=sourcesFor(file)}catch(error){rows.push({file:relative(vendor,file),status:'SOURCE_UNAVAILABLE',reason:String(error.message)});continue}
    const standard={language:'Solidity',sources,settings:{optimizer:{enabled:optimizer,runs:200},evmVersion:'cancun',
      outputSelection:{'*':{'*':['abi','evm.deployedBytecode','evm.methodIdentifiers','storageLayout'],'':['ast']}}}};
    let compiled;try{compiled=JSON.parse(compiler.compile(JSON.stringify(standard)))}catch(error){rows.push({file:relative(vendor,file),status:'COMPILE_ERROR',reason:String(error.message)});continue}
    const errors=(compiled.errors||[]).filter(e=>e.severity==='error');if(errors.length){rows.push({file:relative(vendor,file),status:'COMPILE_ERROR',reason:errors.map(x=>x.message).join(';').slice(0,500)});continue}
    const path=relative(vendor,file).replaceAll('\\','/');
    for(const [name,contract] of Object.entries(compiled.contracts[path]||{})) {
      const code=contract.evm.deployedBytecode.object;
      if(!code||/[^0-9a-f]/i.test(code)||Object.keys(contract.evm.methodIdentifiers||{}).length===0)continue;
      const bytecode='0x'+code,code_sha=sha(Buffer.from(code,'hex'));
      const snapshot={chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),entry_address:'0x'+'2'.repeat(40),code_hash:'0x'+Buffer.from(keccak_256(Buffer.from(code,'hex'))).toString('hex'),code_sha256:code_sha};
      const source={stdJsonInput:standard,compilation:{compilerVersion:compiler.version(),fullyQualifiedName:path+':'+name},
        runtimeMatch:'exact_match',match:'exact_match',provenance:'public_repository_local_compilation',
        runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:contract.abi};
      const input={snapshot,bytecode,source,engines:['whatsabi','sevm','solc-ast']};
      const expected=new Set(Object.values(contract.evm.methodIdentifiers).map(x=>'0x'+x));
      const row={sample:path+':'+name,compiler:compiler.version(),optimizer,code_sha256:code_sha,
        repository_commit:'c64a1edb67b6e3f4a15cca8909c9482ad33a02b0',basis:'public_source_local_build',expected_public_entries:expected.size};
      for(const channel of ['source','hidden']) {
        const started=performance.now();try {
          const result=analyze(channel==='source'?input:{snapshot,bytecode,engines:['whatsabi','sevm']});
          const found=new Set(result.sections.methods.map(m=>m.selector).filter(Boolean));
          row[channel]={status:'ANALYZED',wall_ms:Math.round(performance.now()-started),found_expected:[...expected].filter(x=>found.has(x)).length,
            missing:[...expected].filter(x=>!found.has(x)),extra:[...found].filter(x=>!expected.has(x)),methods:result.sections.methods.length,
            unresolved_methods:result.sections.methods.filter(m=>m.completeness.unresolved.length).length,
            source_verified:result.sections.engines.some(e=>e.engine==='solc-ast'&&e.status==='completed'),peak_rss_bytes:process.resourceUsage().maxRSS*1024};
        }catch(error){row[channel]={status:'FAILED',reason:String(error.message).slice(0,500)}}
      }
      rows.push(row);inputs.push(input);
      if(inputs.length%10===0)console.log(version,optimizer?'optimized':'plain',inputs.length,path+':'+name);
    }
    writeFileSync(join(out,`${version}-${optimizer?'optimized':'plain'}.json`),JSON.stringify(rows));
  }
  writeFileSync(join(out,`${version}-${optimizer?'optimized':'plain'}-inputs.jsonl`),inputs.map(x=>JSON.stringify(x)).join('\n'));
}

if(process.argv[2]==='worker')worker(process.argv[3],process.argv[4]==='true');
else {
  const versions=['0.8.24','0.8.25','0.8.26','0.8.27','0.8.28','0.8.29','0.8.30','0.8.31','0.8.32','0.8.33','0.8.34','0.8.35','0.8.36','0.8.37'];
  const seen=new Set(),all=[];
  for(const version of versions)for(const optimizer of [false,true]) {
    const p=spawnSync(process.execPath,['--max-old-space-size=1536',new URL(import.meta.url).pathname,'worker',version,String(optimizer)],{encoding:'utf8',timeout:900000,maxBuffer:1<<20});
    const log=join(out,`${version}-${optimizer?'optimized':'plain'}.log`);writeFileSync(log,(p.stdout||'')+(p.stderr||''));
    const report=join(out,`${version}-${optimizer?'optimized':'plain'}.json`);
    if(p.status!==0)throw new Error('CORPUS_WORKER_FAILED:'+version+':'+p.status+':'+p.stderr?.slice(-500));
    const rows=JSON.parse(readFileSync(report,'utf8'));
    for(const row of rows)if(row.code_sha256&&!seen.has(row.code_sha256)){seen.add(row.code_sha256);all.push(row)}
    const source=all.filter(x=>x.source?.status==='ANALYZED'),hidden=all.filter(x=>x.hidden?.status==='ANALYZED');
    const summary={engine_source_sha256:sourceIdentity,unique_logical_implementations:new Set(all.map(r=>r.sample)).size,engines:['whatsabi','sevm','solc-ast'],unique_code_samples:all.length,source_analyzed:source.length,hidden_analyzed:hidden.length,
      source_entry_recall:{found:source.reduce((s,x)=>s+x.source.found_expected,0),expected:source.reduce((s,x)=>s+x.expected_public_entries,0)},
      hidden_entry_recall:{found:hidden.reduce((s,x)=>s+x.hidden.found_expected,0),expected:hidden.reduce((s,x)=>s+x.expected_public_entries,0)},
      source_extra_entries:source.reduce((s,x)=>s+x.source.extra.length,0),hidden_extra_entries:hidden.reduce((s,x)=>s+x.hidden.extra.length,0),
      status:all.length>=1000?'CORPUS_SIZE_REACHED':'IN_PROGRESS',metric_scope:'entry_discovery_not_business_accuracy',rows:all};
    writeFileSync(join(out,'summary.json'),JSON.stringify(summary));console.log('CORPUS',version,optimizer,'UNIQUE',all.length,'CHILD_EXIT',p.status);
    if(all.length>=1000)process.exit(0);
  }
  process.exitCode=seen.size>=1000?0:1;
}
