import {unique}from'../model.mjs';

// Relations carry execution order. CSV record order and numeric label sorting
// are not control-flow edges, especially after Gigahorse clones a statement.
export function buildTacOrder(rows,blocks,functionEntries=[]) {
 const halt=new Set(['STOP','RETURN','REVERT','INVALID','SELFDESTRUCT']),control=new Set([...halt,'JUMP','JUMPI']);
 const byStatement=new Map(),byBlock=new Map(),gaps=[];
 for(const row of rows){if(byStatement.has(row.statement)){gaps.push({code:'TAC_DUPLICATE_STATEMENT',statement:row.statement});continue;}
  byStatement.set(row.statement,row);const list=byBlock.get(row.block)||[];list.push(row);byBlock.set(row.block,list);}
 const cfg=new Map(blocks.map(b=>[b.block,b])),entryByBlock=new Map(),exitsByBlock=new Map();
 for(const[block,members]of byBlock){const incoming=new Set(),outgoing=new Set();
  for(const row of members)if(!control.has(row.op))for(const next of row.statement_successors||[])if(byStatement.get(next)?.block===block){incoming.add(next);outgoing.add(row.statement);}
  const first=members.filter(r=>!incoming.has(r.statement)).map(r=>r.statement),last=members.filter(r=>!outgoing.has(r.statement)).map(r=>r.statement);
  entryByBlock.set(block,first.length?first:members.map(r=>r.statement));exitsByBlock.set(block,last.length?last:members.map(r=>r.statement));
  if(first.length!==1||last.length!==1)gaps.push({code:'TAC_BLOCK_ORDER_AMBIGUOUS',block,entries:first,exits:last});
 }
 function entries(block,seen=new Set()){
  if(entryByBlock.has(block))return entryByBlock.get(block);
  if(seen.has(block)||seen.size>=256){gaps.push({code:'TAC_EMPTY_BLOCK_CYCLE_OR_BUDGET',block});return[];}
  const next=cfg.get(block)?.successors;
  if(!next?.length){gaps.push({code:'TAC_BLOCK_STATEMENTS_UNAVAILABLE',block});return[];}
  return unique(next.flatMap(b=>entries(b,new Set([...seen,block]))));
 }
 const successors=new Map(rows.map(r=>[r.statement,new Set()])),predecessors=new Map(rows.map(r=>[r.statement,new Set()])),sources=new Map();
 const add=(from,to,basis)=>{if(!byStatement.has(from)||!byStatement.has(to))return;
  successors.get(from).add(to);predecessors.get(to).add(from);const key=from+'\0'+to;sources.set(key,unique([...(sources.get(key)||[]),basis]));};
 // TAC_Statement_Next is lexical ordering. It can cross RETURN or bypass an
 // inlined private callee. Only its within-block, non-terminating links execute.
 for(const row of rows)if(!control.has(row.op))for(const next of row.statement_successors||[])if(byStatement.get(next)?.block===row.block)add(row.statement,next,'TAC_Statement_Next');
 for(const block of blocks)for(const from of exitsByBlock.get(block.block)||[]){
  if(halt.has(byStatement.get(from).op))continue;
  for(const next of block.successors||[])for(const to of entries(next))add(from,to,'LocalBlockEdge');
 }
 const entryStatements=unique(functionEntries.flatMap(b=>entries(b)));
 if(!entryStatements.length)gaps.push({code:'TAC_FUNCTION_ENTRY_UNAVAILABLE',function_entries:functionEntries});
 const reachable=new Set(),pending=[...entryStatements];
 while(pending.length&&reachable.size<20000){const statement=pending.pop();if(reachable.has(statement))continue;reachable.add(statement);pending.push(...successors.get(statement)||[]);}
 if(pending.length)gaps.push({code:'TAC_REACHABILITY_BUDGET'});
 const degree=new Map([...reachable].map(s=>[s,[...predecessors.get(s)||[]].filter(p=>reachable.has(p)).length]));
 const queue=[...reachable].filter(s=>degree.get(s)===0),ordered=[];
 for(let i=0;i<queue.length;i++){const statement=queue[i];ordered.push(statement);for(const next of successors.get(statement)||[])if(reachable.has(next)){const count=degree.get(next)-1;degree.set(next,count);if(!count)queue.push(next);}}
 const orderedSet=new Set(ordered),frontier=[...reachable].filter(s=>!orderedSet.has(s));
 if(frontier.length)gaps.push({code:'TAC_MEMORY_CFG_CYCLE_FRONTIER',statements:frontier});
 const edges=[];
 for(const[from,targets]of successors)for(const to of targets){const row=byStatement.get(from),basis=sources.get(from+'\0'+to),fallthrough=(row.statement_successors||[]).filter(s=>byStatement.has(s));
  let condition=null;
  if(row.op==='JUMPI'){
   if(fallthrough.length===1&&targets.size===2)condition={variable:row.reads?.[1],expected:to!==fallthrough[0],statement:from,basis:'JUMPI_with_explicit_fallthrough_and_local_CFG_target'};
   else condition={variable:row.reads?.[1],expected:null,statement:from,unresolved:'TAC_BRANCH_EDGE_CONDITION_UNRESOLVED'};
  }else if(targets.size>1)condition={variable:row.reads?.[0],expected:null,statement:from,unresolved:'TAC_DYNAMIC_EDGE_CONDITION_UNRESOLVED'};
  edges.push({from,to,basis,condition});
 }
 return{byStatement,successors,predecessors,entryStatements,reachable,ordered,frontier,edges,gaps};
}
