"""Perturb a retained real delta to verify that review does not erase new facts."""
import ast,copy,hashlib,json,pathlib,re

ROOT=pathlib.Path(__file__).resolve().parents[2];BASE=ROOT/'acceptance/contract-analysis-storage'
image=(BASE/'engine-image.txt').read_text().strip();directory=BASE/'corpus'/('candidate-17-'+image[7:23])
script=ROOT/'scripts/contract-analysis/p2_storage_semantic_review.py'
module=ast.parse(script.read_text());names={'method','projection','condition_proof','duplicate_conditions_proof','source_occurrence','same_source_occurrence'}
namespace={'json':json,'re':re};exec(compile(ast.Module(body=[n for n in module.body if isinstance(n,ast.FunctionDef)and n.name in names],type_ignores=[]),str(script),'exec'),namespace)
old_review=json.loads((directory/'semantic-review-attempts/initial-compiler-conditions/semantic-review.json').read_text())
delta=next(r for r in old_review['unreviewed_rows']if r['key']=='ampl-implementation-source'and r['section']=='storage')
key=delta['key'];old=json.loads((pathlib.Path(delta['prior_directory'])/(key+'-output.json')).read_text())['sections'];new=json.loads((directory/(key+'-output.json')).read_text())['sections']
before=delta['before_row'];projection=namespace['projection'];duplicate=namespace['duplicate_conditions_proof'];condition=namespace['condition_proof']
fields=['access','slot','offset','type','variable','variable_type','key_expression','value_expression','operation','slot_role','conditions','code_address','storage_address'];namespace['fields']={'storage':fields}
# The projection function uses the same explicit strict field set as the gate.
after=next(r for r in new['storage']if projection(old,before,'storage',{'conditions'})==projection(new,r,'storage',{'conditions'})and namespace['same_source_occurrence'](before,r,old,new)and duplicate(before,r,old,new)and list(dict.fromkeys(before['conditions']))==r['conditions'])
def minimal(s,row):
    eid=row['key_expression_id'];return{'methods':[m for m in s['methods']if m['method_id']==row['method_id']],
        'expressions':[e for e in s['expressions']if e['expression_id']==eid],
        'evidence':[e for e in s['evidence']if e['evidence_id']in row['evidence_ids']]}
old=minimal(old,before);new=minimal(new,after);checks=[]
def normalized(row):return{**row,'conditions':list(dict.fromkeys(row['conditions']))}
assert duplicate(before,after,old,new)
assert condition(normalized(before),normalized(after),new,{'conditions':{}})==[]
checks.append('exact duplicate predicate serialization accepted')
changed=copy.deepcopy(after);changed['conditions']=[];changed_sections=copy.deepcopy(new);changed_sections['expressions'][0]['conditions']=[]
assert condition(normalized(before),normalized(changed),changed_sections,{'conditions':{}})is None
checks.append('removing a distinct guard from both row and context rejected')
changed_sections=copy.deepcopy(new);changed_sections['evidence'][0]['location']['ast_id']+=1
assert duplicate(before,after,old,changed_sections)is None
checks.append('substituting a different source occurrence rejected')
changed_sections=copy.deepcopy(new);changed_sections['expressions'][0]['path']+='/call:999999999'
changed_sections.pop('_occurrence_indexes',None)
assert not namespace['same_source_occurrence'](before,after,old,changed_sections)
checks.append('substituting a different internal call frame rejected')
changed={**after,'value_expression':'different-state-value'}
assert projection(old,before,'storage',{'conditions'})!=projection(new,changed,'storage',{'conditions'})
checks.append('changing the state value excluded before condition review')
output={'status':'PASS','image':image,'checks':checks,'cases':len(checks),'source_delta_key':key,
 'before_row':before,'after_row':after,'scope':'negative checks for the serialized-condition review only, not engine or whole business correctness',
 'reviewer_sha256':hashlib.sha256(script.read_bytes()).hexdigest(),'verifier_sha256':hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()}
(directory/'semantic-review-selfcheck.json').write_text(json.dumps(output,indent=2))
print('P2_STORAGE_REVIEW_NEGATIVE_CHECKS_PASS',len(checks),flush=True)
