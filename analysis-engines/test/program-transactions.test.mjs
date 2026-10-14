import test from 'node:test';
import assert from 'node:assert/strict';
import { emptyResult } from '../model.mjs';
import { addSolanaTransactions, addTronTransactions, decodeSolanaInstruction, SOLANA_PROGRAMS, tronAddress } from '../adapters/program-transactions.mjs';

const alphabet='123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
function encoded(bytes){let n=BigInt('0x'+bytes.toString('hex')),s='';while(n){s=alphabet[Number(n%58n)]+s;n/=58n}return '1'.repeat(bytes.findIndex(b=>b!==0)<0?bytes.length:bytes.findIndex(b=>b!==0))+s}
function record(){return {slot:42,blockTime:1700000000,transaction:{message:{header:{numRequiredSignatures:1,numReadonlySignedAccounts:0,numReadonlyUnsignedAccounts:1},accountKeys:['payer','recipient',SOLANA_PROGRAMS.system],instructions:[]}},meta:{err:null,fee:5000,preBalances:['9007199254740993000','0','1'],postBalances:['9007199254740988000','0','1'],loadedAddresses:{writable:['loaded-writable'],readonly:['loaded-readonly']},preTokenBalances:[],postTokenBalances:[]}}}

test('Solana preserves large integers, loaded-account permissions and incomplete balance endpoints',()=>{
 const raw=record();raw.meta.preBalances.push('9','11');raw.meta.postBalances.push('9','11');
 raw.meta.preTokenBalances=[{accountIndex:1,mint:'mint',owner:'before-owner',programId:SOLANA_PROGRAMS.token,uiTokenAmount:{amount:'900719925474099300000',decimals:9}}];
 raw.meta.postTokenBalances=[{accountIndex:1,mint:'mint',owner:'after-owner',programId:SOLANA_PROGRAMS.token,uiTokenAmount:{amount:'900719925474099300007',decimals:9}},{accountIndex:3,mint:'other',uiTokenAmount:{amount:'10',decimals:0}}];
 const r=emptyResult({});addSolanaTransactions(r,[{signature:'sig',record:raw}]);
 assert.equal(r.sections.account_changes[0].delta_raw,'-5000');assert.equal(r.sections.balance_reconciliation[0].balanced,true);
 const keys=r.sections.transactions[0].account_metas;
 assert.equal(keys[0].signer,true);assert.equal(keys[2].writable,false);assert.equal(keys[3].writable,true);assert.equal(keys[4].writable,false);assert.equal(keys[3].signer,false);
 assert.equal(r.sections.token_balance_changes[0].delta_raw,'7');assert.equal(r.sections.token_balance_changes[0].before_owner,'before-owner');assert.equal(r.sections.token_balance_changes[0].after_owner,'after-owner');
 assert.equal(r.sections.token_balance_changes[1].delta_raw,null);assert.equal(r.sections.token_balance_changes[1].before_raw,null);
 assert.equal(r.sections.transactions[0].method_identity_status,'historical_program_code_not_bound');
});
test('Solana instruction decoding keeps uint64 precision and failed or caught calls separate from settlements',()=>{
 const b=Buffer.alloc(10);b[0]=12;b.writeBigUInt64LE(18446744073709551615n,1);b[9]=6;
 const decoded=decodeSolanaInstruction(SOLANA_PROGRAMS.token2022,encoded(b),['source','mint','destination','authority']);
 assert.equal(decoded.amount_raw,'18446744073709551615');assert.equal(decoded.asset,'mint');assert.equal(decoded.authority,'authority');assert.equal(decoded.decimals,6);
 assert.equal(decodeSolanaInstruction(SOLANA_PROGRAMS.token,encoded(Buffer.from([12])),[]).status,'instruction_encoding_invalid');
 const raw=record();delete raw.meta.loadedAddresses;raw.meta.err={InstructionError:[0,'Custom']};raw.transaction.message.instructions=[{programId:SOLANA_PROGRAMS.token2022,accounts:['source','mint','destination','authority'],data:encoded(b)}];
 raw.meta.innerInstructions=[{index:0,instructions:[{programId:SOLANA_PROGRAMS.token,accounts:[],data:'1',stackHeight:2}]}];
 const r=emptyResult({});addSolanaTransactions(r,[{signature:'failed',record:raw}]);
 assert.equal(r.sections.execution_calls[0].individual_execution_success,null);assert.equal(r.sections.execution_calls[1].stack_height,2);assert.equal(r.sections.instruction_semantics[0].settlement,'rolled_back_transaction_instruction_intent');
 assert.equal(r.sections.balance_reconciliation[0].balanced,true);
});
test('unsafe already-parsed transaction integers fail before creating false amounts',()=>{
 const raw=record();raw.meta.fee=9007199254740992;
 assert.throws(()=>addSolanaTransactions(emptyResult({}),[{signature:'unsafe',record:raw}]),/PRECISION_LOST/);
});
test('TRON binds receipt identity, energy, exact fees and rejected internal value transfers',()=>{
 const tx='a'.repeat(64),from='41'+'1'.repeat(40),to='41'+'2'.repeat(40);
 const transaction={txID:tx,ret:[{contractRet:'SUCCESS'}],raw_data:{contract:[{type:'TriggerSmartContract',parameter:{value:{owner_address:from,contract_address:to,call_value:2,data:'abcd'}}}]}};
 const receipt={id:tx,blockNumber:7,fee:'900719925474099300',receipt:{result:'SUCCESS',energy_usage_total:123,net_fee:10},internal_transactions:[{caller_address:from,transferTo_address:to,callValueInfo:[{callValue:9}]},{caller_address:from,transferTo_address:to,rejected:true,callValueInfo:[{callValue:99}]}]};
 const r=emptyResult({});addTronTransactions(r,[{tx_hash:tx,transaction,receipt}]);
 assert.equal(r.sections.transactions[0].fee_raw,'900719925474099300');assert.equal(r.sections.transactions[0].energy_usage_total,'123');
 assert.equal(r.sections.executed_funds.length,2);assert.equal(r.sections.executed_funds[0].unit,'sun');assert.equal(r.sections.executed_funds[1].amount_raw,'9');
 assert.equal(r.sections.executed_funds[0].from,tronAddress(from));assert.equal(r.sections.execution_calls[2].success,false);
 assert.throws(()=>addTronTransactions(emptyResult({}),[{tx_hash:tx,transaction,receipt:{...receipt,id:'b'.repeat(64)}}]),/IDENTITY_MISMATCH/);
 const failed=emptyResult({});addTronTransactions(failed,[{tx_hash:tx,transaction:{...transaction,ret:[{contractRet:'REVERT'}]},receipt}]);assert.equal(failed.sections.executed_funds.length,0);
});
