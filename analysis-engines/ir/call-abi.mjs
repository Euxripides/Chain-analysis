import{id,unique}from'../model.mjs';import{decodeAbi,AbiFault}from'./abi.mjs';
const profiles={
 '0xa9059cbb':{signature:'transfer(address,uint256)',types:['address','uint256'],recipient:0,amount:1,sender:null},
 '0x23b872dd':{signature:'transferFrom(address,address,uint256)',types:['address','address','uint256'],recipient:1,amount:2,sender:0},
 '0x095ea7b3':{signature:'approve(address,uint256)',types:['address','uint256'],recipient:0,amount:1,sender:null},
};
const field=value=>value?{abi_value_id:value.abi_value_id,type:value.type,value:value.value,value_expression:value.value_expression||null,
 status:value.status,unresolved:value.unresolved}:null;

// Selectors supply candidate schemas only. Keep the byte-derived arguments and
// their money roles separate from target implementation and settlement proof.
export function analyzeCallAbi(result){
 for(const section of['call_argument_models','fund_call_models','abi_values'])result.sections[section]||=[];
 for(const binding of result.sections.call_input_bindings||[]){
  if(!binding.ir_id)continue;
  const method=result.sections.methods.find(m=>m.method_id===binding.method_id);if(!method)continue;
  binding.call_argument_model_ids=[];
  const cases=binding.cases?.length?binding.cases:[{bytes:[],conditions:[],unresolved:binding.unresolved||['CALL_INPUT_BYTES_UNAVAILABLE']}];
  for(const [ordinal,c]of cases.entries()){
   const prefix=c.bytes?.slice(0,4)||[],selector=prefix.length===4&&prefix.every(b=>b.kind==='constant')?'0x'+Buffer.from(prefix.map(b=>b.value)).toString('hex'):null,
    profile=profiles[selector],call_argument_model_id=id(binding.call_input_binding_id,'abi-candidate',ordinal),evidence_ids=binding.evidence_ids||[],unresolved=unique([...(binding.unresolved||[]),...(c.unresolved||[])]);
   const row={call_argument_model_id,method_id:method.method_id,ir_id:binding.ir_id,call_input_binding_id:binding.call_input_binding_id,
    memory_version_id:binding.memory_version_id,frame_id:binding.frame_id,frame_basis:binding.frame_basis,case_ordinal:ordinal,
    selector,signature_candidate:profile?.signature||null,type_basis:profile?'selector_candidate_only':'unknown',target_implementation_verified:false,
    target_expression:binding.target_expression||null,call_type:binding.call_type,conditions:c.conditions||[],evidence_ids,unresolved,
    status:profile?'partial':'untyped_bytes',abi_value_ids:[],all_execution_paths_verified:false};
   if(profile){
    try{const decoded=decodeAbi(c.bytes.slice(4),profile.types,{identity:call_argument_model_id});row.status=decoded.status;row.unresolved=unique([...unresolved,...decoded.unresolved]);row.canonical=decoded.canonical;row.argument_types=profile.types;
     row.abi_value_ids=decoded.values.map(v=>v.abi_value_id);
     for(const value of decoded.nodes)result.sections.abi_values.push({...value,method_id:method.method_id,ir_id:binding.ir_id,call_argument_model_id,
      call_input_binding_id:binding.call_input_binding_id,type_basis:'selector_candidate_only',conditions:c.conditions||[],evidence_ids});
     if(binding.call_type==='CALL'){
      const fund_call_model_id=id(call_argument_model_id,'money-role-candidate'),money={fund_call_model_id,method_id:method.method_id,ir_id:binding.ir_id,call_argument_model_id,
       call_input_binding_id:binding.call_input_binding_id,memory_version_id:binding.memory_version_id,frame_id:binding.frame_id,frame_basis:binding.frame_basis,
       kind:selector==='0x095ea7b3'?'approval_call_candidate':'transfer_call_candidate',signature_candidate:profile.signature,asset_expression:binding.target_expression,
       sender:profile.sender===null?{expression:'address(this)',basis:'CALL_caller_context'}:field(decoded.values[profile.sender]),recipient:field(decoded.values[profile.recipient]),amount:field(decoded.values[profile.amount]),
       unit:'asset_quantity_or_token_id',unit_basis:'unverified_target_interface',target_implementation_verified:false,settlement:'requires_target_and_successful_nonreverted_execution',
       status:'pending',conditions:c.conditions||[],unresolved:unique([...row.unresolved,'TARGET_INTERFACE_NOT_VERIFIED','CALL_SUCCESS_AND_RETURNDATA_NOT_BOUND','ANCESTOR_REVERSION_NOT_CHECKED']),
       evidence_ids,all_execution_paths_verified:false};
      result.sections.fund_call_models.push(money);row.fund_call_model_id=fund_call_model_id;
     }
    }catch(error){if(!(error instanceof AbiFault))throw error;row.status='partial';row.unresolved=unique([...row.unresolved,error.code]);}
   }else row.unresolved=unique([...row.unresolved,selector?'TARGET_ABI_SCHEMA_UNAVAILABLE':'CALL_SELECTOR_BYTES_UNRESOLVED']);
   result.sections.call_argument_models.push(row);binding.call_argument_model_ids.push(call_argument_model_id);
  }
 }
 for(const method of result.sections.methods){method.call_argument_model_ids=result.sections.call_argument_models.filter(r=>r.method_id===method.method_id).map(r=>r.call_argument_model_id);
  method.fund_call_model_ids=result.sections.fund_call_models.filter(r=>r.method_id===method.method_id).map(r=>r.fund_call_model_id);}
 return result;
}
