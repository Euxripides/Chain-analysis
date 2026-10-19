import { mkdtempSync, writeFileSync, readFileSync, existsSync, statSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { hash, id, makeMethod, evidence, unique } from '../model.mjs';

const COMMIT = '1b06cef82f0a29790e87f576eba6d6d793170d70';
const RELATIONS = ['PublicFunction','Function','InFunction','TAC_Op','TAC_Block','TAC_Use','TAC_Def','TAC_Variable_Value',
  'LocalBlockEdge','TAC_Statement_Next','TAC_Statement_OriginalStatement','IRFunctionCall','TAC_OriginalStatement_Block',
  'HighLevelFunctionName','Analytics_JumpToMany'];

export function analyzeGigahorse(result, bytecode) {
  const started = performance.now();
  const engine = { engine: 'gigahorse', source_commit: COMMIT, adapter_version: 'tac-1', status: 'failed' };
  const root = process.env.GIGAHORSE_ROOT || '/opt/gigahorse/gigahorse-toolchain';
  try {
    if (!existsSync(join(root, 'cache/main.dl_compiled'))) throw new Error('GIGAHORSE_COMPILED_RUNTIME_UNAVAILABLE');
    const dir = mkdtempSync(join(tmpdir(), 'contract-gigahorse-'));
    const input = join(dir,'contract.hex'); writeFileSync(input, bytecode.replace(/^0x/,''));
    const python = process.env.CONTRACT_PYTHON || '/opt/contract-python/bin/python';
    const run = spawnSync(python, [join(root,'gigahorse.py'), input, '--working_dir', join(dir,'work'),
      '--cache_dir', join(root,'cache'), '--results_file', join(dir,'results.json'), '--reuse_datalog_bin',
      '--skip_sig_resolution', '--jobs','1','--timeout_secs','90','--minimum_client_time','5','--quiet'], {
      cwd: root, encoding:'utf8', timeout: 100000, maxBuffer: 1 << 20,
      env: {...process.env, PYTHONDONTWRITEBYTECODE:'1', OMP_NUM_THREADS:'2',
        LD_LIBRARY_PATH: join(root,'souffle-addon') + (process.env.LD_LIBRARY_PATH ? ':'+process.env.LD_LIBRARY_PATH : '')},
    });
    if (run.status !== 0) throw new Error((run.error?.code || 'GIGAHORSE_FAILED') + ':' + (run.stderr || '').slice(-600));
    const out = join(dir,'work','contract','out'), relations = {};
    let total = 0;
    for (const name of RELATIONS) {
      const p = join(out,name+'.csv');
      if (!existsSync(p)) { relations[name] = []; continue; }
      const size = statSync(p).size; total += size;
      if (size > 8 << 20 || total > 24 << 20) throw new Error('GIGAHORSE_RELATION_BUDGET_EXCEEDED');
      relations[name] = readFileSync(p,'utf8').trimEnd().split('\n').filter(Boolean).map(line => line.split('\t'));
    }
    if (!relations.TAC_Op.length) throw new Error('GIGAHORSE_NO_TAC_OUTPUT');
    const raw = JSON.stringify({source_commit:COMMIT,relations}), artifact_id = id('gigahorse-tac',result.snapshot.code_hash);
    result.sections.artifacts.push({artifact_id,kind:'gigahorse_tac',filename:'gigahorse-tac.json',media_type:'application/json',text:raw,sha256:hash(raw)});
    const metadata = existsSync(join(dir,'results.json')) ? readFileSync(join(dir,'results.json'),'utf8') : '[]';
    result.sections.artifacts.push({artifact_id:id('gigahorse-run',result.snapshot.code_hash),kind:'gigahorse_metrics',filename:'gigahorse-run.json',media_type:'application/json',text:metadata,sha256:hash(metadata)});
    const blocks = new Map(relations.TAC_Block.map(([stmt,block])=>[stmt,block]));
    const members = new Map();
    for (const [block,fn] of relations.InFunction) { const list=members.get(block)||[];list.push(fn);members.set(block,list); }
    const publicFunctions = new Map(relations.PublicFunction.map(([fn,selector])=>[fn,'0x'+selector.replace(/^0x/,'').padStart(8,'0')]));
    const uses = new Map(), defs = new Map(), values=new Map();
    for(const [variable,value] of relations.TAC_Variable_Value){const list=values.get(variable)||new Set();list.add(value);values.set(variable,list)}
    const constants=new Map([...values].filter(([,list])=>list.size===1).map(([variable,list])=>[variable,[...list][0]]));
    for (const [stmt,value,position] of relations.TAC_Use) { const list=uses.get(stmt)||[];list.push({value,position:Number(position)});uses.set(stmt,list); }
    for (const [stmt,value,position] of relations.TAC_Def) { const list=defs.get(stmt)||[];list.push({value,position:Number(position)});defs.set(stmt,list); }
    const originals = new Map(relations.TAC_Statement_OriginalStatement.map(([stmt,pc])=>[stmt,pc]));
    const edges = new Map();for(const [from,to] of relations.LocalBlockEdge){const list=edges.get(from)||[];list.push(to);edges.set(from,list)}
    const methods = new Map();
    for (const [fn] of relations.Function) {
      let selector = publicFunctions.get(fn) || '';
      // Gigahorse inserts a synthetic receive block beyond the original code.
      // Its reserved hex label is not a four-byte calldata selector.
      const receive=selector==='0xeeeeeeee'&&/^0x[0-9a-f]+$/i.test(fn)&&BigInt(fn)>=BigInt((bytecode.length-2)/2);
      const fallback = selector === '0x00000000' && !result.sections.methods.some(m=>m.selector===selector);
      if (fallback||receive) selector = '';
      let method = selector ? result.sections.methods.find(m=>m.selector===selector&&m.dispatch_role!=='overridden_source_definition') :
        receive?result.sections.methods.find(m=>!m.selector&&m.entry_kind==='receive'):
        fallback ? result.sections.methods.find(m=>!m.selector&&['fallback','dispatch'].includes(m.entry_kind)) : null;
      if (!method) {
        method = makeMethod(result.snapshot,selector?`selector:${selector}`:fallback?'dispatch':receive?'receive':`gigahorse:${fn}`,selector);
        method.entry_kind=selector?'function':fallback?'fallback':receive?'receive':'bytecode_internal_function';method.visibility=selector||fallback||receive?'external':'internal';
        method.dispatch_role=selector?'external_dispatch':fallback||receive?'unselected_entry':'bytecode_internal_function';
        method.completeness={complete:false,basis:'gigahorse',unresolved:[],execution_paths:'not_exhaustively_verified'};
        const existing=result.sections.methods.find(m=>m.method_id===method.method_id);
        if(existing)method=existing;else result.sections.methods.push(method);
      }
      method.gigahorse_function_entries=unique([...(method.gigahorse_function_entries||[]),fn]);
      methods.set(fn,method);
    }
    const statementNext=new Map(),statementPrevious=new Map();
    for(const[from,to]of relations.TAC_Statement_Next){statementNext.set(from,unique([...(statementNext.get(from)||[]),to]));statementPrevious.set(to,unique([...(statementPrevious.get(to)||[]),from]));}
    result.sections.ir ||= [];result.sections.control_flow ||= [];
    for (const [stmt,op] of relations.TAC_Op) for (const fn of members.get(blocks.get(stmt)) || []) {
      const method = methods.get(fn);if (!method) continue;
      const use=(uses.get(stmt)||[]).sort((a,b)=>a.position-b.position);
      const define=(defs.get(stmt)||[]).sort((a,b)=>a.position-b.position);
      const eid=evidence(result,method,'gigahorse_tac',{artifact_id,statement:stmt,block:blocks.get(stmt),original_statement:originals.get(stmt)||null},op);
      const ir_id=id(method.method_id,'gigahorse',stmt);
      result.sections.ir.push({ir_id,method_id:method.method_id,engine:'gigahorse',op,statement:stmt,block:blocks.get(stmt),gigahorse_function_id:fn,
        original_statement:originals.get(stmt)||null,statement_successors:statementNext.get(stmt)||[],statement_predecessors:statementPrevious.get(stmt)||[],
        reads:use.map(x=>x.value),definitions:define.map(x=>x.value),
        constants:use.filter(x=>constants.has(x.value)).map(x=>({variable:x.value,value:constants.get(x.value)})),
        constant_candidates:use.filter(x=>values.has(x.value)).map(x=>({variable:x.value,values:[...values.get(x.value)]})),
        constant_definitions:define.filter(x=>values.has(x.value)).map(x=>({variable:x.value,values:[...values.get(x.value)]})),
        text:`${define.map(x=>x.value).join(',')} ${define.length?'=':''} ${op} ${use.map(x=>constants.get(x.value)||x.value).join(', ')}`,
        evidence_ids:[eid],basis:'decompiled_tac',type:'evm_word',width:256,all_paths_verified:false});
      method.ir_ids=unique([...(method.ir_ids||[]),ir_id]);
      method.additional_analysis_bases=unique([...(method.additional_analysis_bases||[]),'gigahorse_tac']);
    }
    for(const [block,functions] of members)for(const fn of functions){const method=methods.get(fn);if(!method)continue;
      result.sections.control_flow.push({cfg_id:id(method.method_id,'gigahorse-cfg',block),method_id:method.method_id,engine:'gigahorse',block,
        successors:edges.get(block)||[],function_entry:fn,gigahorse_function_id:fn,basis:'decompiled_cfg',artifact_id,complete:false});
    }
    engine.status='completed';engine.artifact_id=artifact_id;engine.statements=relations.TAC_Op.length;
    engine.functions=relations.Function.length;engine.public_functions=relations.PublicFunction.length;
    engine.imprecise_jumps=relations.Analytics_JumpToMany.length;
    if (engine.imprecise_jumps) engine.status='partial';
    result.sections.coverage.push({dimension:'gigahorse_tac',complete:false,basis:'decompiled_tac',
      statements:engine.statements,public_functions:engine.public_functions,imprecise_jumps:engine.imprecise_jumps,reason:'execution_paths_not_exhaustively_verified'});
  } catch (error) {
    engine.error_code='GIGAHORSE_ANALYSIS_FAILED';engine.error_message=String(error.message).slice(0,800);
    result.sections.coverage.push({dimension:'gigahorse_tac',complete:false,reason:engine.error_message});
  }
  engine.duration_ms=Math.round(performance.now()-started);result.sections.engines.push(engine);
}
