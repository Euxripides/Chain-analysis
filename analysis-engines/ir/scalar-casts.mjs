// A fixed bytes value is represented here by its N-byte unsigned integer.
// Solidity pads/truncates its right side when changing bytesN widths.
export function fixedBytesShift(from,to){
 const a=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(from||''),b=/^bytes([1-9]|[12]\d|3[0-2])$/.exec(to||'');
 return a&&b?(Number(b[1])-Number(a[1]))*8:0;
}
export function fixedBytesTerm(term,from,to){
 const bits=fixedBytesShift(from,to);return bits>0?'(* '+term+' '+(1n<<BigInt(bits))+')':bits<0?'(div '+term+' '+(1n<<BigInt(-bits))+')':term;
}
export function fixedBytesValue(value,from,to){
 const bits=fixedBytesShift(from,to);return bits>0?value<<BigInt(bits):bits<0?value>>BigInt(-bits):value;
}
