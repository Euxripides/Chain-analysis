import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {hash} from '../model.mjs';
const require=createRequire(import.meta.url),solc=require('solc-0.8.30');

export const sources={
 'Mechanisms.sol':{content:`pragma solidity ^0.8.24;
contract Mechanisms {
 struct Role {mapping(address=>bool) members;bytes32 admin;}
 mapping(bytes32=>Role) private roles;
 bytes32 public constant FUNDS_ROLE=bytes32(uint256(17));
 address public owner;address payable public treasury;uint256 public feeBps=125;uint256 public required=2;uint256 public approvals=2;
 constructor(){owner=msg.sender;treasury=payable(msg.sender);roles[FUNDS_ROLE].members[msg.sender]=true;}
 modifier nonzero(uint256 value){require(value>0);_;}
 modifier afterBody(){_;feeBps+=1;}
 modifier onlyRole(){require(hasRole(FUNDS_ROLE,msg.sender));_;}
 function hasRole(bytes32 role,address account) public view returns(bool){return roles[role].members[account];}
 function grant(address account,bool enabled) external {require(msg.sender==owner);roles[FUNDS_ROLE].members[account]=enabled;}
 function configure(uint256 rate,address payable target) external {require(msg.sender==owner);require(rate<=1000);feeBps=rate;treasury=target;}
 function setRequired(uint256 count) external {require(msg.sender==owner);required=count;}
 function split(uint256 amount) internal view returns(uint256 net,uint256 fee){fee=amount*feeBps/10000;net=amount-fee;}
 function quote(uint256 amount) external view returns(uint256,uint256){return split(amount);}
 function selected(uint256 amount) external view returns(uint256){(,uint256 fee)=split(amount);return fee;}
 function tupleSwap(uint256 amount) external pure returns(uint256,uint256){return swapping(amount);}
 function narrow(uint256 amount) external pure returns(uint256){return uint8(amount);}
 function swapping(uint256 amount) internal pure returns(uint256 first,uint256 second){first=amount;second=amount+3;(first,second)=(second,first);}
 function constantLoop(uint256 amount) external pure returns(uint256){return fixedStages(amount);}
 function fixedStages(uint256 amount) internal pure returns(uint256){uint256 output=amount;for(uint256 i=0;i<3;i++)output=output/3+7;return output;}
 function fixedWhile(uint256 amount) external pure returns(uint256){return whileStages(amount);}
 function whileStages(uint256 amount) internal pure returns(uint256 output){output=amount;uint256 i;while(i<3){output=output/3+7;i++;}}
 function fixedDo(uint256 amount) external pure returns(uint256){return doStages(amount);}
 function doStages(uint256 amount) internal pure returns(uint256 output){output=amount;uint256 i;do{output+=2;i++;}while(i<2);}
 function flowControl(uint256 amount) external pure returns(uint256){return controlled(amount);}
 function controlled(uint256 amount) internal pure returns(uint256 output){output=amount;for(uint256 i=0;i<6;i++){if(i==1)continue;if(i==4)break;output+=i;}}
 function descending(uint256 amount) external pure returns(uint256){return down(amount);}
 function down(uint256 amount) internal pure returns(uint256 output){output=amount;for(uint256 i=3;i>0;i--)output+=i;}
 function loopLimit(uint256 amount) external pure returns(uint256){return tooMany(amount);}
 function tooMany(uint256 amount) internal pure returns(uint256 output){output=amount;for(uint256 i=0;i<17;i++)output+=i;}
 function dynamic(uint256 amount) external pure returns(uint256){return unknown(amount);}
 function unknown(uint256 amount) internal pure returns(uint256 output){for(uint256 i=0;i<amount;i++)output+=i;}
 function modified(uint256 amount) external pure returns(uint256){return checkedValue(amount);}
 function checkedValue(uint256 amount) internal pure nonzero(amount) returns(uint256){return amount/3;}
 function guardedPayment(uint256 amount) external {payable(msg.sender).transfer(checkedValue(amount));}
 function postModified(uint256 amount) external afterBody returns(uint256){return amount;}
 function pay(uint256 amount) external onlyRole {require(approvals>=required);(uint256 net,uint256 fee)=split(amount);treasury.transfer(fee);payable(msg.sender).transfer(net);}
 function deny(uint256 amount) external {require(!hasRole(FUNDS_ROLE,msg.sender));payable(msg.sender).transfer(amount);}
 receive() external payable{}
}`},
 'Vault.sol':{content:`pragma solidity ^0.8.24;
interface Token {function transfer(address,uint256) external returns(bool);function transferFrom(address,address,uint256) external returns(bool);}
contract Vault {
 Token private token;uint256 public totalSupply;uint256 private managed;uint256 public feeBps;mapping(address=>uint256) private balances;
 address public owner;
 constructor(address assetToken){token=Token(assetToken);owner=msg.sender;feeBps=125;}
 function asset() external view returns(address){return address(token);}
 function totalAssets() public view returns(uint256){return managed;}
 function balanceOf(address account) external view returns(uint256){return balances[account];}
 function configure(uint256 rate) external {require(msg.sender==owner);require(rate<=1000);feeBps=rate;}
 function convertToShares(uint256 assets) public view returns(uint256){return assets*(totalSupply+1)/(managed+1);}
 function convertToAssets(uint256 shares) public view returns(uint256){return shares*(managed+1)/(totalSupply+1);}
 function previewDeposit(uint256 assets) public view returns(uint256){(uint256 net,)=deduct(assets);return convertToShares(net);}
 function previewRedeem(uint256 shares) public view returns(uint256){(uint256 net,)=deduct(convertToAssets(shares));return net;}
 function deduct(uint256 assets) internal view returns(uint256 net,uint256 fee){fee=assets*feeBps/10000;net=assets-fee;}
 function deposit(uint256 assets,address receiver) external returns(uint256 shares){shares=previewDeposit(assets);require(shares>0);require(token.transferFrom(msg.sender,address(this),assets));balances[receiver]+=shares;totalSupply+=shares;managed+=assets;}
 function redeem(uint256 shares,address receiver,address account) external returns(uint256 assets){require(msg.sender==account);require(balances[account]>=shares);assets=previewRedeem(shares);balances[account]-=shares;totalSupply-=shares;managed-=assets;require(token.transfer(receiver,assets));}
}
contract FixtureToken {
 mapping(address=>uint256) public balanceOf;mapping(address=>mapping(address=>uint256)) public allowance;uint256 public totalSupply;
 event Transfer(address indexed from,address indexed to,uint256 amount);
 function mint(address account,uint256 value) external {balanceOf[account]+=value;totalSupply+=value;emit Transfer(address(0),account,value);}
 function approve(address to,uint256 amount) external returns(bool){allowance[msg.sender][to]=amount;return true;}
 function transfer(address to,uint256 amount) external returns(bool){balanceOf[msg.sender]-=amount;balanceOf[to]+=amount;emit Transfer(msg.sender,to,amount);return true;}
 function transferFrom(address from,address to,uint256 amount) external returns(bool){allowance[from][msg.sender]-=amount;balanceOf[from]-=amount;balanceOf[to]+=amount;emit Transfer(from,to,amount);return true;}
}`},
 'EncodedCalls.sol':{content:`pragma solidity ^0.8.24;
interface T {function transfer(address,uint256) external returns(bool);}
contract EncodedCalls {
 bytes4 private constant SELECTOR=bytes4(keccak256(bytes("transfer(address,uint256)")));
 function encoded(address token,address to,uint256 amount) external {(bool ok,)=token.call(abi.encodeWithSelector(SELECTOR,to,amount));require(ok);}
 function signatureEncoding(address token,address to,uint256 amount) external {(bool ok,)=token.call(abi.encodeWithSignature("transfer(address,uint256)",to,amount));require(ok);}
 function typedEncoding(address token,address to,uint256 amount) external {(bool ok,)=token.call(abi.encodeCall(T.transfer,(to,amount)));require(ok);}
 function wrongSelector(address token,address to,uint256 amount) external {(bool ok,)=token.call(abi.encodeWithSelector(bytes4(0x12345678),to,amount));require(ok);}
 function wrongAmount(address token,address to,uint256[2] memory amount) external {(bool ok,)=token.call(abi.encodeWithSelector(SELECTOR,to,amount));require(ok);}
 function encodeOnly(address to,uint256 amount) external pure returns(bytes memory){return abi.encodeWithSelector(SELECTOR,to,amount);}
}`},
 'Quotes.sol':{content:`pragma solidity ^0.8.24;
contract Quotes {
 function amountOut(uint256 input,uint256 reserveIn,uint256 reserveOut) external pure returns(uint256){require(input>0&&reserveIn>0&&reserveOut>0);uint256 adjusted=input*997;return adjusted*reserveOut/(reserveIn*1000+adjusted);}
 function amountIn(uint256 output,uint256 reserveIn,uint256 reserveOut) external pure returns(uint256){require(output>0&&reserveIn>0&&reserveOut>output);return reserveIn*output*1000/((reserveOut-output)*997)+1;}
 function conditionalFee(uint256 amount,bool exempt) external pure returns(uint256,uint256){if(exempt)return(amount,0);uint256 fee=amount*125/10000;return(amount-fee,fee);}
}`}
};

export function compileMechanismFixture(name='Mechanisms',optimizer=false) {
 const file=name==='Mechanisms'?'Mechanisms.sol':name==='Quotes'?'Quotes.sol':name==='EncodedCalls'?'EncodedCalls.sol':'Vault.sol';
 const std={language:'Solidity',sources:{[file]:sources[file]},settings:{optimizer:{enabled:optimizer,runs:200},evmVersion:'shanghai',outputSelection:{'*':{'*':['abi','evm.bytecode','evm.deployedBytecode','evm.methodIdentifiers','storageLayout']}}}};
 const output=JSON.parse(solc.compile(JSON.stringify(std)));assert.deepEqual((output.errors||[]).filter(e=>e.severity==='error'),[]);
 const compiled=output.contracts[file][name],bytecode='0x'+compiled.evm.deployedBytecode.object;
 return {compiled,bytecode,snapshot:{chain:'ethereum',chain_id:'1',block_number:1,block_hash:'0x'+'1'.repeat(64),code_hash:hash(bytecode),code_address:'0x'+'2'.repeat(40),storage_address:'0x'+'2'.repeat(40),entry_address:'0x'+'2'.repeat(40),state_granularity:'block_end'},
  engines:['whatsabi','sevm','solc-ast'],source:{stdJsonInput:std,compilation:{compilerVersion:solc.version(),fullyQualifiedName:file+':'+name},runtimeMatch:'exact_match',match:'exact_match',runtimeBytecode:{onchainBytecode:bytecode,recompiledBytecode:bytecode},abi:compiled.abi}};
}
