const MOD=1n<<256n,MASK=MOD-1n,SIGN=1n<<255n;
export const WORD_ARITIES=Object.freeze({add:2,sub:2,mul:2,div:2,sdiv:2,mod:2,smod:2,addmod:3,mulmod:3,exp:2,
  signextend:2,not:1,and:2,or:2,xor:2,byte:2,shl:2,shr:2,sar:2,lt:2,gt:2,slt:2,sgt:2,eq:2,iszero:1});
export function evmWord(op,values) {
  if(WORD_ARITIES[op]!==values.length)throw new Error('YUL_BUILTIN_UNSUPPORTED:'+op);
  const a=values.map(v=>BigInt(v)&MASK),[x,y,z]=a,signed=v=>v&SIGN?v-MOD:v;
  let out;
  switch(op){
    case 'add':out=x+y;break;case 'sub':out=x-y;break;case 'mul':out=x*y;break;
    case 'div':out=y===0n?0n:x/y;break;case 'mod':out=y===0n?0n:x%y;break;
    case 'sdiv':out=y===0n?0n:signed(x)/signed(y);break;case 'smod':out=y===0n?0n:signed(x)%signed(y);break;
    case 'addmod':out=z===0n?0n:(x+y)%z;break;case 'mulmod':out=z===0n?0n:(x*y)%z;break;
    case 'exp':{let b=x,e=y;out=1n;while(e){if(e&1n)out=out*b&MASK;b=b*b&MASK;e>>=1n;}break;}
    case 'not':out=~x;break;case 'and':out=x&y;break;case 'or':out=x|y;break;case 'xor':out=x^y;break;
    case 'lt':out=x<y?1n:0n;break;case 'gt':out=x>y?1n:0n;break;
    case 'slt':out=signed(x)<signed(y)?1n:0n;break;case 'sgt':out=signed(x)>signed(y)?1n:0n;break;
    case 'eq':out=x===y?1n:0n;break;case 'iszero':out=x===0n?1n:0n;break;
    case 'byte':out=x>=32n?0n:(y>>((31n-x)*8n))&255n;break;
    case 'shl':out=x>=256n?0n:y<<x;break;case 'shr':out=x>=256n?0n:y>>x;break;
    case 'sar':out=x>=256n?(signed(y)<0n?MASK:0n):signed(y)>>x;break;
    case 'signextend':{if(x>=32n){out=y;break;}const bit=x*8n+7n,mask=(1n<<(bit+1n))-1n;out=y&(1n<<bit)?y|(~mask):y&mask;break;}
  }
  return out&MASK;
}
