from pathlib import Path
import subprocess,json
root=Path(__file__).resolve().parents[2];assert root.name=='contract-transaction-history-20261016';out=root/'acceptance/contract-analysis-transaction-history/geth-runtime';out.mkdir(parents=True,exist_ok=True)
tag='ethereum/client-go:v1.16.9'
locked='ethereum/client-go@sha256:2dd7ef210a1a3fb87887676f7ead1b98161af83810928cdf6fc5b84bcdcc8ef4'
with(out/'image-pull.log').open('w')as log:p=subprocess.run(['docker','pull',locked],stdout=log,stderr=subprocess.STDOUT)
if p.returncode:print((out/'image-pull.log').read_text()[-3500:],flush=True);raise SystemExit(p.returncode)
item=json.loads(subprocess.check_output(['docker','image','inspect',locked]))[0];digest=next(x for x in item['RepoDigests']if x==locked);image=item['Id']
command=['docker','run','--rm','--pull','never','--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','--user','65532:65532','--cpus','2','--memory','1g','--pids-limit','64']
version=subprocess.check_output([*command,'--entrypoint','/usr/local/bin/geth',image,'version'],text=True);assert'Version: 1.16.9'in version
binary=subprocess.check_output([*command,'--entrypoint','sha256sum',image,'/usr/local/bin/geth'],text=True).split()[0]
(out/'geth-image.json').write_text(json.dumps({'image':image,'repo_digest':digest,'version':version,'binary_sha256':binary,'tag':tag,'source_tag':'https://github.com/ethereum/go-ethereum/tree/v1.16.9','dev_chain_hardfork':'osaka'},indent=2));print('PINNED_LOCAL_GETH_QA_RUNTIME',image,digest,binary,flush=True)
