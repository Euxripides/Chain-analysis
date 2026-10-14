import { createHash } from 'node:crypto';
import { id, hash } from '../model.mjs';

const alphabet='123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz';
export function decode58(text) {
  if(typeof text!=='string'||text.length>65536)throw new Error('BASE58_INPUT_INVALID');
  let n=0n;for(const c of text){const digit=alphabet.indexOf(c);if(digit<0)throw new Error('BASE58_INPUT_INVALID');n=n*58n+BigInt(digit)}
  const hex=n.toString(16),body=n===0n?Buffer.alloc(0):Buffer.from(hex.length%2?'0'+hex:hex,'hex');
  return Buffer.concat([Buffer.alloc(text.match(/^1*/)?.[0].length||0),body]);
}
function encode58(bytes) {
  let n=BigInt('0x'+bytes.toString('hex')),text='';while(n){text=alphabet[Number(n%58n)]+text;n/=58n}
  return '1'.repeat(bytes.findIndex(x=>x!==0)<0?bytes.length:bytes.findIndex(x=>x!==0))+text;
}
export function tronAddress(value) {
  if(typeof value!=='string')return null;
  if(value.startsWith('T'))return value;
  const hex=value.replace(/^0x/,'');
  if(!/^(41)?[0-9a-fA-F]{40}$/.test(hex))return null;
  const payload=Buffer.from(hex.length===40?'41'+hex:hex,'hex');
  const checksum=createHash('sha256').update(createHash('sha256').update(payload).digest()).digest().subarray(0,4);
  return encode58(Buffer.concat([payload,checksum]));
}
function integer(value) {
  if(typeof value==='number'&&!Number.isSafeInteger(value))throw new Error('TRANSACTION_INTEGER_PRECISION_LOST');
  if(!/^\d+$/.test(String(value)))throw new Error('TRANSACTION_INTEGER_INVALID');
  return BigInt(value);
}
function artifactEvidence(result,chain,tx,raw) {
  const text=JSON.stringify(raw),artifact_id=id(chain,'transaction',tx),evidence_id=id(artifact_id,'execution-evidence');
  result.sections.artifacts.push({artifact_id,kind:`raw_${chain}_transaction`,filename:`transaction-${tx}.json`,media_type:'application/json',text,sha256:hash(text)});
  (result.sections.execution_evidence||=[]).push({evidence_id,kind:`${chain}_transaction`,tx_hash:tx,artifact_id});
  return evidence_id;
}

export const SOLANA_PROGRAMS={
  system:'11111111111111111111111111111111',
  token:'TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA',
  token2022:'TokenzQdBNbLqP5VEhdkAS6EPFLC1PHnBqCXEpPxuEb',
  compute:'ComputeBudget111111111111111111111111111111',
};
// Instruction encodings identify the interface. They do not certify the program
// code executed in the selected transaction or a caught inner invocation.
export function decodeSolanaInstruction(program,data,accounts) {
  if(typeof data!=='string')return {status:'raw_instruction_data_unavailable'};
  let b;try{b=decode58(data)}catch{return {status:'instruction_encoding_invalid'}}
  const base={basis:'registered_program_interface_encoding',historical_program_code_verified:false};
  const need=n=>{if(b.length<n)throw new Error('INSTRUCTION_LENGTH_INVALID')};
  try {
    if(program===SOLANA_PROGRAMS.system) {
      need(4);const tag=b.readUInt32LE(0);
      if(tag===2){need(12);return {...base,status:'decoded',operation:'system_transfer',from:accounts[0],to:accounts[1],amount_raw:b.readBigUInt64LE(4).toString(),unit:'lamports',purpose:'按指令金额从付款账户转出 lamports'}}
      if(tag===0){need(52);return {...base,status:'decoded',operation:'create_account',from:accounts[0],to:accounts[1],amount_raw:b.readBigUInt64LE(4).toString(),space_raw:b.readBigUInt64LE(12).toString(),owner_bytes_hex:b.subarray(20,52).toString('hex'),unit:'lamports',purpose:'支付账户创建资金，分配数据空间并设置账户 owner'}}
      return {...base,status:'unsupported_system_instruction',discriminator:tag};
    }
    if([SOLANA_PROGRAMS.token,SOLANA_PROGRAMS.token2022].includes(program)) {
      need(1);const tag=b[0],names={3:'transfer',4:'approve',5:'revoke',6:'set_authority',7:'mint_to',8:'burn',9:'close_account',10:'freeze_account',11:'thaw_account',12:'transfer_checked',13:'approve_checked',14:'mint_to_checked',15:'burn_checked'};
      if(!names[tag])return {...base,status:'unsupported_token_instruction',discriminator:tag,extensions_not_decoded:program===SOLANA_PROGRAMS.token2022};
      const row={...base,status:'decoded',operation:'token_'+names[tag],token_program:program,unit:'token_raw',authority:accounts[2]||accounts[1],multisig_account_candidates:accounts.slice(3)};
      if([3,4,7,8,12,13,14,15].includes(tag)){need(tag>=12?10:9);row.amount_raw=b.readBigUInt64LE(1).toString();if(tag>=12)row.decimals=b[9]}
      if(tag===3){row.from=accounts[0];row.to=accounts[1];row.purpose='从代币账户转出指定原始数量'}
      if(tag===12){row.from=accounts[0];row.asset=accounts[1];row.to=accounts[2];row.authority=accounts[3];row.multisig_account_candidates=accounts.slice(4);row.purpose='核对 mint 和 decimals 后转出代币'}
      if([4,13].includes(tag)){row.account=accounts[0];row.spender=accounts[tag===13?2:1];row.authority=accounts[tag===13?3:2];row.purpose='设置代币账户的委托额度'}
      if([7,14].includes(tag)){row.asset=accounts[0];row.to=accounts[1];row.purpose='按 mint 权限向代币账户增发'}
      if([8,15].includes(tag)){row.from=accounts[0];row.asset=accounts[1];row.purpose='销毁代币账户中的指定数量'}
      if(tag===5){row.account=accounts[0];row.authority=accounts[1];row.purpose='撤销代币账户的委托权限'}
      if(tag===6){need(3);row.account=accounts[0];row.authority=accounts[1];row.authority_type=b[1];row.new_authority_option=b[2];if(b[2]===1){need(35);row.new_authority=encode58(b.subarray(3,35))}else if(b[2]!==0)return {status:'instruction_encoding_invalid'};row.purpose='修改指定种类的代币或账户管理权限'}
      if(tag===9){row.account=accounts[0];row.to=accounts[1];row.authority=accounts[2];row.unit='lamports';row.purpose='关闭代币账户并归还账户 lamports；实际金额见余额变化'}
      if([10,11].includes(tag)){row.account=accounts[0];row.asset=accounts[1];row.purpose=tag===10?'冻结代币账户':'解除代币账户冻结'}
      return row;
    }
    if(program===SOLANA_PROGRAMS.compute){need(1);if(b[0]===2){need(5);return {...base,status:'decoded',operation:'set_compute_unit_limit',units:String(b.readUInt32LE(1))}}if(b[0]===3){need(9);return {...base,status:'decoded',operation:'set_compute_unit_price',micro_lamports_per_unit:b.readBigUInt64LE(1).toString()}}}
    return {status:'program_instruction_schema_unavailable'};
  }catch{return {status:'instruction_encoding_invalid'}}
}

export function addSolanaTransactions(result,records) {
  for(const section of ['transactions','execution_calls','account_changes','token_balance_changes','instruction_semantics','balance_reconciliation'])result.sections[section]||=[];
  for(const {signature,record} of records||[]) {
    const evidence_id=artifactEvidence(result,'solana',signature,record),meta=record?.meta,message=record?.transaction?.message;
    if(!meta||!message){result.sections.coverage.push({dimension:'transaction',tx_hash:signature,complete:false,reason:'transaction_metadata_unavailable'});continue}
    const staticKeys=(message.accountKeys||[]).map(k=>typeof k==='string'?k:k.pubkey),writable=meta.loadedAddresses?.writable||[],readonly=meta.loadedAddresses?.readonly||[];
    const keys=[...staticKeys,...writable,...readonly],header=message.header||{};
    const accountMetas=keys.map((address,i)=>{
      const supplied=message.accountKeys?.[i];
      return {address,index:i,signer:typeof supplied?.signer==='boolean'?supplied.signer:i<(header.numRequiredSignatures||0),
        writable:typeof supplied?.writable==='boolean'?supplied.writable:i>=staticKeys.length?i<staticKeys.length+writable.length:
          i<(header.numRequiredSignatures||0)?i<(header.numRequiredSignatures||0)-(header.numReadonlySignedAccounts||0):i<staticKeys.length-(header.numReadonlyUnsignedAccounts||0),
        source:i<staticKeys.length?'static_transaction_key':'loaded_address_table_key'};
    });
    const success=meta.err===null,fee=integer(meta.fee);
    result.sections.transactions.push({chain:'solana',tx_hash:signature,slot:record.slot,block_time:record.blockTime,success,execution_error:meta.err,
      fee_raw:fee.toString(),fee_unit:'lamports',compute_units_consumed:meta.computeUnitsConsumed==null?null:integer(meta.computeUnitsConsumed).toString(),
      account_keys:keys,account_metas:accountMetas,method_ids:[],method_identity_status:'historical_program_code_not_bound',evidence_ids:[evidence_id]});
    for(const group of [{index:-1,instructions:message.instructions||[]},...(meta.innerInstructions||[])])for(const [ordinal,insn] of (group.instructions||[]).entries()) {
      const program=insn.programId||keys[insn.programIdIndex];if(!program)continue;
      const accounts=(insn.accounts||[]).map(k=>typeof k==='number'?keys[k]:k),call_id=id(signature,'instruction',group.index,ordinal);
      const row={call_id,tx_hash:signature,program_id:program,instruction_index:group.index===-1?ordinal:group.index,inner_index:group.index===-1?null:ordinal,
        stack_height:insn.stackHeight??null,account_keys:accounts,account_metas:accounts.map(a=>accountMetas.find(k=>k.address===a)||{address:a,signer:null,writable:null}),
        instruction_data:insn.data??null,parsed:insn.parsed??null,kind:group.index===-1?'top_level_instruction':'recorded_inner_instruction',
        individual_execution_success:null,transaction_success:success,code_binding_status:'transaction_prestate_program_not_verified',evidence_ids:[evidence_id]};
      result.sections.execution_calls.push(row);
      const decoded=decodeSolanaInstruction(program,insn.data,accounts);
      result.sections.instruction_semantics.push({instruction_semantic_id:id(call_id,'semantics'),call_id,tx_hash:signature,program_id:program,...decoded,
        settlement:success?'instruction_intent_with_transaction_metadata':'rolled_back_transaction_instruction_intent',evidence_ids:[evidence_id]});
    }
    if(meta.preBalances?.length===keys.length&&meta.postBalances?.length===keys.length) {
      let delta=0n;
      for(let i=0;i<keys.length;i++){const before=integer(meta.preBalances[i]),after=integer(meta.postBalances[i]);delta+=after-before;
        result.sections.account_changes.push({change_id:id(signature,'lamports',i),tx_hash:signature,account:keys[i],account_index:i,before_raw:before.toString(),after_raw:after.toString(),delta_raw:(after-before).toString(),unit:'lamports',basis:'transaction_metadata_account_balances',includes_rent_and_network_fee:true,evidence_ids:[evidence_id]})}
      result.sections.balance_reconciliation.push({reconciliation_id:id(signature,'lamports-conservation'),tx_hash:signature,asset:'native',unit:'lamports',account_count:keys.length,
        observed_delta_raw:delta.toString(),network_fee_raw:fee.toString(),difference_raw:(delta+fee).toString(),balanced:delta+fee===0n,basis:'complete_pre_post_balance_arrays',state_replay_verified:false,evidence_ids:[evidence_id]});
    }else result.sections.coverage.push({dimension:'native_balances',tx_hash:signature,complete:false,reason:'pre_post_balance_arrays_incomplete'});
    const balances=new Map();
    for(const [side,rows] of [['before',meta.preTokenBalances],['after',meta.postTokenBalances]])for(const r of rows||[]) {
      const key=r.accountIndex+':'+r.mint,current=balances.get(key)||{account_index:r.accountIndex,account:keys[r.accountIndex],asset:r.mint,before_raw:null,after_raw:null};
      current[side+'_raw']=r.uiTokenAmount?.amount==null?null:integer(r.uiTokenAmount.amount).toString();current.decimals=r.uiTokenAmount?.decimals??current.decimals??null;
      current[side+'_owner']=r.owner??null;current[side+'_token_program']=r.programId??null;balances.set(key,current);
    }
    for(const [key,r] of balances)result.sections.token_balance_changes.push({change_id:id(signature,'token',key),tx_hash:signature,...r,
      delta_raw:r.before_raw!==null&&r.after_raw!==null?(BigInt(r.after_raw)-BigInt(r.before_raw)).toString():null,status:r.before_raw!==null&&r.after_raw!==null?'observed_account_net_change':'one_endpoint_unavailable',unit:'token_raw',evidence_ids:[evidence_id]});
    result.sections.coverage.push({dimension:'historical_program_code',tx_hash:signature,complete:false,reason:'transaction_prestate_program_version_not_verified'});
  }
}

export function addTronTransactions(result,records) {
  for(const section of ['transactions','execution_calls','executed_funds','token_events','instruction_semantics'])result.sections[section]||=[];
  const transferTopic='ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef';
  for(const {tx_hash,transaction,receipt} of records||[]) {
    const evidence_id=artifactEvidence(result,'tron',tx_hash,{transaction,receipt});
    if(transaction?.txID?.replace(/^0x/,'').toLowerCase()!==tx_hash.replace(/^0x/,'').toLowerCase()||receipt?.id?.replace(/^0x/,'').toLowerCase()!==tx_hash.replace(/^0x/,'').toLowerCase())throw new Error('TRON_TRANSACTION_IDENTITY_MISMATCH');
    const success=transaction.ret?.every(r=>r.contractRet==='SUCCESS')===true&&(!receipt.receipt?.result||receipt.receipt.result==='SUCCESS')&&receipt.result!=='FAILED';
    result.sections.transactions.push({chain:'tron',tx_hash,block_number:receipt.blockNumber,block_time_ms:receipt.blockTimeStamp,success,
      fee_raw:integer(receipt.fee??0).toString(),fee_unit:'sun',energy_usage_total:receipt.receipt?.energy_usage_total==null?null:integer(receipt.receipt.energy_usage_total).toString(),
      energy_fee_raw:receipt.receipt?.energy_fee==null?null:integer(receipt.receipt.energy_fee).toString(),bandwidth_usage:receipt.receipt?.net_usage==null?null:integer(receipt.receipt.net_usage).toString(),bandwidth_fee_raw:receipt.receipt?.net_fee==null?null:integer(receipt.receipt.net_fee).toString(),
      method_ids:[],method_identity_status:'historical_program_code_not_bound',evidence_ids:[evidence_id]});
    for(const [i,c] of (transaction.raw_data?.contract||[]).entries()) {
      const p=c.parameter?.value||{},from=tronAddress(p.owner_address),to=tronAddress(p.contract_address||p.to_address),call_id=id(tx_hash,'tron-top',i);
      const amount=c.type==='TransferContract'?p.amount:p.call_value;
      result.sections.execution_calls.push({call_id,tx_hash,from,to,call_type:c.type,input:p.data||null,value_raw:amount==null?null:integer(amount).toString(),unit:'sun',success,code_binding_status:'transaction_prestate_program_not_verified',evidence_ids:[evidence_id]});
      if(success&&from&&to&&amount!=null&&integer(amount)>0n)result.sections.executed_funds.push({fund_id:id(call_id,'native'),tx_hash,call_id,asset:'native',from,to,amount_raw:integer(amount).toString(),unit:'sun',basis:'successful_native_transaction_contract',evidence_ids:[evidence_id]});
    }
    for(const [i,call] of (receipt.internal_transactions||[]).entries()) {
      const call_id=id(tx_hash,'tron-inner',i),from=tronAddress(call.caller_address),to=tronAddress(call.transferTo_address),accepted=success&&call.rejected!==true;
      result.sections.execution_calls.push({call_id,tx_hash,from,to,call_type:'tvm_internal_transaction',note:call.note||null,rejected:call.rejected===true,success:accepted?null:false,code_binding_status:'transaction_prestate_program_not_verified',evidence_ids:[evidence_id]});
      for(const [j,value] of (call.callValueInfo||[]).entries())if(accepted&&integer(value.callValue??0)>0n)result.sections.executed_funds.push({fund_id:id(call_id,j),tx_hash,call_id,asset:value.tokenId?'trc10:'+value.tokenId:'native',from,to,amount_raw:integer(value.callValue).toString(),unit:value.tokenId?'trc10_raw':'sun',basis:'receipt_non_rejected_internal_transaction',evidence_ids:[evidence_id]});
    }
    if(success)for(const [log_index,log] of (receipt.log||[]).entries()) {
      if(log.topics?.[0]?.replace(/^0x/,'').toLowerCase()!==transferTopic||log.topics.length!==3||!/^([0-9a-fA-F]{64})$/.test((log.data||'').replace(/^0x/,'')))continue;
      const from=tronAddress(log.topics[1].replace(/^0x/,'').slice(-40)),to=tronAddress(log.topics[2].replace(/^0x/,'').slice(-40));
      if(from&&to)result.sections.token_events.push({event_id:id(tx_hash,'tron-log',log_index),tx_hash,log_index,asset:tronAddress(log.address),from,to,amount_raw:BigInt('0x'+log.data.replace(/^0x/,'')).toString(),unit:'token_raw',basis:'committed_transfer_event',ledger_verified:false,evidence_ids:[evidence_id]});
    }
    result.sections.coverage.push({dimension:'tron_transaction',tx_hash,complete:false,reason:'historical_tvm_account_prestate_and_proposal_rules_unavailable',internal_transactions_presence:receipt.internal_transactions==null?'not_supplied':'receipt_supplied'});
  }
}
