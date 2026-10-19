import test from'node:test';import assert from'node:assert/strict';import{createHash}from'node:crypto';
import{applyLedgerRules}from'../rules/ledger.mjs';import{analyze}from'../analyze.mjs';

function ledgerFixture({separate=false,otherFrame=false,sharedBudget=false,oppositeBranch=false}={}){
 const methods=[{method_id:'getter',signature:'balanceOf(address)',verified_signature:true,fund_ids:[]},{method_id:'credit',fund_ids:[]}],
  unknown={expression_id:'opaque',op:'FunctionCall',text:'producer()',path:'entry',unresolved_return_value:'INTERNAL_RETURN_REQUIRE_MESSAGE_EFFECT_UNSUPPORTED'},
  expressions=[unknown,{...unknown,expression_id:'another-opaque'},
   {expression_id:'write-value',op:'Identifier',text:'value',path:'entry',binding_expression_id:'opaque'},
   {expression_id:'event-value',op:'Identifier',text:'value',path:otherFrame?'entry/call:other':'entry',binding_expression_id:separate?'another-opaque':'opaque'},
   {expression_id:'zero',text:'address(0)'},{expression_id:'recipient',text:'who'}],
  storage=[{storage_id:'get',method_id:'getter',access:'read',state_id:'units',variable_type:'mapping(address => uint256)'},
   {storage_id:'write',method_id:'credit',access:'write',state_id:'units',operation:'+=',key_expression:'holdings[who]',value_expression:'value',value_expression_id:'write-value',conditions:[],evidence_ids:[]}],
  steps=[{step_id:'event',method_id:'credit',kind:'event',event_signature:'Transfer(address,address,uint256)',event_indexed:[true,true,false],event_arguments:['address(0)','who','value'],event_argument_ids:['zero','recipient','event-value'],path:otherFrame?'entry/call:other':'entry',conditions:[],evidence_ids:[]}];
 const result={snapshot:{storage_address:'contract-under-analysis'},sections:{methods,expressions,storage,steps,funds:[]}};
 if(sharedBudget){result.sections.source_scalar_versions=[{source_scalar_version_id:'budget',declaration_id:null,frame_id:null,unresolved:['SOURCE_SCALAR_VERSION_BUDGET']}];
  for(const e of expressions.filter(e=>['write-value','event-value'].includes(e.expression_id))){e.binding_expression_id=null;e.source_scalar_binding_unavailable=true;e.source_scalar_version_id='budget';}}
 if(oppositeBranch){storage[1].conditions=['flag'];steps[0].conditions=['!(flag)'];}
 return result;
}

test('ledger matching retains a shared opaque value and rejects different results or frames',()=>{
 const same=ledgerFixture();applyLedgerRules(same);assert.equal(same.sections.funds.length,1);assert.equal(same.sections.funds[0].kind,'mint');
 assert.equal(same.sections.funds[0].ledger_effects[0].value_identity_basis,'shared_opaque_value_in_source_frame');
 for(const options of[{separate:true},{otherFrame:true},{sharedBudget:true},{oppositeBranch:true}]){const r=ledgerFixture(options);applyLedgerRules(r);assert.equal(r.sections.funds.length,0);}
});

test('compiled ledger events cannot correlate separate external return values by spelling',()=>{
 const source=`pragma solidity ^0.8.24;contract Coin {
 mapping(address=>uint256)private holdings;uint256 private sequence;event Transfer(address indexed from,address indexed to,uint256 value);
 function balanceOf(address who)external view returns(uint256){return holdings[who];}
 function quote()external returns(uint256){sequence+=1;return sequence;}
 function credit(address who)external{uint256 value=this.quote();holdings[who]+=value;emit Transfer(address(0),who,value);}
 function counterfeit(address who)external{uint256 value=this.quote();holdings[who]+=value;uint256 reported=this.quote();emit Transfer(address(0),who,reported);}
 function computedCounterfeit(address who)external{uint256 value=this.quote()+1;holdings[who]+=value;uint256 reported=this.quote()+1;emit Transfer(address(0),who,reported);}
 }`,material={kind:'source',source:{language:'Solidity',files:{'Coin.sol':source},compiler_version:'0.8.30+commit.73712a01',target:'Coin.sol:Coin',settings:{evmVersion:'shanghai'}}},raw=JSON.stringify(material),
  r=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex'),state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['whatsabi','sevm','solc-ast']});
 const funds=name=>{const m=r.sections.methods.find(m=>m.name===name);return r.sections.funds.filter(f=>f.method_id===m.method_id&&f.kind==='mint');};
 assert.equal(funds('credit').length,1);assert.equal(funds('credit')[0].asset,'address(this)');assert.equal(funds('credit')[0].asset_scope,'symbolic_self_storage');
 assert.equal(funds('counterfeit').length,0);assert.equal(funds('computedCounterfeit').length,0);
});
