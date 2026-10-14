import {readFileSync,writeFileSync,mkdirSync,readdirSync,existsSync} from 'node:fs';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {join} from 'node:path';
const worker=process.env.CONTRACT_LICENSE_WORKER==='1';
const base=worker?new URL('file:///engine/'):new URL('../../analysis-engines/',import.meta.url),notices=new URL('notices/',base);if(!worker)mkdirSync(notices,{recursive:true});
const lock=JSON.parse(readFileSync(new URL('package-lock.json',base),'utf8')),records=[];
if(worker)for(const [path,entry]of Object.entries(lock.packages)){
 if(!path||entry.dev)continue;
 if(!existsSync(new URL(path+'/package.json',base))){records.push({path,version:entry.version,license:entry.license,installation_status:'not_installed_in_runtime'});continue;}
 const pkg=JSON.parse(readFileSync(new URL(path+'/package.json',base),'utf8'));
 const record={path,name:pkg.name,version:pkg.version,license:pkg.license||entry.license||'not_declared',integrity:entry.integrity,source:pkg.repository?.url||pkg.repository,notices:[]};
 for(const file of readdirSync(new URL(path+'/',base)).filter(file=>/^(licen[sc]e|copying|notice)(\.|$|[-_])/i.test(file))){
  const p=new URL(path+'/'+file,base);let bytes;try{bytes=readFileSync(p)}catch{continue};record.notices.push({path:path+'/'+file,sha256:createHash('sha256').update(bytes).digest('hex')});
 }
 if(/^node_modules\/solc-\d+\.\d+\.\d+$/.test(path)){
  const alias=path.split('/').at(-1);const child=spawnSync(process.execPath,['-e',`process.stdout.write(require(${JSON.stringify(alias)}).version())`],{cwd:base,encoding:'utf8',timeout:15000,maxBuffer:8192});
  if(child.status!==0)throw new Error('compiler identity inventory failed: '+alias);
  const version=child.stdout.trim();record.compiler_version=version;record.compiler_license='GPL-3.0';record.wrapper_license=record.license;
  const commit=/\+commit\.([0-9a-f]+)/.exec(version)?.[1];record.compiler_source=commit?`https://github.com/ethereum/solidity/tree/${commit}`:null;
  record.requested_version=alias.slice(5);record.identity_matches_alias=version.startsWith(record.requested_version+'+');
 }
 records.push(record);
}
if(worker){process.stdout.write(JSON.stringify(records));}
else{
const image=readFileSync(new URL('../../acceptance/contract-analysis/engine-image.txt',import.meta.url),'utf8').trim();
const run=spawnSync('docker',['run','--rm','-i','--network','none','--read-only','--user','65532:65532','--cap-drop','ALL','--security-opt','no-new-privileges','--cpus','2','--memory','512m','--pids-limit','32','--env','CONTRACT_LICENSE_WORKER=1','--entrypoint','node',image,'--input-type=module','-'],{input:readFileSync(new URL(import.meta.url)),encoding:'utf8',timeout:180000,maxBuffer:16<<20});
if(run.status!==0)throw new Error('runtime license inventory failed '+run.stderr.slice(0,500));records.push(...JSON.parse(run.stdout));
const binarySources=[
 ['heimdall-MIT.txt','https://raw.githubusercontent.com/Jon-Becker/heimdall-rs/0.9.3/LICENSE'],
 ['foundry-MIT.txt','https://raw.githubusercontent.com/foundry-rs/foundry/51a52c59cffd940f76eddd0b4bb1791aa4b5ac7f/LICENSE-MIT'],
 ['foundry-APACHE.txt','https://raw.githubusercontent.com/foundry-rs/foundry/51a52c59cffd940f76eddd0b4bb1791aa4b5ac7f/LICENSE-APACHE'],
 ['solidity-GPL-3.0.txt','https://raw.githubusercontent.com/ethereum/solidity/v0.8.37/LICENSE.txt'],
];
for(const [name,url]of binarySources){const response=await fetch(url,{signal:AbortSignal.timeout(30000)});if(!response.ok)throw new Error('license fetch failed '+response.status+' '+url);const bytes=Buffer.from(await response.arrayBuffer());if(bytes.length>1<<20)throw new Error('oversize license');writeFileSync(new URL(name,notices),bytes);records.push({name,source:url,sha256:createHash('sha256').update(bytes).digest('hex')})}
writeFileSync(new URL('inventory.json',notices),JSON.stringify(records,null,2));
writeFileSync(new URL('README.md',notices),'# Contract analysis dependency notices\n\nThe runtime retains dependency license files under /engine/node_modules and system notices under /usr/share/doc. inventory.json records exact npm integrity and compiler identities. Solidity compiler core uses GPL-3.0; solc-js wrapper licensing is recorded separately. Compiler source links point to each reported commit. Heimdall and Anvil binaries are unmodified upstream releases. No Slither, Gigahorse, Halmos or Mythril executable is shipped.\n');
console.log('LICENSE_INVENTORY',records.length,'COMPILERS',records.filter(r=>r.compiler_version).length,'IDENTITY_EXCLUSIONS',records.filter(r=>r.identity_matches_alias===false).map(r=>r.requested_version));

}
