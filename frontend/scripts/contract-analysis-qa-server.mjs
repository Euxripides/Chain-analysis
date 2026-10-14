// Isolated QA only: serve candidate assets and proxy to the isolated API.
import {createServer,request} from 'node:http';
import {readFile,stat} from 'node:fs/promises';
import {resolve,extname} from 'node:path';
import {fileURLToPath} from 'node:url';
const root=resolve(fileURLToPath(new URL('../apps/user-console/dist/',import.meta.url)));
const mime={'.html':'text/html; charset=utf-8','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml','.png':'image/png','.woff2':'font/woff2','.json':'application/json'};
const server=createServer(async(req,res)=>{
  if(req.url.startsWith('/api/')||req.url.startsWith('/health/')){
    const upstream=request({host:'127.0.0.1',port:Number(process.env.CONTRACT_QA_API_PORT||18081),path:req.url,method:req.method,headers:{...req.headers,host:'127.0.0.1:18081'}},response=>{res.writeHead(response.statusCode,response.headers);response.pipe(res)});
    upstream.on('error',()=>{if(!res.headersSent)res.writeHead(502);res.end()});req.pipe(upstream);return;
  }
  try{
    const url=new URL(req.url,'http://127.0.0.1'),path=decodeURIComponent(url.pathname).replace(/^\/app-v2\/?/,'');
    let file=resolve(root,path||'index.html');
    if(!file.startsWith(root+'/')){res.writeHead(404);res.end();return}
    try { if(!(await stat(file)).isFile())file=resolve(root,'index.html') } catch { if(path.startsWith('assets/')){res.writeHead(404);res.end();return}file=resolve(root,'index.html') }
    const body=await readFile(file);res.writeHead(200,{'content-type':mime[extname(file)]||'application/octet-stream','cache-control':'no-store'});res.end(body);
  }catch{res.writeHead(500);res.end()}
});
server.listen(Number(process.env.CONTRACT_QA_CONSOLE_PORT||18083),'127.0.0.1',()=>console.log('isolated contract QA console ready'));
process.on('SIGTERM',()=>server.close());
