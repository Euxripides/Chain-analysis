import {keccak_256} from '@noble/hashes/sha3.js';

const own=(o,k)=>Object.hasOwn(o,k), object=v=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const MAX_ACCOUNTS=4096,MAX_WORDS=65536,emptyHash='0x'+Buffer.from(keccak_256(new Uint8Array())).toString('hex');
function quantity(v,bits=256){
  if(typeof v==='number'&&!Number.isSafeInteger(v))throw Error('invalid_quantity');
  if(!['string','number','bigint'].includes(typeof v)||!(/^(?:0x[0-9a-f]+|[0-9]+)$/i.test(String(v))))throw Error('invalid_quantity');
  const n=BigInt(v);if(n<0n||n>=1n<<BigInt(bits))throw Error('quantity_out_of_range');return '0x'+n.toString(16);
}
function code(v){if(typeof v!=='string'||!/^0x(?:[0-9a-f]{2})*$/i.test(v))throw Error('invalid_code');return v.toLowerCase();}
function digest(v){if(typeof v!=='string'||!/^0x[0-9a-f]{64}$/i.test(v))throw Error('invalid_code_hash');return v.toLowerCase();}
const codeHash=c=>c==='0x'?'0x'+'0'.repeat(64):'0x'+Buffer.from(keccak_256(Buffer.from(c.slice(2),'hex'))).toString('hex');
function addressMap(raw){
  if(!object(raw)||Object.keys(raw).length>MAX_ACCOUNTS)throw Error('invalid_account_map');
  const out={};for(const [key,value]of Object.entries(raw)){
    const addr=key.toLowerCase();if(!/^0x[0-9a-f]{40}$/.test(addr)||own(out,addr)||!object(value))throw Error('invalid_or_duplicate_account');out[addr]=value;
  }return out;
}
function account(raw,requireBalance=false){
  if(!object(raw)||Object.keys(raw).some(k=>!['balance','nonce','code','codeHash','storage'].includes(k)))throw Error('invalid_account_fields');
  if(requireBalance&&!own(raw,'balance'))throw Error('unobserved_balance');
  const value={};if(own(raw,'balance'))value.balance=quantity(raw.balance);if(own(raw,'nonce'))value.nonce=quantity(raw.nonce,64);
  if(own(raw,'code'))value.code=code(raw.code);
  if(own(raw,'codeHash')){value.codeHash=digest(raw.codeHash);if(own(value,'code')&&![codeHash(value.code),value.code==='0x'?emptyHash:codeHash(value.code)].includes(value.codeHash))throw Error('code_hash_conflict');}
  value.storage={};if(own(raw,'storage')){
    if(!object(raw.storage)||Object.keys(raw.storage).length>MAX_WORDS)throw Error('invalid_storage');
    for(const [key,v]of Object.entries(raw.storage)){const slot=quantity(key);if(own(value.storage,slot))throw Error('duplicate_storage_alias');value.storage[slot]=quantity(v);}
  }return value;
}

// Geth diff mode prunes unchanged fields/slots and represents cleared storage by
// absence from post. The complete prestate supplies the observed before values;
// absent witness values are never invented to make a comparison pass.
export function normalizeTransactionDiff(raw,prestate){
  const effects={},gaps=[];let words=0;
  try{
    if(!object(raw)||!own(raw,'pre')||!own(raw,'post')||Object.keys(raw).some(k=>!['pre','post'].includes(k)))throw Error('state_diff_required');
    const pre=addressMap(raw.pre),post=addressMap(raw.post),observed=addressMap(prestate);
    for(const addr of [...new Set([...Object.keys(pre),...Object.keys(post)])].sort()){
      if(!own(observed,addr))throw Error('unobserved_account:'+addr);
      const full=account(observed[addr],true),before={balance:full.balance,nonce:full.nonce??'0x0',code:full.code??'0x'},
        left=own(pre,addr)?account(pre[addr]):null,right=own(post,addr)?account(post[addr]):null,changes={};
      if(left){
        for(const field of ['balance','nonce','code'])if(own(left,field)&&left[field]!==before[field])throw Error('prestate_diff_conflict:'+addr+':'+field);
        if(left.codeHash&&![codeHash(before.code),before.code==='0x'?emptyHash:codeHash(before.code)].includes(left.codeHash))throw Error('prestate_code_hash_conflict:'+addr);
      }else if(before.balance!=='0x0'||before.nonce!=='0x0'||before.code!=='0x')throw Error('nonempty_account_missing_from_diff_pre:'+addr);
      if(!right)changes.account_deleted={before:true,after:false};
      const after=right?{balance:right.balance??before.balance,nonce:right.nonce??before.nonce,code:right.code??before.code}:{balance:'0x0',nonce:'0x0',code:'0x'};
      if(right?.codeHash){
        if(!own(right,'code')&&['0x'+'0'.repeat(64),emptyHash].includes(right.codeHash))after.code='0x';
        if(![codeHash(after.code),after.code==='0x'?emptyHash:codeHash(after.code)].includes(right.codeHash))throw Error('poststate_code_hash_unresolved:'+addr);
      }
      for(const field of ['balance','nonce','code'])if(before[field]!==after[field])changes[field]={before:before[field],after:after[field]};
      const slots=[...new Set([...Object.keys(left?.storage||{}),...Object.keys(right?.storage||{})])].sort((a,b)=>BigInt(a)<BigInt(b)?-1:1);
      const storage={};for(const slot of slots){
        if(++words>MAX_WORDS)throw Error('storage_word_budget');
        if(!own(full.storage,slot))throw Error('unobserved_storage:'+addr+':'+slot);
        const old=left?.storage[slot]??'0x0',next=right?.storage[slot]??'0x0';
        if(old!==full.storage[slot])throw Error('prestate_storage_conflict:'+addr+':'+slot);
        if(old!==next)storage[slot]={before:old,after:next};
      }
      if(Object.keys(storage).length)changes.storage=storage;
      if(Object.keys(changes).length)effects[addr]=changes;
    }
  }catch(error){gaps.push(String(error.message));}
  return {complete:gaps.length===0,effects,gaps,storage_words_checked:words};
}

export function compareTransactionDiffs(original,originalPrestate,replayed,replayedPrestate){
  const expected=normalizeTransactionDiff(original,originalPrestate),actual=normalizeTransactionDiff(replayed,replayedPrestate),differences=[];
  if(expected.complete&&actual.complete)for(const addr of [...new Set([...Object.keys(expected.effects),...Object.keys(actual.effects)])].sort()){
    const left=expected.effects[addr]||{},right=actual.effects[addr]||{};
    for(const field of [...new Set([...Object.keys(left),...Object.keys(right)])].sort()){
      if(field==='storage')for(const slot of [...new Set([...Object.keys(left.storage||{}),...Object.keys(right.storage||{})])].sort()){
        if(JSON.stringify(left.storage?.[slot])!==JSON.stringify(right.storage?.[slot]))differences.push('storage:'+addr+':'+slot);
      }else if(JSON.stringify(left[field])!==JSON.stringify(right[field]))differences.push(field+':'+addr);
    }
  }
  return {complete:expected.complete&&actual.complete,matched:expected.complete&&actual.complete&&differences.length===0,
    expected,actual,differences,gaps:[...expected.gaps.map(g=>'original_state_diff:'+g),...actual.gaps.map(g=>'replayed_state_diff:'+g)]};
}
