import {readFileSync,writeFileSync,mkdirSync} from 'node:fs';
import {spawnSync} from 'node:child_process';
import {createRequire} from 'node:module';
import {resolve,dirname,relative} from 'node:path';
import {compileFixture} from '../../analysis-engines/test/fixtures.mjs';
const root=resolve(new URL('../..',import.meta.url).pathname),out=resolve(root,process.env.CONTRACT_QA_OUT||'acceptance/contract-analysis-coverage','gold');mkdirSync(out,{recursive:true});
const require=createRequire(new URL('../../analysis-engines/package.json',import.meta.url)),{keccak_256}=require('@noble/hashes/sha3.js');
const codeHash=code=>'0x'+Buffer.from(keccak_256(Buffer.from(code.slice(2),'hex'))).toString('hex');
const image=readFileSync(resolve(out,'../engine-image.txt'),'utf8').trim(),checks=[],runs=[];
function check(scenario,ok,evidence){checks.push({scenario,status:ok?'PASS':'FAIL',evidence});writeFileSync(resolve(out,'report.json'),JSON.stringify({image,checks,runs},null,2));console.log(scenario,ok?'PASS':'FAIL');if(!ok)throw new Error(scenario)}
function run(name,input){const start=performance.now();const process=spawnSync('docker',['run','--rm','--pull','never','-i','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','2g','--memory-swap','2g','--pids-limit','128','--log-driver','none','--tmpfs','/tmp:rw,nosuid,size=192m',image],{input:JSON.stringify(input),encoding:'utf8',timeout:310000,maxBuffer:64<<20});
 check(name+'-sandbox-exit',process.status===0,process.stderr?.slice(-1000));const result=JSON.parse(process.stdout);writeFileSync(resolve(out,name+'.json'),process.stdout);runs.push({sample:name,code_hash:input.snapshot.code_hash,source:!!input.source,engines:result.sections.engines,wall_ms:Math.round(performance.now()-start),metrics:result.metrics});
 check(name+'-selected-engines',result.sections.engines.every(e=>['completed','partial'].includes(e.status)||(e.engine==='slither'&&!input.source&&e.status==='skipped')),result.sections.engines);
 const evidence=new Map(result.sections.evidence.map(e=>[e.evidence_id,e])),methods=new Set(result.sections.methods.map(m=>m.method_id));
 check(name+'-evidence-identities',Object.entries(result.sections).filter(([s])=>!['artifacts','evidence'].includes(s)).every(([,rows])=>rows.every(row=>(!row.method_id||methods.has(row.method_id))&&(row.evidence_ids||[]).every(id=>evidence.has(id)))));
 check(name+'-row-and-artifact-bounds',Object.entries(result.sections).every(([s,rows])=>rows.every(row=>Buffer.byteLength(JSON.stringify(row))<(s==='artifacts'?32<<20:4<<20))));
 return result;
}
const engines=['whatsabi','sevm','solc-ast','heimdall','slither','gigahorse'];
for(const optimizer of [false,true])for(const name of ['AnalystVault','NativeLedger','DataflowPaths','HelperFlow','PackedLedger','RebasedLedger','OwnershipLedger','LedgerToken','Reachability','DelegateFixture']) {
 const f=compileFixture(name,optimizer),input={bytecode:f.bytecode,snapshot:f.snapshot,source:f.source,engines};const result=run(name+'-'+optimizer,input),find=name=>result.sections.methods.find(m=>m.name===name);
 const expected=Object.values(f.compiled.evm.methodIdentifiers).map(x=>'0x'+x),found=new Set(result.sections.methods.map(m=>m.selector).filter(Boolean));
 check(name+'-'+optimizer+'-external-entries',expected.every(x=>found.has(x))&&[...found].every(x=>expected.includes(x)),{expected:expected.length,found:found.size});
 if(name==='AnalystVault'){const redeem=find('redeem'),funds=result.sections.funds.filter(f=>f.method_id===redeem.method_id);check(name+'-'+optimizer+'-funds-guards-fee',funds.length===2&&funds.some(f=>f.expanded_amount_expression==='amount-(amount * feeBps / 10000)'&&f.to_expression==='msg.sender')&&funds.every(f=>f.conditions.includes('!paused')))}
 if(name==='HelperFlow'){check(name+'-'+optimizer+'-bound-library-formula',result.sections.funds.some(f=>f.method_id===find('pay').method_id&&f.expanded_amount_expression==='amount-(amount*rate/10000)'));check(name+'-'+optimizer+'-bound-authority',result.sections.permissions.some(g=>g.method_id===find('configure').method_id&&g.expanded_condition==='(msg.sender)==(controller)'))}
 if(name==='DataflowPaths')check(name+'-'+optimizer+'-branched-formula-is-partial',result.sections.funds.find(f=>f.method_id===find('conditional').method_id).amount_derivation_status==='partial');
 if(name==='PackedLedger')check(name+'-'+optimizer+'-packed-mapping-projection',result.sections.storage.some(s=>s.key_expression==='credit[holder][7].amount'&&s.storage_projection?.offset===16&&s.storage_projection.path.length===3));
 if(name==='OwnershipLedger')check(name+'-'+optimizer+'-ownership-unit',result.sections.funds.some(f=>f.kind==='nft_ownership_assignment'&&f.token_id_expression==='tokenId'&&f.unit==='nft_units'));
 if(name==='LedgerToken')check(name+'-'+optimizer+'-event-only-not-credited',!result.sections.funds.some(f=>f.method_id===find('counterfeit').method_id));
 if(name==='Reachability')check(name+'-'+optimizer+'-no-unreachable-payment',find('dead').fund_ids.length===0&&result.sections.funds.filter(f=>f.method_id===find('repeated').method_id).length===2);
 if(['AnalystVault','NativeLedger','DelegateFixture'].includes(name)) {delete input.source;const hidden=run(name+'-'+optimizer+'-hidden',input);check(name+'-'+optimizer+'-hidden-signatures-unverified',hidden.sections.methods.every(m=>!m.verified_signature));check(name+'-'+optimizer+'-gigahorse-dataflow',hidden.sections.ir_dataflow?.length>0&&hidden.sections.control_flow?.length>0)}
}
const vendor=resolve(root,'.work/coverage/vendors/openzeppelin-v5.4.0'),sources={};
function include(file){const key=relative(vendor,file).replaceAll('\\','/');if(sources[key])return;const content=readFileSync(file,'utf8');sources[key]={content};for(const[,p]of content.matchAll(/\bimport\s+(?:[^;]*?\sfrom\s*)?["']([^"']+)["']\s*;/g)){const dependency=resolve(dirname(file),p);if(!dependency.startsWith(vendor+'/'))throw new Error('IMPORT_OUTSIDE_VENDOR');include(dependency)}}
for(const file of ['contracts/token/ERC20/utils/SafeERC20.sol','contracts/token/ERC20/ERC20.sol','contracts/token/ERC20/extensions/ERC4626.sol','contracts/token/ERC721/ERC721.sol'])include(resolve(vendor,file));
sources['Gold.sol']={content:`pragma solidity ^0.8.24;
import {SafeERC20} from "contracts/token/ERC20/utils/SafeERC20.sol";
import {IERC20} from "contracts/token/ERC20/IERC20.sol";
import {ERC20} from "contracts/token/ERC20/ERC20.sol";
import {ERC4626} from "contracts/token/ERC20/extensions/ERC4626.sol";
import {ERC721} from "contracts/token/ERC721/ERC721.sol";
contract SafePayments {using SafeERC20 for IERC20;function pay(IERC20 asset,address recipient,uint256 amount) external {asset.safeTransfer(recipient,amount*137/10000);}}
contract ShareVault is ERC4626 {constructor(IERC20 token) ERC20("Shares","SH") ERC4626(token){}}
contract Collectibles is ERC721 {constructor() ERC721("Collectibles","NFT"){}function mint(address to,uint256 tokenId) external {_mint(to,tokenId);}}
`};
const compiler=require('solc-0.8.30'),standard={language:'Solidity',sources,settings:{optimizer:{enabled:true,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.deployedBytecode','evm.methodIdentifiers','storageLayout']}}}};
const output=JSON.parse(compiler.compile(JSON.stringify(standard)));check('openzeppelin-real-source-compiles',!(output.errors||[]).some(e=>e.severity==='error'),(output.errors||[]).filter(e=>e.severity==='error'));
for(const name of ['SafePayments','ShareVault','Collectibles']){const contract=output.contracts['Gold.sol'][name],bytecode='0x'+contract.evm.deployedBytecode.object,snapshot={...compileFixture('NativeLedger').snapshot,code_hash:codeHash(bytecode)};
 const source={stdJsonInput:standard,compilation:{compilerVersion:compiler.version(),fullyQualifiedName:'Gold.sol:'+name},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:contract.abi};
 const result=run('openzeppelin-'+name,{snapshot,bytecode,source,engines});
 if(name==='SafePayments'){const pay=result.sections.methods.find(m=>m.signature==='pay(address,address,uint256)'),funds=result.sections.funds.filter(f=>f.method_id===pay.method_id);check('openzeppelin-safeerc20-actual-call-amount-recipient',funds.length===1&&funds[0].expanded_amount_expression==='amount*137/10000'&&funds[0].expanded_to_expression==='recipient'&&funds[0].expanded_asset_expression==='asset',funds)}
 else check('openzeppelin-'+name+'-interface-profile-and-caveats',result.sections.protocols.some(p=>p.profile===(name==='ShareVault'?'erc4626':'erc721')&&p.economic_model_verified===false&&p.deployed_protocol_identity_verified===false));
}
console.log('EVM_GOLD_PASS',checks.length,'RUNS',runs.length);
