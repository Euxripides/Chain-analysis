const fs=require('fs'), path=require('path');
const DIR=process.argv[2];
const imgs=['postgres-18','clickhouse-clickhouse-server-26.7','nats-2-alpine','minio-minio-latest'];
const SEVS=['CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN'];
const out={images:{},misconfig:{}};
for(const n of imgs){
  const f=path.join(DIR,'images',n+'.json');
  if(!fs.existsSync(f)){out.images[n]={error:'missing '+f};continue;}
  let j; try{j=JSON.parse(fs.readFileSync(f,'utf8'));}catch(e){out.images[n]={error:'parse '+e.message};continue;}
  const counts={CRITICAL:0,HIGH:0,MEDIUM:0,LOW:0,UNKNOWN:0};
  const rows=[]; const perTarget={};
  for(const r of (j.Results||[])){
    const vs=r.Vulnerabilities||[];
    if(vs.length) perTarget[r.Target]=vs.length;
    for(const v of vs){
      const s=SEVS.includes(v.Severity)?v.Severity:'UNKNOWN';
      counts[s]++;
      if(s==='CRITICAL'||s==='HIGH'){
        rows.push({sev:s,id:v.VulnerabilityID,pkg:v.PkgName,installed:v.InstalledVersion,
          fixed:(v.FixedVersion==null?'':v.FixedVersion),title:v.Title||'',
          target:r.Target,cls:r.Class,type:r.Type,url:v.PrimaryURL||''});
      }
    }
  }
  rows.sort((a,b)=> (a.sev===b.sev? (a.pkg+a.id).localeCompare(b.pkg+b.id) : (a.sev==='CRITICAL'?-1:1)));
  out.images[n]={artifactName:j.ArtifactName,artifactType:j.ArtifactType,
    os:j.Metadata&&j.Metadata.OS?j.Metadata.OS:null,
    schemaVersion:j.SchemaVersion,
    counts,total:SEVS.reduce((a,s)=>a+counts[s],0),targets:perTarget,highCritical:rows};
}
// misconfig: trivy config repo root
function readJSON(p){ try{return JSON.parse(fs.readFileSync(p,'utf8'));}catch(e){return null;} }
const tc=readJSON(path.join(DIR,'misconfig','trivy-config.json'));
function misconfSum(j){
  if(!j) return {error:'missing'};
  const sev={CRITICAL:0,HIGH:0,MEDIUM:0,LOW:0,UNKNOWN:0}; const rules=[]; let targets=0;
  for(const r of (j.Results||[])){
    if((r.Misconfigurations||[]).length) targets++;
    for(const m of (r.Misconfigurations||[])){
      const s=SEVS.includes(m.Severity)?m.Severity:'UNKNOWN'; sev[s]++;
      rules.push({id:m.ID,avdId:m.AVDID||m.ID,sev:s,title:m.Title,file:r.Target,line:(m.CauseMetadata&&m.CauseMetadata.StartLine)||null,status:m.Status,resolution:(m.Resolution||'').slice(0,200)});
    }
  }
  return {findings:rules.length,severities:sev,targetsWithFindings:targets,allTargets:(j.Results||[]).map(r=>r.Target),rules};
}
out.misconfig.trivyRepoRoot=misconfSum(tc);
out.misconfig.trivyDeploy=misconfSum(readJSON(path.join(DIR,'misconfig','trivy-config-deploy.json')));
out.misconfig.checkovCompose=readJSON(path.join(DIR,'misconfig','checkov-compose.json'));
fs.writeFileSync(path.join(DIR,'raw','summary.json'),JSON.stringify(out,null,1));

console.log('==== IMAGE SEVERITY MATRIX ====');
console.log(['image','CRITICAL','HIGH','MEDIUM','LOW','UNKNOWN','TOTAL'].join('\t'));
for(const n of imgs){const i=out.images[n]; if(i.error){console.log(n+'\tERROR '+i.error);continue;}
  console.log([n,i.counts.CRITICAL,i.counts.HIGH,i.counts.MEDIUM,i.counts.LOW,i.counts.UNKNOWN,i.total].join('\t'));}
console.log();
console.log('==== MISCONFIG (trivy config) ====');
console.log('repo-root findings:',out.misconfig.trivyRepoRoot.findings,'severities:',JSON.stringify(out.misconfig.trivyRepoRoot.severities));
console.log('repo-root targets:',JSON.stringify(out.misconfig.trivyRepoRoot.allTargets));
console.log('deploy findings:',out.misconfig.trivyDeploy.findings,'severities:',JSON.stringify(out.misconfig.trivyDeploy.severities));
console.log();
console.log('==== CRITICAL/HIGH DETAIL ====');
for(const n of imgs){const i=out.images[n]; if(i.error) continue;
  console.log('--- '+n+' ('+i.artifactName+') CRIT='+i.counts.CRITICAL+' HIGH='+i.counts.HIGH+' ---');
  for(const r of i.highCritical) console.log([r.sev,r.id,r.pkg,r.installed,'->'+(r.fixed||'no-fix'),r.target].join(' | '));
  console.log();}
