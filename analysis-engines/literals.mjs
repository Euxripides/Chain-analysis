const scales={wei:1n,gwei:1000000000n,ether:1000000000000000000n,seconds:1n,minutes:60n,hours:3600n,days:86400n,weeks:604800n,years:31536000n};
export function numericLiteral(text,unit) {
  const input=text.replaceAll('_',''),scale=unit?scales[unit]:1n;
  if(scale==null)return {value:null,status:'unknown_subdenomination'};
  if(/^0x[\da-f]+$/i.test(input))return {value:(BigInt(input)*scale).toString(),status:'exact_integer'};
  const match=/^(\d*)(?:\.(\d*))?(?:e([+-]?\d+))?$/i.exec(input);
  if(!match||!match[1]&&!match[2])return {value:null,status:'numeric_literal_unresolved'};
  const exponent=Number(match[3]||0);if(!Number.isSafeInteger(exponent)||Math.abs(exponent)>1000)return {value:null,status:'literal_exponent_budget'};
  let numerator=BigInt((match[1]||'0')+(match[2]||''))*scale,denominator=10n**BigInt((match[2]||'').length);
  if(exponent>=0)numerator*=10n**BigInt(exponent);else denominator*=10n**BigInt(-exponent);
  return numerator%denominator===0n?{value:(numerator/denominator).toString(),status:'exact_integer'}:{value:null,numerator:numerator.toString(),denominator:denominator.toString(),status:'exact_rational'};
}
