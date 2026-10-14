import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { emptyResult, id, hash, stringify } from './model.mjs';
const quantity = v => '0x' + BigInt(v || 0).toString(16);
const normalizeWord = v => BigInt(v || '0x0').toString(16).padStart(64,'0');
const sleep = ms => new Promise(resolve => setTimeout(resolve,ms));
export async function replay(input) {
  const result=emptyResult(input.snapshot),w=input.witness;
  if (!w?.raw_transaction||!w.raw_receipt||!w.raw_block||!w.parent_block||!w.prestate) throw new Error('REPLAY_WITNESS_REQUIRED');
  const tx=w.raw_transaction,original=w.raw_receipt,header=w.raw_block;
  const txHash=tx.hash,started=performance.now(),gaps=[],reads=[],optionalMethods=[];
  const accounts=Object.fromEntries(Object.entries(w.prestate).map(([a,state])=>[a.toLowerCase(),state]));
  const parent=w.parent_block,blockNumber=BigInt(header.number),fork=w.hardfork||'chain_rules_unavailable';
  const row={replay_id:id(txHash,'foundry-witness'),tx_hash:txHash,engine:'foundry/anvil',version:'1.8.5',
    binary_digest:'sha256:00f97668e1ae0fcca118fd7d2d1b6d25c0496447a523ca6bc2dfd10958c99604',
    mode:'transaction_prestate_witness',state_granularity:'transaction_prestate',transaction_index:tx.transactionIndex,
    hardfork:fork,status:'not_verified',kind:'replayed_execution',differences:[],witness_gaps:gaps,
    evidence_ids:[id(txHash,'replay-witness')],simulation:w.simulation===true,unsupported_optional_methods:optionalMethods};
  if (fork==='chain_rules_unavailable') {
    row.status='unsupported_chain_rules';row.complete=false;
    result.sections.replays=[row];result.sections.coverage.push({dimension:'replay',tx_hash:txHash,complete:false,status:row.status});
    return result;
  }
  const rpcServer=createServer(async(req,res)=>{
    let size=0,parts=[];for await(const part of req){size+=part.length;if(size>1<<20){res.writeHead(413);res.end();return}parts.push(part)}
    let body;try{body=JSON.parse(Buffer.concat(parts).toString('utf8'))}catch{res.writeHead(400);res.end();return}
    const handle=call=>{
      const p=call.params||[],method=call.method;let value;
      reads.push({method,params:p});
      // Anvil falls back to ordinary account RPCs when this optional capability
      // is absent. Reject the capability without claiming a missing state read.
      if(['anvil_nodeInfo','anvil_metadata','eth_getAccountInfo'].includes(method)){
        if(!optionalMethods.includes(method))optionalMethods.push(method);
        return {jsonrpc:'2.0',id:call.id,error:{code:-32601,message:'Method not found'}};
      }
      try{
        if(method==='eth_chainId')value=quantity(input.snapshot.chain_id);
        else if(method==='net_version')value=String(input.snapshot.chain_id);
        else if(method==='eth_blockNumber')value=parent.number;
        else if(method==='eth_getBlockByNumber'||method==='eth_getBlockByHash'){
          if(p[0]==='latest'||p[0]===parent.number||p[0]===parent.hash)value={...parent,transactions:[]};
          else if(p[0]===header.number||p[0]===header.hash)value={...header,transactions:[tx]};
          else throw new Error('unknown_block:'+p[0]);
        }else if(['eth_getBalance','eth_getTransactionCount','eth_getCode','eth_getStorageAt'].includes(method)){
          const address=String(p[0]).toLowerCase(),account=accounts[address];
          if(!account)throw new Error('unknown_account:'+address);
          if(method==='eth_getBalance')value=account.balance||'0x0';
          if(method==='eth_getTransactionCount')value=quantity(account.nonce||0);
          if(method==='eth_getCode')value=account.code||'0x';
          if(method==='eth_getStorageAt'){
            const slot=normalizeWord(p[1]);const entry=Object.entries(account.storage||{}).find(([key])=>normalizeWord(key)===slot);
            if(!entry)throw new Error('unknown_storage:'+address+':'+slot);
            value='0x'+normalizeWord(entry[1]);
          }
        }else if(method==='eth_getTransactionByHash'&&p[0]===txHash)value=tx;
        else if(method==='eth_getTransactionReceipt'&&p[0]===txHash)value=original;
        else if(method==='eth_gasPrice')value=tx.gasPrice||header.baseFeePerGas||'0x0';
        else if(method==='eth_getLogs')value=[];
        else throw new Error('unsupported_witness_method:'+method);
        return {jsonrpc:'2.0',id:call.id,result:value};
      }catch(error){gaps.push(String(error.message));return {jsonrpc:'2.0',id:call.id,error:{code:-32001,message:'REPLAY_WITNESS_INCOMPLETE'}}}
    };
    const output=Array.isArray(body)?body.map(handle):handle(body);res.setHeader('content-type','application/json');res.end(stringify(output));
  });
  rpcServer.listen(19000,'127.0.0.1');await once(rpcServer,'listening');
  const args=['--host','127.0.0.1','--port','19001','--accounts','0','--threads','2','--fork-url','http://127.0.0.1:19000','--fork-block-number',String(blockNumber-1n),
    '--chain-id',String(input.snapshot.chain_id),'--no-mining','--auto-impersonate','--no-fork-node-info','--no-bal','--disable-default-create2-deployer','--disable-console-log','--timeout','2000','--retries','0','--memory-limit','134217728','--silent'];
  if(fork!=='engine_default')args.push('--hardfork',fork);
  const binary=process.env.ANVIL_BINARY||'/usr/local/bin/anvil';const child=spawn(binary,args,{stdio:['ignore','ignore','pipe'],env:{PATH:'/usr/bin:/bin',HOME:'/tmp'}});
  let diagnostics='';child.stderr.on('data',b=>{if(diagnostics.length<8192)diagnostics+=b.toString()});
  let sequence=0;
  const rpc=async(method,params=[])=>{
    const response=await fetch('http://127.0.0.1:19001',{method:'POST',headers:{'content-type':'application/json'},body:stringify({jsonrpc:'2.0',id:++sequence,method,params}),signal:AbortSignal.timeout(15000)});
    const body=await response.json();if(body.error)throw new Error(`${method}:${body.error.message}`);return body.result;
  };
  try{
    let ready=false;for(let i=0;i<80;i++){if(child.exitCode!==null)throw new Error('ANVIL_STARTUP_FAILED:'+diagnostics);try{await rpc('eth_chainId');ready=true;break}catch{await sleep(100)}}
    if(!ready)throw new Error('ANVIL_STARTUP_TIMEOUT');
    if(header.baseFeePerGas)await rpc('anvil_setNextBlockBaseFeePerGas',[header.baseFeePerGas]);
    if(header.gasLimit)await rpc('anvil_setBlockGasLimit',[header.gasLimit]);
    if(header.miner)await rpc('anvil_setCoinbase',[header.miner]);
    await rpc('evm_setNextBlockTimestamp',[Number(BigInt(header.timestamp))]);
    // Preserve the original block random value when the pinned engine exposes it.
    if(['paris','shanghai','cancun','prague','osaka'].includes(fork)&&header.mixHash){try{await rpc('anvil_setNextBlockPrevRandao',[header.mixHash])}catch{row.environment_gap='original_prev_randao_setter_unavailable'}}
    else row.environment_gap='pow_difficulty_environment_adapter_unavailable';
    const transaction={};for(const key of ['from','to','value','gas','nonce','gasPrice','maxFeePerGas','maxPriorityFeePerGas','accessList','type','authorizationList'])if(tx[key]!=null)transaction[key]=tx[key];
    transaction.data=tx.input||'0x';
    if(transaction.maxFeePerGas!=null)delete transaction.gasPrice;
    if(transaction.type==='0x3')throw new Error('BLOB_TRANSACTION_REPLAY_ADAPTER_UNAVAILABLE');
    const replayHash=await rpc('eth_sendTransaction',[transaction]);await rpc('evm_mine');const receipt=await rpc('eth_getTransactionReceipt',[replayHash]);
    if(!receipt)throw new Error('REPLAY_RECEIPT_UNAVAILABLE');
    row.replay_tx_hash=replayHash;
    const logShape=logs=>(logs||[]).map(l=>({address:l.address.toLowerCase(),topics:l.topics.map(t=>t.toLowerCase()),data:l.data.toLowerCase()}));
    row.observed={status:original.status,gas_used:BigInt(original.gasUsed).toString(),logs:logShape(original.logs)};
    row.replayed={status:receipt.status,gas_used:BigInt(receipt.gasUsed).toString(),logs:logShape(receipt.logs)};
    if(BigInt(receipt.status)!==BigInt(original.status))row.differences.push('status');
    if(BigInt(receipt.gasUsed)!==BigInt(original.gasUsed))row.differences.push('gas_used');
    if(JSON.stringify(row.observed.logs)!==JSON.stringify(row.replayed.logs))row.differences.push('logs');
    const changed=w.state_diff?.post||{},pre=w.state_diff?.pre||{};let checked=0;
    for(const address of new Set([...Object.keys(changed),...Object.keys(pre)])){
      const slots=new Set([...Object.keys(changed[address]?.storage||{}),...Object.keys(pre[address]?.storage||{})]);
      for(const slot of slots){const expected=changed[address]?.storage?.[slot]||'0x0';const actual=await rpc('eth_getStorageAt',[address,slot,'latest']);checked++;if(BigInt(actual)!==BigInt(expected))row.differences.push('storage:'+address+':'+slot)}
      for(const [field,method]of [['balance','eth_getBalance'],['nonce','eth_getTransactionCount'],['code','eth_getCode']]) {
        const before=pre[address]?.[field],after=changed[address]?.[field];if(before==null&&after==null)continue;
        const expected=after??(changed[address]?before:field==='code'?'0x':0);
        const actual=await rpc(method,[address,'latest']);
        if(field==='code'?actual.toLowerCase()!==String(expected).toLowerCase():BigInt(actual)!==BigInt(expected))row.differences.push(field+':'+address);
      }
    }
    row.state_slots_checked=checked;row.state_diff_complete=Boolean(w.state_diff);
    row.status=gaps.length?'witness_gap':row.differences.length?'replay_mismatch':row.environment_gap?'environment_gap':'matched_observations';
    row.complete=row.status==='matched_observations'&&Boolean(w.state_diff)&&!row.environment_gap;
    row.environment={block_number:header.number,block_timestamp:header.timestamp,base_fee:header.baseFeePerGas,coinbase:header.miner,prev_randao:header.mixHash,prestate_includes_prior_same_block_execution:true};
    const trace=await rpc('debug_traceTransaction',[replayHash,{tracer:'callTracer'}]);row.call_trace=trace;
  }catch(error){row.status=gaps.length?'witness_gap':'replay_failed';row.error=String(error.message).slice(0,1000);row.complete=false}
  finally{child.kill('SIGKILL');rpcServer.closeAllConnections();await new Promise(resolve=>rpcServer.close(resolve));}
  row.duration_ms=Math.round(performance.now()-started);row.witness_rpc_reads=reads.length;
  result.sections.replays=[row];result.sections.coverage.push({dimension:'replay',tx_hash:txHash,complete:row.complete===true,status:row.status});
  const artifact=stringify({replay:row,witness_reads:reads});result.sections.artifacts.push({artifact_id:id(txHash,'replay'),kind:'replay_evidence',filename:txHash+'-replay.json',media_type:'application/json',text:artifact,sha256:hash(artifact)});
  result.metrics={duration_ms:row.duration_ms,peak_rss_bytes:process.resourceUsage().maxRSS*1024};return result;
}
