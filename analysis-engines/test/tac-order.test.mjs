import test from'node:test';import assert from'node:assert/strict';import{buildTacOrder}from'../ir/tac-order.mjs';
const row=(statement,block,op,next=[],reads=[])=>({statement,block,op,statement_successors:next,reads});

test('TAC order follows real next and local CFG edges rather than input order or cloned labels',()=>{
 const rows=[row('cloned-late','tail','RETURN'),row('a','entry','MSTORE',['b']),row('c','body','CALL',['d']),row('b','entry','JUMP'),row('d','body','JUMP')];
 const blocks=[{block:'entry',successors:['body']},{block:'body',successors:['tail']},{block:'tail',successors:[]}];
 for(const input of[rows,[...rows].reverse(),[rows[2],rows[4],rows[0],rows[3],rows[1]]]){
  const graph=buildTacOrder(input,blocks,['entry']);assert.deepEqual(graph.ordered,['a','b','c','d','cloned-late']);assert.deepEqual(graph.gaps,[]);
 }
});

test('conditional edges retain the actual false fallthrough and true destination',()=>{
 const rows=[row('guard','entry','JUMPI',['false'],['dest','condition']),row('true','yes','MSTORE',['merge']),row('false','no','MSTORE',['merge']),row('merge','join','CALL')];
 const graph=buildTacOrder(rows,[{block:'entry',successors:['yes','no']},{block:'yes',successors:['join']},{block:'no',successors:['join']},{block:'join',successors:[]}],['entry']);
 assert.equal(graph.edges.find(e=>e.from==='guard'&&e.to==='false').condition.expected,false);
 assert.equal(graph.edges.find(e=>e.from==='guard'&&e.to==='true').condition.expected,true);
 assert.ok(graph.ordered.indexOf('merge')>graph.ordered.indexOf('true'));assert.ok(graph.ordered.indexOf('merge')>graph.ordered.indexOf('false'));
});

test('cycles and missing entries remain explicit frontiers and unreachable records are not executed',()=>{
 const graph=buildTacOrder([row('start','entry','JUMP'),row('loop','body','MSTORE',['loop','after']),row('after','tail','CALL'),row('dead','unreachable','CALL')],
 [{block:'entry',successors:['body']},{block:'body',successors:['body','tail']},{block:'tail',successors:[]}],['entry']);
 assert.deepEqual(graph.ordered,['start']);assert.deepEqual(new Set(graph.frontier),new Set(['loop','after']));assert.ok(!graph.reachable.has('dead'));
 assert.ok(graph.gaps.some(g=>g.code==='TAC_MEMORY_CFG_CYCLE_FRONTIER'));
 assert.ok(buildTacOrder([row('a','other','CALL')],[],['absent']).gaps.some(g=>g.code==='TAC_FUNCTION_ENTRY_UNAVAILABLE'));
});

test('empty blocks are traversed and ambiguous block chains preserve their concrete gap',()=>{
 const graph=buildTacOrder([row('start','entry','JUMP'),row('call','target','CALL')],[{block:'entry',successors:['empty']},{block:'empty',successors:['target']},{block:'target',successors:[]}],['entry']);
 assert.deepEqual(graph.ordered,['start','call']);assert.equal(graph.gaps.length,0);
 assert.deepEqual(buildTacOrder([row('call','target','CALL')],[{block:'empty',successors:['target']},{block:'target',successors:[]}],['empty']).ordered,['call']);
 const ambiguous=buildTacOrder([row('x','entry','MSTORE'),row('y','entry','CALL')],[{block:'entry',successors:[]}],['entry']);
 assert.ok(ambiguous.gaps.some(g=>g.code==='TAC_BLOCK_ORDER_AMBIGUOUS'));
});

test('lexical next after RETURN and lexical jumps past inlined callees never become executable edges',()=>{
 const graph=buildTacOrder([row('invoke','entry','JUMP',['after']),row('body','callee','MSTORE',['back']),row('back','callee','JUMP',['after']),row('after','return-block','RETURN',['dead']),row('dead','unreachable','CALL')],
  [{block:'entry',successors:['callee']},{block:'callee',successors:['return-block']},{block:'return-block',successors:['unreachable']},{block:'unreachable',successors:[]}],['entry']);
 assert.deepEqual(graph.ordered,['invoke','body','back','after']);assert.equal(graph.reachable.has('dead'),false);assert.ok(!graph.edges.some(e=>e.from==='invoke'&&e.to==='after'));
});
