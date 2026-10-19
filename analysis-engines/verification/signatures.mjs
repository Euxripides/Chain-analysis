import {id,unique} from '../model.mjs';
import {scalarType,asInt} from './terms.mjs';
import {and,or,obligation} from './solver.mjs';
import {literalBytes,hashBytes,encodeABI} from './bytes.mjs';

const PERMIT_TYPE='Permit(address owner,address spender,uint256 value,uint256 nonce,uint256 deadline)';
const DOMAIN_TYPE='EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)';
const value=(type,term)=>({...scalarType({type}),term:String(term)});
const literalHash=(text,terms)=>hashBytes(literalBytes({value:text}),terms);
function mapping(ctx,accessor,keys){
 let node=accessor?.node,indices=accessor?.namespace_path?.keys;
 if(!indices){indices=[];while(node&&node.nodeType!=='Identifier'){
  if(node.nodeType==='IndexAccess')indices.unshift(node.indexExpression);node=node.baseExpression||node.expression;
 }}
 const root=accessor?.namespace_path?.root??node?.referencedDeclaration,item=ctx.layout.storage.find(s=>s.astId===root);let type=ctx.layout.types?.[item?.type];
 if(accessor?.fn&&(indices.length!==keys||indices.some((key,i)=>key?.nodeType!=='Identifier'||key.referencedDeclaration!==accessor.fn.parameters.parameters[i]?.id)))return null;
 for(let i=0;i<keys;i++){if(type?.encoding!=='mapping'||!/^address(?: payable)?$/.test(ctx.layout.types?.[type.key]?.label||''))return null;type=ctx.layout.types?.[type.value];}
 return /^uint\d*$/.test(type?.label||'')?root:null;
}
function readAccessor(t,accessor,args,storage=t.initial){
 const state=t.initialState();state.storage=storage;
 if(accessor.fn){const rows=t.call(accessor.fn,args,state);if(rows.length!==1||rows[0].values.length!==1||rows[0].state.storage!==storage)throw new Error('SIGNATURE_GETTER_NOT_SINGLE_READONLY_VALUE');return rows[0].values[0];}
 let node=accessor.node;
 for(const [i,arg]of args.entries()){
  const ref=-1000000000-i;state.locals.set(ref,arg);node={nodeType:'IndexAccess',baseExpression:node,indexExpression:{nodeType:'Identifier',name:'policy_key',referencedDeclaration:ref}};
 }
 return t.expr(node,state);
}

// These are universal source obligations for the identified entry. Concrete
// ECDSA behavior is checked separately against the local EVM. The symbolic
// precompile deliberately adds no unforgeability or general hash injectivity.
export function signaturePolicies(result,ctx,transitions,getter){
 const models=[],queries=[],nonceModels=new Set();
 function nonceMonotonicity(accessor,nonceRoot,permitModel){
  if(nonceModels.has(nonceRoot))return;nonceModels.add(nonceRoot);
  const m={model_id:id(result.snapshot.code_hash,result.snapshot.storage_address,'permit-nonce-monotonicity',nonceRoot),kind:'permit_nonce_monotonicity',
   scope:'all_mutating_runtime_entries_preserve_nondecreasing_nonce_for_every_modified_account',
   proposition:'nonce(account)@exit >= nonce(account)@entry for every account whose nonce storage may change',
   code_hash:permitModel.code_hash,code_address:permitModel.code_address,storage_address:permitModel.storage_address,
   nonce_state_ast_id:nonceRoot,permit_model_id:permitModel.model_id,
   assumptions:unique(transitions.flatMap(x=>x.model.assumptions||[])),
   evidence_ids:unique(transitions.flatMap(x=>x.model.evidence_ids||[])),obligations:[],unresolved:[],
   entry_coverage:transitions.map(x=>({method_id:x.model.method_id,complete:x.model.complete,unresolved:x.model.unresolved})),
   comparison_scope:'final_successful_entry_states_with_atomic_reverts',whole_authorization_proved:false};
  models.push(m);
  for(const transition of transitions){
   if(!transition.model.complete){m.unresolved.push('ENTRY_TRANSITION_INCOMPLETE:'+transition.model.method_id);continue;}
   try{
    const violations=[];
    for(const path of transition.model.paths){
     if(path.writes.some(w=>w.root_ast_id===null||w.root_ast_id===undefined))throw Error('NONCE_UNCLASSIFIED_RAW_STORAGE_WRITE');
     const accounts=new Set();
     for(const write of path.writes.filter(w=>w.root_ast_id===nonceRoot)){
      if(write.keys?.length!==1||!write.keys[0]||write.fields?.length)throw Error('NONCE_WRITE_ACCOUNT_IDENTITY_UNAVAILABLE');accounts.add(write.keys[0]);
     }
     for(const key of accounts){
      const account=value('address',key),before=readAccessor(transition,accessor,[account]),after=readAccessor(transition,accessor,[account],path.storage_after);
      violations.push(and([path.condition,'(< '+asInt(after)+' '+asInt(before)+')']));
     }
    }
    const local={...m,method_id:transition.model.method_id,evidence_ids:transition.model.evidence_ids},query=obligation(local,'all_entries_preserve_nonce_monotonicity',transition.terms.smt(),transition.terms.domain(),or(violations),transition.terms.inputs);
    query.transition=transition;queries.push(query);
   }catch(error){m.unresolved.push(String(error.message)+':'+transition.model.method_id);}
  }
 }
 for(const t of transitions){
  const fn=ctx.functions.find(f=>ctx.methodByAST.get(f.id)?.method_id===t.model.method_id),method=ctx.methodByAST.get(fn?.id),params=fn?.parameters?.parameters||[];
  if(method?.signature!=='permit(address,address,uint256,uint256,uint8,bytes32,bytes32)')continue;
  const m={model_id:id(t.model.model_id,'erc2612-permit'),kind:'erc2612_permit_integrity',scope:'all_successful_paths_of_the_identified_permit_entry',
   proposition:'accepted permit binds the owner recovery to the EIP712 domain and exact allowance action, consumes one owner nonce, and respects the deadline',
   method_id:t.model.method_id,code_hash:t.model.code_hash,code_address:t.model.code_address,storage_address:t.model.storage_address,
   assumptions:unique([...t.model.assumptions,'symbolic_ecrecover_overapproximates_all_precompile_results','general_keccak_hashes_have_only_function_congruence','private_key_control_and_signature_unforgeability_are_not_proved']),
   evidence_ids:t.model.evidence_ids,obligations:[],unresolved:[],entry_coverage:[{method_id:t.model.method_id,complete:t.model.complete,unresolved:t.model.unresolved}],
   crypto_axioms_added:[],whole_authorization_proved:false};models.push(m);
  try{
   const nonceGetter=getter(ctx,'nonces',1),allowanceGetter=getter(ctx,'allowance',2),nonceRoot=mapping(ctx,nonceGetter,1),allowanceRoot=mapping(ctx,allowanceGetter,2);
   if(nonceRoot!==null)nonceMonotonicity(nonceGetter,nonceRoot,m);
   if(!t.model.complete){m.unresolved.push('PERMIT_TRANSITION_INCOMPLETE');continue;}
   if(nonceRoot===null||allowanceRoot===null||nonceRoot===allowanceRoot)throw new Error('PERMIT_NONCE_OR_ALLOWANCE_STORAGE_NOT_IDENTIFIED');
   m.nonce_state_ast_id=nonceRoot;m.allowance_state_ast_id=allowanceRoot;
   const args=params.map(p=>t.terms.variable('arg_'+p.id,scalarType(p),p.name)),[owner,spender,amount,deadline,v,r,s]=args;
   const entryNonce=readAccessor(t,nonceGetter,[owner]),clock=t.terms.inputs.find(i=>i.expression==='block.timestamp')?.name;
   if(!clock||!/^\d+$/.test(String(ctx.chainID))||!/^0x[0-9a-f]{40}$/i.test(ctx.executionAddress||t.model.storage_address||''))throw new Error('PERMIT_ENVIRONMENT_IDENTITY_UNAVAILABLE');
   const structHash=hashBytes(encodeABI([literalHash(PERMIT_TYPE,t.terms),owner,spender,amount,entryNonce,deadline]),t.terms);
   const violations={nonzero_owner:[],unexpired_deadline:[],domain_action_and_owner_recovery:[],nonce_consumed_once:[],only_owner_nonce_changes:[],signed_allowance_written:[],only_signed_allowance_location_changes:[]};
   for(const path of t.model.paths){
    if(path.writes.some(w=>w.root_ast_id===null||w.root_ast_id===undefined))throw new Error('PERMIT_UNCLASSIFIED_RAW_STORAGE_WRITE');
    const records=path.signature_recoveries||[];if(!records.length)throw new Error('PERMIT_SUCCESS_PATH_WITHOUT_RECOVERY');
    const bindings=[];
    for(const recovery of records){
     const preimage=recovery.hash_preimage,outer=preimage?.provenance?.values,domainValues=outer?.[1]?.hash_preimage?.provenance?.values;
     if(preimage?.provenance?.kind!=='abi.encodePacked'||outer?.length!==3||domainValues?.length!==5||outer[1].hash_preimage.provenance.kind!=='abi.encode')continue;
     if(!domainValues.slice(1,3).every(x=>x.type==='bytes32'&&/^\d+$/.test(x.term||'')))continue;
     const domain=hashBytes(encodeABI([literalHash(DOMAIN_TYPE,t.terms),value('bytes32',domainValues[1].term),value('bytes32',domainValues[2].term),value('uint256',ctx.chainID),value('address',BigInt(ctx.executionAddress||t.model.storage_address))]),t.terms);
     const digest=hashBytes(encodeABI([literalBytes({hexValue:'1901'}),domain,structHash],{packed:true}),t.terms);
     bindings.push(and([recovery.condition,'(= '+recovery.digest+' '+digest.term+')','(= '+recovery.recovered_address+' '+owner.term+')',
      '(= '+recovery.v+' '+v.term+')','(= '+recovery.r+' '+r.term+')','(= '+recovery.s+' '+s.term+')']));
    }
    if(!bindings.length)m.unresolved.push('EIP712_DOMAIN_PREIMAGE_NOT_REPRESENTED:'+path.path_id);
    const nonceAfter=readAccessor(t,nonceGetter,[owner],path.storage_after),allowanceAfter=readAccessor(t,allowanceGetter,[owner,spender],path.storage_after);
    violations.nonzero_owner.push(and([path.condition,'(= '+owner.term+' 0)']));
    violations.unexpired_deadline.push(and([path.condition,'(> '+clock+' '+deadline.term+')']));
    violations.domain_action_and_owner_recovery.push(and([path.condition,'(not '+or(bindings)+')']));
    violations.nonce_consumed_once.push(and([path.condition,'(distinct '+asInt(nonceAfter)+' (+ '+asInt(entryNonce)+' 1))']));
    violations.signed_allowance_written.push(and([path.condition,'(distinct '+asInt(allowanceAfter)+' '+amount.term+')']));
    for(const w of path.writes){
     if(w.root_ast_id===nonceRoot)violations.only_owner_nonce_changes.push(and([path.condition,'(distinct '+w.old_value+' '+w.new_value+')','(distinct '+w.keys[0]+' '+owner.term+')']));
     if(w.root_ast_id===allowanceRoot)violations.only_signed_allowance_location_changes.push(and([path.condition,'(distinct '+w.old_value+' '+w.new_value+')',or(['(distinct '+w.keys[0]+' '+owner.term+')','(distinct '+w.keys[1]+' '+spender.term+')'])]));
    }
   }
   for(const [kind,rows]of Object.entries(violations)){
    const query=obligation(m,'permit_'+kind,t.terms.smt(),t.terms.domain(),or(rows),t.terms.inputs);query.transition=t;queries.push(query);
   }
  }catch(error){m.unresolved.push(String(error.message));}
 }
 return {models,queries};
}
