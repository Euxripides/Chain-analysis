import test from 'node:test';
import assert from 'node:assert/strict';
import { analyze } from '../analyze.mjs';
import { disassemble } from '../bytecode.mjs';
import { compileFixture } from './fixtures.mjs';
import { legacyLayout } from '../layout.mjs';
import { numericLiteral } from '../literals.mjs';

test('ownership assignment and an ambiguous transferFrom uint argument do not become ERC20 quantities',()=>{
 const result=analyze(compileFixture('OwnershipLedger')),assign=result.sections.methods.find(m=>m.name==='assign'),forward=result.sections.methods.find(m=>m.name==='forward');
 const owner=result.sections.funds.find(f=>f.method_id===assign.method_id&&f.kind==='nft_ownership_assignment');
 assert.equal(owner.token_id_expression,'tokenId');assert.equal(owner.to_expression,'recipient');assert.equal(owner.unit,'nft_units');
 const moved=result.sections.funds.find(f=>f.method_id===forward.method_id);assert.equal(moved.unit,'asset_quantity_or_token_id');assert.equal(moved.asset_standard_verified,false);
});

test('typed encoded CALL follows library parameters and snapshot immutable values without inventing encode-only transfers',()=>{
 const input=compileFixture('EncodedTokenFlow');
 const declarations=input.output.sources['Fixtures.sol'].ast.nodes.find(n=>n.name==='EncodedTokenFlow').nodes;
 const values={asset:'0x'+'a'.repeat(40),treasury:'0x'+'b'.repeat(40),fee:'137'};
 const bytes=Buffer.from(input.bytecode.slice(2),'hex');
 for(const [ast,ranges] of Object.entries(input.compiled.evm.deployedBytecode.immutableReferences)) {
   const declaration=declarations.find(n=>String(n.id)===ast),value=BigInt(values[declaration.name]).toString(16);
   for(const range of ranges)Buffer.from(value.padStart(range.length*2,'0'),'hex').copy(bytes,range.start);
 }
 input.bytecode='0x'+bytes.toString('hex');input.source.runtimeBytecode.onchainBytecode=input.bytecode;
 const result=analyze(input),pay=result.sections.methods.find(m=>m.name==='pay');
 const funds=result.sections.funds.filter(f=>f.method_id===pay.method_id);
 assert.equal(funds.length,1,JSON.stringify(result.sections.calls));
 assert.equal(funds[0].expanded_amount_expression,'amount*137/10000');
 assert.equal(funds[0].expanded_asset_expression,values.asset);
 assert.equal(funds[0].expanded_to_expression,values.treasury);
 assert.equal(funds[0].settlement,'requires_execution_evidence');
 const encoded=result.sections.methods.find(m=>m.name==='encodeOnly');
 assert.equal(encoded.fund_ids.length,0);
 assert.ok(pay.completeness.unresolved.some(g=>g.code==='ASSEMBLY_SEMANTICS_UNRESOLVED'));
});

test('verified source explains guards, fee rounding, separate recipients and configuration dependencies', () => {
  const result = analyze(compileFixture());
  const find = name => result.sections.methods.find(m => m.name === name);
  assert.equal(result.sections.engines.find(e => e.engine === 'solc-ast')?.status, 'completed', JSON.stringify(result.sections.engines));
  const redeem = find('redeem'), configure = find('configure');
  assert.ok(redeem.verified_signature);
  const guards = result.sections.permissions.filter(g => g.method_id === redeem.method_id);
  assert.ok(guards.some(g => g.condition === '!paused'));
  assert.ok(guards.some(g => g.condition.includes('block.timestamp >= unlockTime')));
  assert.ok(result.sections.expressions.some(e => e.method_id === redeem.method_id && e.op === '/' && e.text === 'amount * feeBps / 10000' && e.rounding === 'toward_zero'));
  const payments = result.sections.funds.filter(f => f.method_id === redeem.method_id);
  assert.equal(payments.length, 2);
  assert.deepEqual(payments.map(f => f.to_expression).sort(), ['msg.sender', 'recipient']);
  assert.ok(payments.every(f => f.conditions.includes('!paused')));
  const net = payments.find(f => f.to_expression === 'msg.sender');
  assert.equal(net.expanded_amount_expression, 'amount-(amount * feeBps / 10000)');
  assert.equal(net.amount_derivation_status, 'represented_source_dependencies_resolved');
  const sequence = result.sections.steps.filter(s => s.method_id === redeem.method_id);
  assert.ok(sequence.findIndex(s => s.kind === 'external_call' && s.text.includes('recipient')) < sequence.findIndex(s => s.kind === 'guard' && s.text.includes('asset.transfer(recipient')));
  assert.ok(redeem.dependency_methods.includes(configure.method_id));
  const detail=result.sections.method_details.find(d=>d.method_id===redeem.method_id);
  assert.equal(detail.asset_effects.find(f=>f.recipient==='msg.sender').amount,'amount-(amount * feeBps / 10000)');
  assert.ok(result.sections.control_effects.some(c=>c.method_id===configure.method_id&&c.affected_method_ids.includes(redeem.method_id)&&c.caller_condition_basis==='represented_caller_condition'));
  assert.ok(result.sections.permissions.some(g => g.method_id === configure.method_id && g.condition === 'msg.sender == owner'));
  const misleading = find('withdraw');
  assert.equal(misleading.fund_ids.length, 0);
  assert.ok(!misleading.behaviors.includes('代币转出调用'));
  assert.ok(result.sections.expressions.some(e => e.method_id === find('wrap').method_id && e.op === '+' && e.arithmetic === 'wrap'));
  assert.ok(result.sections.storage.some(s => s.method_id === redeem.method_id && s.key_expression === 'shares[msg.sender]' && s.slot !== null));
  assert.ok(result.sections.storage.filter(s=>s.key_expression==='shares[msg.sender]').every(s=>s.slot_role==='layout_base'));
  for (const method of result.sections.methods) for (const eid of method.evidence_ids) assert.ok(result.sections.evidence.some(e => e.evidence_id === eid));
});
test('local amount reassignment expands prior definitions and preserves conditional and compound gaps',()=>{
 const result=analyze(compileFixture('DataflowPaths'));
 const payment=name=>result.sections.funds.find(f=>f.method_id===result.sections.methods.find(m=>m.name===name).method_id);
 assert.equal(payment('reassigned').expanded_amount_expression,'((amount/2)/3)');
 assert.equal(payment('reassigned').amount_derivation_status,'represented_source_dependencies_resolved');
 assert.equal(payment('conditional').amount_derivation_status,'partial');
 assert.equal(payment('compound').amount_derivation_status,'partial');
});
test('verified compiler layout retains nested mapping keys and packed member offset',()=>{
 const result=analyze(compileFixture('PackedLedger')),method=result.sections.methods.find(m=>m.name==='balanceOf');
 const read=result.sections.storage.find(s=>s.method_id===method.method_id&&s.key_expression==='credit[holder][7].amount');
 assert.equal(read.storage_projection.scalar_type,'uint128');assert.equal(read.storage_projection.offset,16);
 assert.deepEqual(read.storage_projection.path.map(p=>p.kind),['mapping','mapping','member']);
 assert.equal(read.storage_projection.path[1].key_literal,'7');
 assert.equal(read.storage_projection.path[0].key_referenced_declaration,method.parameters[0].source_ast_id);
});
test('internal and using-for library returns preserve parameter bindings, parentheses and overflow scope',()=>{
 const result=analyze(compileFixture('HelperFlow'));
 const method=name=>result.sections.methods.find(m=>m.name===name),payment=name=>result.sections.funds.find(f=>f.method_id===method(name).method_id);
 assert.equal(payment('pay').expanded_amount_expression,'amount-(amount*rate/10000)');
 assert.equal(payment('pay').amount_derivation_status,'represented_source_dependencies_resolved');
 assert.equal(payment('wrapped').expanded_amount_expression,'((amount+1)+1)*2');
 const guard=result.sections.permissions.find(g=>g.method_id===method('configure').method_id);
 assert.equal(guard.expanded_condition,'(msg.sender)==(controller)');
 const calleeAddition=result.sections.expressions.find(e=>e.method_id===method('wrapped').method_id&&e.text==='quantity+1'&&e.op==='+');
 assert.equal(calleeAddition.arithmetic,'checked');
 assert.ok(result.sections.expressions.some(e=>e.method_id===method('wrapped').method_id&&e.text==='amount+1'&&e.op==='+'&&e.arithmetic==='wrap'));
});
test('source mismatch cannot lend verified semantics to different bytecode', () => {
  const input = compileFixture('NativeLedger');
  input.source.runtimeBytecode.onchainBytecode = '0x00';
  const result = analyze(input);
  assert.ok(result.sections.conflicts.some(c => c.reason === 'SOURCE_SNAPSHOT_CODE_MISMATCH'));
  assert.ok(result.sections.methods.every(m => !m.verified_signature));
});
test('hidden source retains native call amount, storage keys and revert guards', () => {
  const input = compileFixture('NativeLedger'); delete input.source;
  const result = analyze(input);
  assert.ok(result.sections.methods.length >= 2);
  assert.equal(result.sections.funds.filter(f => f.kind === 'native_payment').length, 1);
  assert.equal(result.sections.funds.find(f => f.kind === 'native_payment').amount_expression, '_arg0');
  assert.ok(result.sections.methods.some(m => m.parameters.some(p => p.index === 0 && p.offset === '4')));
  assert.ok(result.sections.storage.some(s => s.access === 'write'));
  assert.ok(result.sections.permissions.length > 0);
  assert.ok(result.sections.methods.every(m => !m.verified_signature));
  assert.ok(result.sections.artifacts.some(a => a.kind === 'pseudocode'));
});
test('modern instructions keep PC identity and no fake symbolic completeness', () => {
  const rows = disassemble('0x5f5c5d5e494a6001f4fd');
  assert.deepEqual(rows.map(r => r.pc), [0,1,2,3,4,5,6,8,9]);
  assert.deepEqual(rows.slice(0,6).map(r => r.mnemonic), ['PUSH0','TLOAD','TSTORE','MCOPY','BLOBHASH','BLOBBASEFEE']);
  const input = compileFixture('DelegateFixture'); delete input.source;
  const result = analyze(input);
  assert.ok(result.sections.calls.some(c => c.call_type === 'DELEGATECALL'));
  assert.equal(result.sections.funds.filter(f => f.kind === 'native_payment').length, 0);
});
test('token ledger, mint/burn and event-only counterfeit remain distinguishable',()=>{
 const result=analyze(compileFixture('LedgerToken'));const find=name=>result.sections.methods.find(m=>m.name===name);
 assert.ok(result.sections.funds.some(f=>f.method_id===find('move').method_id&&f.kind==='token_ledger_effect'&&f.amount_expression==='amount'));
 assert.ok(result.sections.funds.some(f=>f.method_id===find('issue').method_id&&f.kind==='mint'));
 assert.ok(result.sections.funds.some(f=>f.method_id===find('destroy').method_id&&f.kind==='burn'));
 assert.equal(result.sections.funds.filter(f=>f.method_id===find('counterfeit').method_id).length,0);
 assert.equal(find('move').parameters[1].unit,'token_raw');
});
test('terminal paths, repeated modifier bodies and post-return modifiers retain execution order',()=>{
 const result=analyze(compileFixture('Reachability'));const find=name=>result.sections.methods.find(m=>m.name===name);
 assert.equal(find('dead').fund_ids.length,0,'unreachable payment was invented');
 const branch=result.sections.funds.find(f=>f.method_id===find('branch').method_id);
 assert.ok(branch.conditions.includes('!(skip)'));
 const repeated=result.sections.funds.filter(f=>f.method_id===find('repeated').method_id);
 assert.equal(repeated.length,2);assert.equal(new Set(repeated.map(f=>f.fund_id)).size,2);
 const returned=result.sections.steps.filter(s=>s.method_id===find('returned').method_id);
 assert.ok(returned.some(s=>s.text==='执行 count++'));
 assert.ok(!returned.some(s=>s.text.includes('count=123')));
});
test('legacy layout packs inherited primitives, reserves mapping slots and stops at unknown aggregate',()=>{
 const variable=(id,name,type)=>({nodeType:'VariableDeclaration',stateVariable:true,id,name,type});
 const base={nodes:[variable(1,'owner','address'),variable(2,'fee','uint16'),variable(3,'paused','bool')]};
 const main={nodes:[variable(4,'credit','mapping(address => uint256)'),variable(5,'x','uint256'),variable(6,'s','struct X.S'),variable(7,'later','uint256')]};
 const layout=legacyLayout([main,base],node=>node.type);
 assert.deepEqual(layout.storage.map(v=>[v.label,v.slot,v.offset]),[['owner','0',0],['fee','0',20],['paused','0',22],['credit','1',0],['x','2',0]]);
 assert.equal(layout.unresolved.length,2);
});
test('legacy storage reference suffix preserves string and dynamic array slot allocation',()=>{
 const types=['string storage ref','bytes storage ref','uint8','mapping(address => uint256)','uint16[] storage ref'];
 const contract={nodes:types.map((type,id)=>({nodeType:'VariableDeclaration',stateVariable:true,id,name:'v'+id,type}))};
 const layout=legacyLayout([contract],node=>node.type);
 assert.deepEqual(layout.storage.map(v=>[v.slot,v.offset]),[['0',0],['1',0],['2',0],['3',0],['4',0]]);
 assert.deepEqual(layout.unresolved,[]);
});
test('verified source assembly keeps bytecode delegate evidence under the same fallback identity',()=>{
 const result=analyze(compileFixture('DelegateFixture'));
 assert.ok(result.sections.calls.some(c=>c.call_type==='DELEGATECALL'));
 assert.equal(result.sections.funds.filter(f=>f.kind==='native_payment').length,0);
 assert.equal(new Set(result.sections.methods.map(m=>m.method_id)).size,result.sections.methods.length);
 assert.ok(result.sections.methods.some(m=>m.completeness.basis==='mixed_source_and_bytecode'&&!m.completeness.complete));
});
test('currency and time literals stay exact without floating point',()=>{
 assert.equal(numericLiteral('1.5','ether').value,'1500000000000000000');
 assert.equal(numericLiteral('1e77',null).value,'1'+'0'.repeat(77));
 assert.equal(numericLiteral('2','days').value,'172800');
 assert.equal(numericLiteral('0.1',null).status,'exact_rational');
});
test('internal argument guards and direct monetary units propagate without confusing ether scaling',()=>{
 const result=analyze(compileFixture('ArgumentUnits'));const pay=result.sections.methods.find(m=>m.name==='pay'),whole=result.sections.methods.find(m=>m.name==='whole');
 assert.equal(pay.parameters[1].unit,'wei');
 assert.ok(pay.parameters[1].constraints.some(c=>c.condition==='value>0'));
 assert.equal(whole.parameters[1].unit,null,'ether input mislabeled as raw wei');
 assert.ok(result.sections.expressions.some(e=>e.subdenomination==='ether'&&e.value==='1000000000000000000'));
 const remove=result.sections.methods.find(m=>m.name==='remove');
 assert.ok(result.sections.funds.some(f=>f.method_id===remove.method_id&&f.kind==='selfdestruct_balance_transfer'));
 assert.ok(!result.sections.funds.some(f=>f.method_id===remove.method_id&&f.amount_expression==='999'));
});
