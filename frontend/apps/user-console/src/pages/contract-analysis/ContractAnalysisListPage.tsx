import {withContractQueries} from "./provider";
import { useQuery } from "@tanstack/react-query";
import { Link,useSearchParams } from "react-router-dom";
import { contractAnalysis, useAuth } from "@chain/api-client";
import { Button, Card } from "@chain/ui";
import { STATUS, text, useAnalysisError,Pager } from "./shared";
import "./contract-analysis.css";
function ContractAnalysisListPage(){const {client,can,session}=useAuth();
 const [params,setParams]=useSearchParams(),cursor=params.get("cursor")||"";
 const query=useQuery({queryKey:["contract-analysis-list",session?.workspace_id,cursor],queryFn:async({signal})=>(await contractAnalysis.list(client,signal,cursor||undefined)).data});useAnalysisError(query.error,"contract-list"+cursor);
 return <div className="ca-page"><header className="ca-header"><h1>合约分析</h1><div>{can("investigation:run")?<Link className="btn btn--primary" to="/contract-analyses/new">新建分析</Link>:null}<Button variant="ghost" onClick={()=>void query.refetch()}>刷新</Button></div></header>
  <Card><div className="ca-table-wrap"><table className="ca-table"><thead><tr><th>合约</th><th>链</th><th>状态</th><th>阶段</th><th>创建时间</th></tr></thead><tbody>{query.data?.items.map(row=><tr key={text(row.analysis_id)}><td><Link className="ca-mono" to={`/contract-analyses/${text(row.analysis_id)}`}>{text(row.address)}</Link></td><td>{text(row.chain)}</td><td>{STATUS[String(row.status)]||text(row.status)}</td><td>{text(row.phase)}</td><td>{text(row.created_at)}</td></tr>)}</tbody></table></div>{query.isPending?<div role="status" className="ca-loading">读取中</div>:null}</Card>
 <Pager count={query.data?.record_count||0} more={query.data?.has_more||false} cursor={cursor} onFirst={()=>setParams({})} onNext={()=>setParams({cursor:query.data?.next_cursor||""})}/></div>;
}

export default withContractQueries(ContractAnalysisListPage);
