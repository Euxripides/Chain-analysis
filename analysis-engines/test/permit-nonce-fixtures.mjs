import{permitSource}from'./signature-fixtures.mjs';import{erc7201Root}from'../namespace-layout.mjs';
export const appendPermitEntry=(source,extra)=>source.replace(/\}\s*$/,extra+'}');
export function unrelatedNonceSource(){
 const root='0x'+erc7201Root('example.permit.other').toString(16).padStart(64,'0');
 return namespacePermitSource({extra:`/// @custom:storage-location erc7201:example.permit.other
 struct Other{mapping(address=>uint256)nonces;}
 bytes32 constant OTHER_ROOT=${root};function other()private pure returns(Other storage $){assembly{$.slot:=OTHER_ROOT}}
 function bumpOther(address who)external{other().nonces[who]++;}`});
}
export function namespacePermitSource({aliases=false,extra=''}={}){
 const root='0x'+erc7201Root('example.permit').toString(16).padStart(64,'0'),declarations=`/// @custom:storage-location erc7201:example.permit
 struct Store{mapping(address=>uint256)nonces;mapping(address=>mapping(address=>uint256))allowances;}
 bytes32 constant ROOT=${root};function store()private pure returns(Store storage $){assembly{$.slot:=ROOT}}
 function nonces(address owner)external view returns(uint256){${aliases?'Store storage $=store();return $.nonces[owner];':'return store().nonces[owner];'}}
 function allowance(address owner,address spender)external view returns(uint256){${aliases?'Store storage $=store();return $.allowances[owner][spender];':'return store().allowances[owner][spender];'}}`;
 let source=permitSource.replace('mapping(address=>uint256)public nonces;\n mapping(address=>mapping(address=>uint256))public allowance;',declarations)
  .replace('value,nonces[owner]++','value,store().nonces[owner]++').replace('allowance[owner][spender]=value','store().allowances[owner][spender]=value');
 if(aliases)source=source.replace('require(owner!=address(0));','Store storage $=store();require(owner!=address(0));').replace('value,store().nonces[owner]++','value,$.nonces[owner]++').replace('store().allowances[owner][spender]=value','$.allowances[owner][spender]=value');
 return appendPermitEntry(source,extra);
}
