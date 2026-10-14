// During additive release, the running OAuth service retains its authorization
// codes and token revocations. An initialization RPC checks that authority.
export async function legacyOAuthAllowed(endpoint:string,authorization:string,clientIP:string):Promise<boolean>{
 const url=new URL(endpoint);
 if(url.protocol!=="http:"||!["127.0.0.1","[::1]"].includes(url.hostname))throw new Error("OAUTH_VALIDATION_CONFIGURATION");
 const response=await fetch(url,{method:"POST",headers:{authorization,"content-type":"application/json",accept:"application/json, text/event-stream","x-forwarded-for":clientIP},
  body:JSON.stringify({jsonrpc:"2.0",id:"contract-auth-check",method:"initialize",params:{protocolVersion:"2025-03-26",capabilities:{},clientInfo:{name:"contract-release-auth-check",version:"1.0.0"}}}),signal:AbortSignal.timeout(5000)});
 await response.body?.cancel();
 if(response.status===401||response.status===403)return false;
 if(!response.ok)throw new Error("OAUTH_VALIDATION_UNAVAILABLE");
 return true;
}
