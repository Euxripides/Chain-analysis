const WORD=1n<<256n,WINDOW=8191n;
export const SYSTEM_ADDRESS='0xfffffffffffffffffffffffffffffffffffffffe';
export const BEACON_ADDRESS='0x000f3df6d732807ef1319fb7b8bb8522d0beac02';
export const HISTORY_ADDRESS='0x0000f90827f1c53a10cb7a02335b175320002935';
export const WITHDRAWAL_ADDRESS='0x00000961ef480eb55e80d19ad83579a64c007002';
export const CONSOLIDATION_ADDRESS='0x0000bbddc7ce488642fb579f8b00f3a590007251';
const slot=n=>'0x'+n.toString(16).padStart(64,'0');
function quantity(value,bits=256){
 if(!['string','number'].includes(typeof value)||typeof value==='number'&&!Number.isSafeInteger(value))throw Error('BLOCK_SYSTEM_QUANTITY_INVALID');
 let n;try{n=BigInt(value);}catch{throw Error('BLOCK_SYSTEM_QUANTITY_INVALID');}if(n<0n||n>=(1n<<BigInt(bits)))throw Error('BLOCK_SYSTEM_QUANTITY_INVALID');return n;
}
function normalizedStorage(data={}){
 if(!data||Array.isArray(data)||typeof data!=='object')throw Error('BLOCK_SYSTEM_STORAGE_INVALID');
 const out={};for(const[key,value]of Object.entries(data)){const k=slot(quantity(key)),v=slot(quantity(value));if(out[k]!==undefined&&out[k]!==v)throw Error('BLOCK_SYSTEM_STORAGE_ALIAS_CONFLICT');out[k]=v;}return out;
}
function account(value){
 if(!value||typeof value!=='object'||Array.isArray(value))throw Error('REPLAY_ACCOUNT_SCHEMA_INVALID');
 quantity(value.balance);quantity(value.nonce??0,64);
 const code=value.code??'0x';if(typeof code!=='string'||!/^0x(?:[0-9a-fA-F]{2})*$/.test(code)||code.length>2+(1<<21))throw Error('REPLAY_ACCOUNT_CODE_INVALID');
 return{...value,code,storage:normalizedStorage(value.storage)};
}
export function prepareReplayAccounts(prestate,supplemental,parent,header,fork){
 const accounts={},origins={},gaps=[];
 try{
  if(!prestate||Array.isArray(prestate)||typeof prestate!=='object')throw Error('REPLAY_ACCOUNT_SCHEMA_INVALID');
  for(const[address,value]of Object.entries(prestate)){
   if(!/^0x[0-9a-fA-F]{40}$/.test(address))throw Error('REPLAY_ACCOUNT_ADDRESS_INVALID');const key=address.toLowerCase(),entry=account(value);
   if(accounts[key]&&JSON.stringify(accounts[key])!==JSON.stringify(entry))throw Error('REPLAY_ACCOUNT_ALIAS_CONFLICT');accounts[key]=entry;origins[key]='transaction_prestate_tracer';
  }
  const modern=['cancun','prague','osaka'].includes(fork);
  if(!modern)return{accounts,origins,gaps,block_system_state_required:false};
  if(!supplemental||supplemental.complete!==true)throw Error('BLOCK_SYSTEM_STATE_INCOMPLETE');
  if(supplemental.schema_version!=='contract-block-system-prestate-v1'||supplemental.execution_rules!==fork||supplemental.required!==true||
   !/^0x[0-9a-f]{64}$/i.test(parent.hash||'')||supplemental.parent_block_hash?.toLowerCase()!==parent.hash.toLowerCase()||header.parentHash?.toLowerCase()!==parent.hash.toLowerCase()||
   quantity(header.number,64)!==quantity(parent.number,64)+1n||quantity(supplemental.parent_block_number,64)!==quantity(parent.number,64))throw Error('BLOCK_SYSTEM_PARENT_IDENTITY_MISMATCH');
  const clock=quantity(header.timestamp,64)%WINDOW,required=new Map([[BEACON_ADDRESS,[clock,clock+WINDOW]],[SYSTEM_ADDRESS,[]]]);
  if(['prague','osaka'].includes(fork)){
   required.set(HISTORY_ADDRESS,[(quantity(header.number,64)-1n)%WINDOW]);required.set(WITHDRAWAL_ADDRESS,[0n,1n,2n,3n]);required.set(CONSOLIDATION_ADDRESS,[0n,1n,2n,3n]);
  }
  const raw=supplemental.accounts;
  if(!raw||typeof raw!=='object'||Array.isArray(raw)||Object.keys(raw).length!==required.size)throw Error('BLOCK_SYSTEM_ACCOUNT_SET_MISMATCH');
  for(const[address,keys]of required){
   const entry=account(raw[address]);
   if([WITHDRAWAL_ADDRESS,CONSOLIDATION_ADDRESS].includes(address)){
    const head=quantity(entry.storage[slot(2n)]),tail=quantity(entry.storage[slot(3n)]);if(tail<head)throw Error('BLOCK_SYSTEM_QUEUE_POINTERS_INVALID');
    const words=address===WITHDRAWAL_ADDRESS?3n:4n,max=address===WITHDRAWAL_ADDRESS?16n:2n,count=tail-head>max?max:tail-head;
    for(let i=0n;i<count;i++)for(let j=0n;j<words;j++){const key=4n+(head+i)*words+j;if(key>=WORD)throw Error('BLOCK_SYSTEM_QUEUE_SLOT_OVERFLOW');keys.push(key);}
   }
   if(Object.keys(entry.storage).length!==keys.length||keys.some(k=>entry.storage[slot(k)]===undefined))throw Error('BLOCK_SYSTEM_STORAGE_SET_MISMATCH');
   const original=accounts[address];
   if(!original){accounts[address]=entry;origins[address]='observed_parent_protocol_state_for_vm_block_boundary';}
   else for(const[key,value]of Object.entries(entry.storage))if(original.storage[key]===undefined){original.storage[key]=value;origins[address]='transaction_prestate_with_observed_parent_support_words';}
  }
 }catch(error){gaps.push(String(error.message));}
 return{accounts,origins,gaps,block_system_state_required:['cancun','prague','osaka'].includes(fork)};
}
