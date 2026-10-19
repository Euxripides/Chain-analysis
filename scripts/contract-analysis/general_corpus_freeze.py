"""Freeze real deployment material and source-reviewed expectations before engine changes.

No provider calls. Expected business facts below were reviewed against the original
source; engine output is deliberately not an input to this program.
"""
import argparse, datetime, hashlib, json, pathlib, re

ROOT = pathlib.Path(__file__).resolve().parents[2]
sha = lambda b: hashlib.sha256(b).hexdigest()

# source context, function (last body when an override exists), dimension, fact
FACTS = {
    'usdt': [
        ('usdt','transfer','conditions','Transfers require not paused and a sender absent from the blacklist; deprecated mode forwards to upgradedAddress.'),
        ('usdt','setParams','permissions','Only owner changes fee parameters; newBasisPoints < 20 and newMaxFee < 50; maximumFee = newMaxFee * 10**decimals.'),
        ('usdt','issue','state','Only owner adds amount to balances[owner] and _totalSupply, with explicit strict-increase checks.'),
        ('usdt','destroyBlackFunds','funds','Only owner destroys the complete balance of a blacklisted address and subtracts it from _totalSupply.')],
    'pepe': [
        ('pepe','_beforeTokenTransfer','conditions','Both sender and recipient must be absent from blacklists; before pair assignment at least one side must be owner.'),
        ('pepe','setRule','permissions','Only owner changes limited, pair, maximum holding and minimum holding.'),
        ('pepe','_transfer','funds','ERC20 transfer subtracts exactly amount from sender and adds exactly amount to recipient; no transfer-tax split in this implementation.'),
        ('pepe','burn','state','Caller may burn its own value through _burn(msg.sender,value).')],
    'ampl': [
        ('ampl-implementation','rebase','permissions','Only monetaryPolicy can rebase; zero delta returns without changing gons-per-fragment.'),
        ('ampl-implementation','rebase','state','Apply signed supplyDelta, cap at MAX_SUPPLY, then _gonsPerFragment = TOTAL_GONS / _totalSupply.'),
        ('ampl-implementation','transfer','funds','gonValue = value * _gonsPerFragment; debit sender gons and credit recipient gons.'),
        ('ampl-implementation','permit','conditions','Deadline and recovered owner constrain approval; nonce is read then incremented by checked addition before allowance write.')],
    'steth': [
        ('steth-implementation','_submit','conditions','Deposit must be nonzero, staking unpaused and within the current staking limit when set.'),
        ('steth-implementation','getSharesByPooledEth','funds','shares = ethAmount * totalShares / totalPooledEther, with integer truncation.'),
        ('steth-implementation','_submit','state','Mint calculated shares to sender and add msg.value to buffered ether.'),
        ('steth-implementation','_handleOracleReport','permissions','Report sender must equal locator accountingOracle; report timestamp cannot exceed block.timestamp.')],
    'sdai': [
        ('sdai','convertToShares','funds','assets * RAY / chi; chi uses _rpow(dsr, timestamp-rho) * pot.chi / RAY when timestamp > rho.'),
        ('sdai','_mint','state','Pull DAI from sender, join daiJoin and pot, then credit receiver shares and totalSupply.'),
        ('sdai','_burn','permissions','Spending another owner shares requires allowance unless it equals uint256 max; finite allowance is reduced.'),
        ('sdai','_mint','conditions','Receiver cannot be zero or the vault itself.')],
    'sushi-bar': [
        ('sushi-bar','enter','funds','Initial shares equal amount when totalShares or totalSushi is zero; otherwise floor(amount * totalShares / totalSushi).'),
        ('sushi-bar','leave','funds','Redemption = floor(share * sushi.balanceOf(this) / totalShares), before burning shares and transferring SUSHI.'),
        ('sushi-bar','enter','state','Mint shares to sender before transferFrom(sender,this,amount); source ignores the ERC20 boolean return.'),
        ('sushi-bar','leave','permissions','Burn is charged to msg.sender; redemption transfer recipient is msg.sender.')],
    'uniswap-v2-pair': [
        ('uniswap-v2-pair','swap','conditions','Require positive output, each output below reserve and recipient distinct from both token addresses; lock modifier serializes entry.'),
        ('uniswap-v2-pair','swap','funds','Adjusted balances use balance*1000 - amountIn*3 and their product must be >= reserve0*reserve1*1000**2.'),
        ('uniswap-v2-pair','_mintFee','funds','Fee LP = floor(totalSupply*(sqrt(reserve0*reserve1)-sqrt(kLast))/(5*sqrt(reserve0*reserve1)+sqrt(kLast))) when growth is positive and fee enabled.'),
        ('uniswap-v2-pair','initialize','permissions','Only factory initializes token0 and token1; swap optionally calls recipient uniswapV2Call before balance and invariant checks.')],
    'uniswap-v3-pool': [
        ('uniswap-v3-pool','mint','conditions','Positive liquidity amount; mint callback must pay amount0/amount1 verified by post-callback balance comparisons.'),
        ('uniswap-v3-pool','flash','funds','fee0/fee1 = ceil(amount0/amount1 * fee / 1000000); final balances must cover initial balances plus these fees.'),
        ('uniswap-v3-pool','flash','state','Paid amounts split protocol fraction and update feeGrowthGlobalX128 with FullMath.mulDiv over liquidity.'),
        ('uniswap-v3-pool','setFeeProtocol','permissions','Only factory owner can alter fee protocol; each side is zero or between 4 and 10 inclusive.')],
    'sushi-masterchef': [
        ('sushi-masterchef','updatePool','funds','sushiReward = multiplier*sushiPerBlock*allocPoint/totalAllocPoint; mint reward/10 to devaddr and reward to this.'),
        ('sushi-masterchef','deposit','state','Set rewardDebt = amount*accSushiPerShare/10**12 after reward payment and LP transferFrom.'),
        ('sushi-masterchef','emergencyWithdraw','funds','Transfer caller complete LP amount, then zero amount and rewardDebt without pending reward settlement.'),
        ('sushi-masterchef','setMigrator','permissions','Only owner can set migrator; migration depends on the configured external migrator.')],
    'uniswap-v3-staker': [
        ('uniswap-v3-staker','createIncentive','conditions','Reward positive; future start bounded by max lead time; end after start and duration bounded.'),
        ('uniswap-v3-staker','unstakeToken','permissions','Before incentive end only deposit owner may unstake; after end this specific owner guard is not required.'),
        ('uniswap-v3-staker','unstakeToken','state','Reduce stake counts and unclaimed reward, credit computed reward to deposit owner, then delete stake fields.'),
        ('uniswap-v3-staker','claimReward','funds','Requested zero or at least accrued reward claims all; smaller positive request caps payment; debit caller rewards then transfer to chosen to.')],
    'compound-ceth': [
        ('compound-ceth','exchangeRateStoredInternal','funds','Initial exchange rate when totalSupply zero; otherwise (cash + totalBorrows - totalReserves)*10**18/totalSupply with checked error returns.'),
        ('compound-ceth','borrowFresh','conditions','Comptroller permission, current accrual block and sufficient cash required; failures return error codes before borrowing.'),
        ('compound-ceth','borrowFresh','state','Transfer native ETH to borrower and update principal, interestIndex and totalBorrows; failed transfer reverts.'),
        ('compound-ceth','_setComptroller','permissions','Caller must equal admin; new controller must satisfy isComptroller marker.')],
    'maker-vat': [
        ('maker-vat','frob','conditions','Check live, initialized rate, debt ceilings, collateral safety, actor-specific wish permissions and dust threshold.'),
        ('maker-vat','frob','funds','dtab = rate*dart; gem[i][v] decreases by signed dink and dai[w] increases by signed dtab.'),
        ('maker-vat','frob','state','Update urn collateral/debt, ilk aggregate normalized debt and global debt atomically.'),
        ('maker-vat','suck','permissions','auth guard limits unbacked debt creation; sin[u], dai[v], vice and debt each increase by rad.')],
    'usdc-proxy': [
        ('usdc-proxy','upgradeTo','permissions','ifAdmin routes admin to _upgradeTo; non-admin calls are forwarded through fallback.'),
        ('usdc-proxy','_willFallback','conditions','Proxy admin is forbidden from reaching implementation fallback.'),
        ('usdc-proxy','_setImplementation','state','Implementation requires nonzero contract code and is written to the explicit implementation slot.'),
        ('usdc-proxy','_delegate','funds','Delegatecall forwards calldata and gas in proxy storage context; return or revert propagates exact returndata.')],
    'uniswap-v2-factory': [
        ('uniswap-v2-factory','createPair','conditions','Tokens must differ, sorted token0 nonzero, pair absent.'),
        ('uniswap-v2-factory','createPair','state','CREATE2 uses pair creationCode and salt keccak256(token0,token1); initialize child then record both directions and append allPairs.'),
        ('uniswap-v2-factory','setFeeTo','permissions','Only feeToSetter changes feeTo.'),
        ('uniswap-v2-factory','setFeeToSetter','permissions','Only current feeToSetter changes feeToSetter.')],
    'permit2': [
        ('permit2','_permitTransferFrom','conditions','Signed deadline and requestedAmount <= permitted.amount checked; signature verified for owner before transfer.'),
        ('permit2','_useUnorderedNonce','state','Nonce bitmap indexes upper 248 bits, selects low 8-bit position and rejects reuse after XOR toggle.'),
        ('permit2','_updateApproval','permissions','Signed allowance details nonce must equal stored owner/token/spender nonce before updateAll.'),
        ('permit2','_permitTransferFrom','funds','Single permit transfers permitted token from signed owner to transferDetails.to for requestedAmount.')],
    'safe-singleton': [
        ('safe-singleton','execTransaction','state','Nonce is included in encoded transaction and incremented before signature checks; guard checks before and after execute.'),
        ('safe-singleton','checkNSignatures','permissions','Threshold count, sorted distinct nonzero owners, and EOA/approved-hash/contract signature branches constrain execution.'),
        ('safe-singleton','execTransaction','conditions','Execution failure reverts if safeTxGas and gasPrice are both zero; otherwise it can emit ExecutionFailure and return false.'),
        ('safe-singleton','handlePayment','funds','Gas reimbursement includes gasUsed plus baseGas; native payment caps gas price at tx.gasprice, token payment uses gasPrice.')],
}


def function_span(doc, name):
    sources = doc['stdJsonInput']['sources']
    main = doc['compilation']['fullyQualifiedName'].rsplit(':', 1)[0]
    candidates = []
    for file, entry in sources.items():
        text = entry['content']
        for match in re.finditer(r'function\s+' + re.escape(name) + r'\s*\(', text):
            at = text.find('{', match.end()); semi = text.find(';', match.end())
            if at < 0 or 0 <= semi < at:
                continue
            depth, end = 1, at + 1
            # Solidity comments/string literals can contain braces. Scan tokens.
            tokens = re.finditer(r'//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|[{}]', text[end:], re.S)
            for token in tokens:
                if token.group() == '{': depth += 1
                if token.group() == '}': depth -= 1
                if depth == 0:
                    end += token.end(); break
            assert depth == 0, (file, name)
            candidates.append((file != main, file, match.start(), end, text))
    assert candidates, name
    # Reviewed overrides are the last body in flattened main source. Single
    # permit uses the first overload in SignatureTransfer.sol, not the batch.
    candidates.sort(key=lambda c: (c[0], '/pkgs/' in c[1], c[1], -c[2]))
    if name == '_permitTransferFrom':
        candidates.sort(key=lambda c: ('/pkgs/' in c[1], c[1], c[2]))
    _, file, start, end, text = candidates[0]
    raw = text.encode('utf-8'); start_b = len(text[:start].encode('utf-8')); body = text[start:end].encode('utf-8')
    assert raw[start_b:start_b+len(body)] == body
    return {'file': file, 'start': start_b, 'length': len(body), 'line': text[:start].count('\n')+1,
            'source_sha256': sha(raw), 'span_sha256': sha(body), 'text': body.decode('utf-8')}


def freeze(materials, out):
    materials = pathlib.Path(materials); out = pathlib.Path(out); out.mkdir(parents=True, exist_ok=True)
    assert not (out/'corpus-manifest.json').exists(), 'Frozen manifest already exists; do not overwrite'
    docs = {key: json.loads((materials/key/'source-provider.json').read_text(encoding='utf-8'))
            for key in set(FACTS)|{'ampl-implementation','steth-implementation'}}
    candidates = {x['key']: x for x in json.loads((materials/'new-root-candidates.json').read_text(encoding='utf-8'))}
    candidates.update({key: {'group': group, 'set': split} for key,group,split in [
        ('usdt','ledger','development'),('pepe','ledger','blind'),('uniswap-v2-pair','amm','development')]})
    samples = []; labels = {'development': [], 'blind': []}
    for key, facts in FACTS.items():
        record = {'key':key, **{k:candidates[key][k] for k in ['group','set']}, 'contexts':[]}
        contexts = [key] + ([key+'-implementation'] if key in ['ampl','steth'] else [])
        for context in contexts:
            d=materials/context; doc=docs[context]
            snapshot_file='snapshot.json' if (d/'snapshot.json').exists() else 'material.json'
            snapshot_doc=json.loads((d/snapshot_file).read_text(encoding='utf-8'))
            snap=snapshot_doc if snapshot_file=='snapshot.json' else snapshot_doc['snapshot']
            code=bytes.fromhex((d/'runtime.hex').read_text().strip()[2:]); assert code
            assert snap.get('code_sha256',sha(code)) == sha(code)
            record['contexts'].append({'key':context,'snapshot':snap,'compiler':doc['compilation'],
                'provider_match':doc['match'],'provider_runtime_match':doc['runtimeMatch'],
                'files': {n:sha((d/n).read_bytes())for n in ['source-provider.json','runtime.hex',snapshot_file]},
                'source_files':{n:sha(s['content'].encode('utf-8'))for n,s in doc['stdJsonInput']['sources'].items()},
                'abi_sha256':sha(json.dumps(doc['abi'],sort_keys=True,separators=(',',':')).encode()),'abi':doc['abi']})
        business=[]
        for index,(context,fn,dimension,text) in enumerate(facts):
            business.append({'fact_id':key+':'+str(index+1),'context':context,'function':fn,'dimension':dimension,
                'expected':text,'basis':'source_review_before_algorithm_changes','evidence':function_span(docs[context],fn)})
        label={'key':key,'business_facts':business,'method_reference':'verified provider ABI frozen independently of analysis output',
            'assessment_rule':'Require structured conditions/effects/formula or permission with valid source/PC evidence. Name, selector, raw source artifact and record counts alone do not satisfy a business fact. Unknown and missing facts remain in denominator.'}
        labels[record['set']].append(label); samples.append(record)
    assert len(samples)==16 and len(labels['blind'])==len(labels['development'])==8
    assert len({s['contexts'][0]['snapshot']['code_hash']for s in samples})==16
    assert all(sum(s['group']==g for s in samples)==2 for g in {s['group']for s in samples})
    manifest={'version':'real-business-corpus-v1','frozen_at_utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),
        'engine_outputs_seen':False,'independent_business_implementations':16,'samples':samples,
        'supplementary':{'weth':'existing execution/selected-transaction control, excluded from independent 16',
            'yearn-dai-v2':'selected source provider returned HTTP 404 before engine analysis; raw error retained; replaced by independent SushiBar'},
        'blind_rule':'Do not use blind output to tune algorithms. On tuning, reclassify and obtain fresh blind samples before a new blind claim.'}
    files={}
    for split,rows in labels.items():
        name='labels-'+split+'.json';raw=json.dumps(rows,ensure_ascii=False,indent=2).encode('utf-8');(out/name).write_bytes(raw);files[name]=sha(raw)
    manifest['label_sha256']=files
    raw=json.dumps(manifest,ensure_ascii=False,indent=2).encode('utf-8');(out/'corpus-manifest.json').write_bytes(raw)
    (out/'seal.json').write_text(json.dumps({'manifest_sha256':sha(raw),'labels':files,'engine_outputs_seen':False,'facts':64},indent=2),encoding='utf-8')
    print('CORPUS_FROZEN',len(samples),'64_SOURCE_REVIEWED_FACTS',sha(raw))


if __name__ == '__main__':
    p=argparse.ArgumentParser();p.add_argument('--materials',required=True);p.add_argument('--out',required=True);a=p.parse_args();freeze(a.materials,a.out)
