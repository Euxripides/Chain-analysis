import{createRequire}from'node:module';import{createHash}from'node:crypto';
const require=createRequire(import.meta.url);
export function scalarFlowSource(version='0.8.30'){
 const address=Number(version.split('.')[1])<5?'address':'address payable';
 return`pragma solidity ${version};contract ScalarFlow {
 modifier sendAfter(uint quantity,${address} receiver){_;receiver.transfer(quantity);}
 modifier afterCheck(bool fail){_;require(!fail);}
 function cap(uint due,uint requested,${address} to)public{uint quantity=due;if(requested>0&&requested<quantity)quantity=requested;to.transfer(quantity);}
 function capture(uint quantity,uint replacement,${address} to)public{uint saved=quantity;quantity=replacement;to.transfer(saved);}
 function tupleSwap(uint left,uint right,${address} to)public{(left,right)=(right,left);to.transfer(left*100+right);}
 function choose(bool pick,uint left,uint right,${address} to)public{uint quantity;if(pick)quantity=left;else quantity=right;to.transfer(quantity);}
 function shortAnd(bool pick,uint quantity,${address} to)public{bool ignored=pick&&((quantity=3)>0);to.transfer(quantity);}
 function shortOr(bool pick,uint quantity,${address} to)public{bool ignored=pick||((quantity=3)>0);to.transfer(quantity);}
 function modifierFrames(uint first,uint second,${address} to)public sendAfter(first,to)sendAfter(second,to){}
 function changed(uint value)internal pure returns(uint){value=7;return value;}
 function callee(uint quantity,${address} to)public{uint saved=quantity;uint result=changed(quantity);to.transfer(saved);to.transfer(result);}
 function afterReturn(bool stop,uint due,uint requested,${address} to)public{if(stop)return;uint quantity=due;if(requested<due)quantity=requested;to.transfer(quantity);}
 function postRevert(bool fail,uint quantity,${address} to)public afterCheck(fail){to.transfer(quantity);}
 }`;
}
export function scalarFlowFixture(version='0.8.30'){
 const solc=require('solc-'+version),compiler=solc.version().split('.Emscripten')[0],source=scalarFlowSource(version),
  settings={optimizer:{enabled:false},evmVersion:Number(version.split('.')[1])<5?'byzantium':Number(version.split('.')[1])<8?'istanbul':'shanghai'},
  standard={language:'Solidity',sources:{'ScalarFlow.sol':{content:source}},settings:{...settings,outputSelection:{'*':{'*':['abi','evm.deployedBytecode','evm.methodIdentifiers']}}}};
 const output=JSON.parse((solc.compileStandardWrapper||solc.compile).call(solc,JSON.stringify(standard)));
 const errors=(output.errors||[]).filter(e=>e.severity==='error');if(errors.length)throw Error(errors.map(e=>e.formattedMessage||e.message).join('\n'));
 const compiled=output.contracts['ScalarFlow.sol'].ScalarFlow,material={kind:'source',source:{language:'Solidity',files:{'ScalarFlow.sol':source},compiler_version:compiler,target:'ScalarFlow.sol:ScalarFlow',settings}},raw=JSON.stringify(material);
 return{version,compiler,material,source,bytecode:'0x'+compiled.evm.deployedBytecode.object,abi:compiled.abi,
  input:{submitted_input_json:raw,snapshot:{identity_kind:'code_only',input_kind:'source',input_sha256:createHash('sha256').update(raw).digest('hex'),state_granularity:'symbolic_state',evm_rules:'shanghai'},engines:['whatsabi','sevm','solc-ast']}};
}
