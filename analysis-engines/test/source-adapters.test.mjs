import test from 'node:test';
import assert from 'node:assert/strict';
import { analyze } from '../analyze.mjs';
import { compileFixture } from './fixtures.mjs';
import { compileVyper } from '../source-vyper.mjs';
import { hash } from '../model.mjs';

const external = { skip: !process.env.CONTRACT_PYTHON };
const vault = `# pragma version 0.4.3
owner: public(address)
recipient: public(address)
fee_bps: public(uint256)
credit: public(HashMap[address, uint256])
@deploy
def __init__():
    self.owner = msg.sender
    self.fee_bps = 125
@external
@payable
def deposit():
    assert msg.value > 0
    self.credit[msg.sender] += msg.value
@external
def redeem(amount: uint256):
    assert self.credit[msg.sender] >= amount
    fee: uint256 = amount * self.fee_bps // 10000
    self.credit[msg.sender] -= amount
    send(self.recipient, fee)
    send(msg.sender, amount - fee)
@external
def configure(fee: uint256, target: address):
    assert msg.sender == self.owner
    assert fee <= 1000
    self.fee_bps = fee
    self.recipient = target
@external
def branch(skip: bool, amount: uint256):
    if skip:
        return
    send(msg.sender, amount)
@external
def literal_value() -> uint256:
    return 900719925474099312345678901234567890
`;

export function vyperFixture(content = vault) {
  const standard = { language: 'Vyper', sources: { 'Vault.vy': { content } }, settings: { evmVersion: 'shanghai' } };
  const { output } = compileVyper(standard, '0.4.3');
  const compiled = output.contracts['Vault.vy'].Vault;
  const bytecode = '0x' + compiled.evm.deployedBytecode.object.replace(/^0x/, '');
  const snapshot = { chain: 'ethereum', chain_id: '1', block_number: 1, block_hash: '0x' + '1'.repeat(64),
    code_hash: hash(bytecode), code_address: '0x' + '2'.repeat(40), storage_address: '0x' + '2'.repeat(40), entry_address: '0x' + '2'.repeat(40) };
  return { snapshot, bytecode, engines: ['whatsabi','sevm','solc-ast'], source: { stdJsonInput: standard,
    compilation: { compilerVersion: '0.4.3', fullyQualifiedName: 'Vault.vy:Vault' },
    runtimeMatch: 'exact_match', match: 'exact_match', provenance: 'local_compiler_fixture',
    runtimeBytecode: { onchainBytecode: bytecode, recompiledBytecode: bytecode }, abi: compiled.abi } };
}

test('real Slither IR resolves to verified methods and exact source locations', external, () => {
  const input = compileFixture('AnalystVault'); input.engines = ['whatsabi','sevm','solc-ast','slither'];
  input.source.stdJsonInput.settings.outputSelection={'*':{'*':['abi','evm.deployedBytecode']}};
  const result = analyze(input), engine = result.sections.engines.find(x => x.engine === 'slither');
  assert.equal(engine.status, 'completed', JSON.stringify(engine));
  assert.ok(result.sections.ir.length > 20);
  const redeem = result.sections.methods.find(x => x.name === 'redeem');
  assert.ok(redeem.ir_ids.length > 0);
  const instructions = result.sections.ir.filter(x => x.method_id === redeem.method_id);
  assert.ok(instructions.some(x => x.text.includes('10000')));
  const evidence = new Map(result.sections.evidence.map(x => [x.evidence_id, x]));
  for (const row of instructions) for (const e of row.evidence_ids) {
    assert.ok(evidence.has(e)); assert.equal(evidence.get(e).location.file, 'Fixtures.sol');
  }
});
test('Vyper internal calls bind external amounts and declared token interfaces preserve their unit',external,()=>{
 const content=`# pragma version 0.4.3
interface Asset:
    def transfer(to: address, amount: uint256) -> bool: nonpayable
token: address
@internal
def pay(payout: uint256):
    assert payout > 0
    send(msg.sender, payout)
@external
def invoke(amount: uint256):
    self.pay(amount)
@external
def move(to: address, amount: uint256):
    assert extcall Asset(self.token).transfer(to, amount)
`;
 const result=analyze(vyperFixture(content)),invoke=result.sections.methods.find(m=>m.name==='invoke'),move=result.sections.methods.find(m=>m.name==='move');
 assert.ok(result.sections.funds.some(f=>f.method_id===invoke.method_id&&f.expanded_amount_expression==='amount'));
 const fund=result.sections.funds.find(f=>f.method_id===move.method_id&&f.kind==='token_transfer_intent');
 assert.ok(fund,JSON.stringify(result.sections.engines));assert.equal(fund.unit,'token_raw');assert.equal(fund.asset,'self.token');
});

test('Vyper verified source retains fee, caller authorization, ledger and payment order', external, () => {
  const result = analyze(vyperFixture()), engine = result.sections.engines.find(x => x.engine === 'vyper-ast');
  assert.equal(engine.status, 'completed', JSON.stringify(result.sections.engines));
  const redeem = result.sections.methods.find(x => x.name === 'redeem');
  assert.ok(redeem.verified_signature);
  const funds = result.sections.funds.filter(x => x.method_id === redeem.method_id);
  assert.deepEqual(funds.map(x => [x.to_expression,x.amount_expression]), [['self.recipient','fee'],['msg.sender','amount - fee']]);
  assert.equal(funds[1].expanded_amount_expression, 'amount - (amount * self.fee_bps // 10000)');
  assert.ok(result.sections.expressions.some(x => x.method_id === redeem.method_id && x.text === 'amount * self.fee_bps // 10000'));
  const configure = result.sections.methods.find(x => x.name === 'configure');
  assert.ok(result.sections.permissions.some(x => x.method_id === configure.method_id && x.condition === 'msg.sender == self.owner'));
  assert.ok(redeem.dependency_methods.includes(configure.method_id));
  assert.ok(result.sections.storage.some(x => x.method_id === redeem.method_id && x.key_expression === 'self.credit[msg.sender]' && x.access === 'write'));
  const branch = result.sections.methods.find(x => x.name === 'branch');
  assert.ok(result.sections.funds.find(x => x.method_id === branch.method_id).conditions.includes('!(skip)'));
  assert.ok(result.sections.expressions.some(x => x.value === '900719925474099312345678901234567890'));
});

test('Vyper mismatched source does not inherit verified names or source payment claims', external, () => {
  const input = vyperFixture(); input.source.runtimeBytecode.onchainBytecode = '0x00';
  const result = analyze(input);
  assert.ok(result.sections.conflicts.some(x => x.reason === 'SOURCE_SNAPSHOT_CODE_MISMATCH'));
  assert.ok(result.sections.methods.every(x => !x.verified_signature));
});

test('Vyper void transferFrom retains an uncertain quantity or NFT identifier',external,()=>{
 const content=`# pragma version 0.4.3
interface Asset:
    def transferFrom(sender: address, recipient: address, token_id: uint256): nonpayable
@external
def move(asset: address, sender: address, recipient: address, token_id: uint256):
    extcall Asset(asset).transferFrom(sender, recipient, token_id)
`;
 const result=analyze(vyperFixture(content));assert.ok(result.sections.funds.some(f=>f.unit==='asset_quantity_or_token_id'&&f.asset_standard_verified===false));
});
