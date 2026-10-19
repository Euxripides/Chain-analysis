import test from 'node:test';
import assert from 'node:assert/strict';
import {applyDataflow} from '../ir/dataflow.mjs';
import {emptyResult} from '../model.mjs';
import {analyze} from '../analyze.mjs';
import {createHash} from 'node:crypto';

function fixture(expressions,evidence=[]) {
 const r=emptyResult({});
 r.sections.methods.push({method_id:'method',completeness:{basis:'source'}});
 r.sections.expressions.push(...expressions.map(e=>({method_id:'method',evidence_ids:[],...e})));
 r.sections.evidence.push(...evidence);
 r.sections.funds.push({fund_id:'fund',method_id:'method',amount_expression_id:expressions.at(-1).expression_id});
 applyDataflow(r);return r.sections.funds[0].expanded_amount_expression;
}
test('conditional values preserve precedence in binary and unary formula operands',()=>{
 const price={expression_id:'price',op:'Conditional',text:'price < cap ? price : cap'};
 const text=fixture([{expression_id:'gas',text:'gas'},price,{expression_id:'amount',symbolic_format:'binary',operator:'*',operands:['gas','price']}]);
 const evaluate=new Function('gas','price','cap','return '+text);
 for(const gas of [0n,1n,21000n,9007199254740993n])for(const price of [0n,2n,100n])for(const cap of [0n,3n,17n])assert.equal(evaluate(gas,price,cap),gas*(price<cap?price:cap));
 const unary=fixture([{expression_id:'condition',text:'a || b'},{expression_id:'value',symbolic_format:'unary',operator:'!',operands:['condition']}]);
 const negate=new Function('a','b','return '+unary);
 for(const a of [true,false])for(const b of [true,false])assert.equal(negate(a,b),!(a||b));
});
test('parameter binding and source-span substitution keep a conditional as one value',()=>{
 const expressions=[{expression_id:'conditional',text:'pick ? x : y'},
  {expression_id:'bound',op:'Identifier',text:'b',binding_expression_id:'conditional',binding_kind:'parameter',evidence_ids:['child']},
  {expression_id:'amount',op:'BinaryOperation',text:'a*b',operands:['bound'],evidence_ids:['parent']}];
 const text=fixture(expressions,[{evidence_id:'child',location:{file:'X.sol',start:2,length:1}},{evidence_id:'parent',location:{file:'X.sol',start:0,length:3}}]);
 const evaluate=new Function('a','pick','x','y','return '+text);
 for(const a of [0n,3n,9007199254740993n])for(const pick of [false,true])assert.equal(evaluate(a,pick,2n,7n),a*(pick?2n:7n));
});
test('compiled Solidity call summaries produce independently evaluable conditional formulas',()=>{
 const material={kind:'source',source:{language:'Solidity',files:{'Quote.sol':'pragma solidity ^0.8.24; contract Quote { function limit(uint x,uint y) internal pure returns(uint){ return x<y?x:y; } function quote(uint gasUsed,uint baseGas,uint price,uint cap) external pure returns(uint){ return (gasUsed+baseGas)*limit(price,cap); }}'},compiler_version:'0.8.30+commit.73712a01',target:'Quote.sol:Quote',settings:{evmVersion:'shanghai'}}};
 const raw=JSON.stringify(material),input_sha256=createHash('sha256').update(raw).digest('hex');
 const result=analyze({submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256,state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['whatsabi','sevm','solc-ast']});
 const method=result.sections.methods.find(m=>m.name==='quote'),derived=result.sections.dataflow.find(d=>d.method_id===method.method_id&&d.kind==='entry_return_derivation');
 assert.equal(derived.status,'represented_source_dependencies_resolved');
 const evaluate=new Function('gasUsed','baseGas','price','cap','return '+derived.symbolic_expression);
 for(const gas of [0n,1n,21000n,9007199254740993n])for(const base of [0n,7n])for(const price of [0n,2n,100n])for(const cap of [0n,3n,17n])assert.equal(evaluate(gas,base,price,cap),(gas+base)*(price<cap?price:cap));
});
