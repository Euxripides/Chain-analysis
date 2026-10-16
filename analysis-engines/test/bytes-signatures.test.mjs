import test from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {Terms,scalarType} from '../verification/terms.mjs';
import {encodeABI,concreteBytes,literalBytes,hashBytes,concreteRecovery,SECP256K1_ORDER,recoverSigner} from '../verification/bytes.mjs';
import {analyze} from '../analyze.mjs';
import {compileVerificationFixture} from './verification-fixtures.mjs';
import {permitSource,byteEncodingSource} from './signature-fixtures.mjs';
const require=createRequire(import.meta.url),{secp256k1}=require('@noble/curves/secp256k1.js'),{keccak_256}=require('@noble/hashes/sha3.js');
const value=(type,n)=>({...scalarType({type}),term:String(n)}),word=n=>BigInt.asUintN(256,BigInt(n)).toString(16).padStart(64,'0');
const actualSolver=Boolean(process.env.CONTRACT_TEST_IMAGE||process.env.CONTRACT_TEST_SOLVER==='1');

test('ABI bytes preserve sign extension, fixed-byte alignment and packed widths',()=>{
 const args=[value('address',0x1234),value('int16',-2),value('bytes3',0x010203),value('bool','true'),value('uint8',9)];
 const encoded=concreteBytes(encodeABI(args));assert.ok(encoded);
 assert.equal(encoded.toString('hex'),word(0x1234)+word(-2)+'010203'+'0'.repeat(58)+word(1)+word(9));
 assert.equal(concreteBytes(encodeABI(args,{packed:true})).toString('hex'),'0000000000000000000000000000000000001234fffe0102030109');
});
test('finite dynamic ABI values use byte offsets and UTF-8 lengths; packed ambiguity is retained',()=>{
 const a=literalBytes({hexValue:'010203'}),text=literalBytes({value:'你好'});
 assert.equal(concreteBytes(encodeABI([a,text,value('uint8',9)])).toString('hex'),word(96)+word(160)+word(9)+word(3)+'010203'+'0'.repeat(58)+word(6)+Buffer.from('你好').toString('hex')+'0'.repeat(52));
 const left=encodeABI([literalBytes({value:'ab'}),literalBytes({value:'c'})],{packed:true}),right=encodeABI([literalBytes({value:'a'}),literalBytes({value:'bc'})],{packed:true});
 assert.equal(hashBytes(left,new Terms()).term,hashBytes(right,new Terms()).term);
});
test('EVM recovery accepts both S halves and returns zero for invalid inputs',()=>{
 const secret=new Uint8Array(32);secret[31]=1;const digest=keccak_256(Buffer.from('contract-signature-runtime-case'));
 const signed=secp256k1.sign(digest,secret,{prehash:false,format:'recovered'}),v=27n+BigInt(signed[0]),r=BigInt('0x'+Buffer.from(signed.subarray(1,33)).toString('hex')),s=BigInt('0x'+Buffer.from(signed.subarray(33)).toString('hex'));
 const h=BigInt('0x'+Buffer.from(digest).toString('hex')),expected=0x7e5f4552091a69125d5dfcb7b8c2659029395bdfn;
 assert.equal(concreteRecovery(h,v,r,s),expected);assert.equal(concreteRecovery(h,v===27n?28n:27n,r,SECP256K1_ORDER-s),expected);
 for(const invalid of [[0n,r,s],[29n,r,s],[v,0n,s],[v,r,0n],[v,SECP256K1_ORDER,s],[v,r,SECP256K1_ORDER]])assert.equal(concreteRecovery(h,...invalid),0n);
 const terms=new Terms(),symbolic=recoverSigner([value('bytes32',h),terms.variable('v',scalarType({type:'uint8'})),value('bytes32',r),value('bytes32',s)],terms);
 assert.ok(symbolic.term);assert.ok(terms.smt().includes('evm_ecrecover'));assert.equal([...terms.constraints].some(c=>c.includes('distinct')),false);
});
test('source byte encoding and ECDSA calls yield complete conservative state transitions',()=>{
 for(const source of [byteEncodingSource,permitSource]){
  const result=analyze(compileVerificationFixture(true,source));assert.ok(result.sections.state_transitions.length);
  for(const t of result.sections.state_transitions)assert.equal(t.complete,true,JSON.stringify(t.unresolved));
  const signature=result.sections.state_transitions.flatMap(t=>t.paths.flatMap(p=>p.signature_recoveries||[]));assert.ok(signature.length);
  assert.ok(signature.every(r=>r.semantics==='exact_concrete_or_conservative_symbolic_evm_precompile'));
  assert.equal(result.sections.verification_closure[0].whole_authorization_proved,false);
  if(source===permitSource){const permit=result.sections.authorization_models.find(m=>m.kind==='erc2612_permit_integrity');assert.ok(permit);assert.deepEqual(permit.unresolved,[]);assert.equal(permit.obligations.length,7);if(actualSolver)assert.equal(permit.complete,true,JSON.stringify(permit));}
 }
});
test('permit proofs expose missing nonce, deadline, signer checks, domain and action binding',()=>{
 for(const [name,source]of [
  ['nonce',permitSource.replace('nonces[owner]++','nonces[owner]')],
  ['deadline',permitSource.replace('require(block.timestamp<=deadline);','')],
  ['zero-owner',permitSource.replace('require(owner!=address(0));','')],
  ['signer',permitSource.replace('require(signer==owner);','')],
  ['chain',permitSource.replace('block.chainid,address(this)','uint256(2),address(this)')],
  ['verifier',permitSource.replace('block.chainid,address(this)','block.chainid,address(0)')],
  ['payload',permitSource.replace('owner,spender,value,nonces','owner,spender,uint256(0),nonces')],
  ['write',permitSource.replace('allowance[owner][spender]=value;','allowance[owner][spender]=value+1;')]
 ]){
  const result=analyze(compileVerificationFixture(true,source)),model=result.sections.authorization_models.find(m=>m.kind==='erc2612_permit_integrity');assert.ok(model,name);
  assert.deepEqual(model.unresolved,[],name+':'+JSON.stringify(model));
  if(actualSolver)assert.equal(model.status,'counterexample',name+':'+JSON.stringify(model));
 }
});
