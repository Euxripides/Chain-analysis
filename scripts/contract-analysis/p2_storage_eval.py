"""Evaluate frozen state semantics against a concrete owned-EVM witness.

The initial storage is a concrete array. Keccak applications are constrained by
actual trace preimages, never by an injectivity or separation axiom added here.
Each selected path and reported value must be uniquely determined.
"""
import ctypes as C
import ctypes.util
import json,re,sys

class ConcreteSolver:
    def __init__(self):
        self.lib=C.CDLL(ctypes.util.find_library('z3')or'libz3.so.4')
        for name,result,arguments in[
            ('Z3_mk_config',C.c_void_p,[]),('Z3_del_config',None,[C.c_void_p]),
            ('Z3_mk_context',C.c_void_p,[C.c_void_p]),('Z3_del_context',None,[C.c_void_p]),
            ('Z3_eval_smtlib2_string',C.c_char_p,[C.c_void_p,C.c_char_p]),('Z3_get_full_version',C.c_char_p,[])]:
            function=getattr(self.lib,name);function.restype=result;function.argtypes=arguments
        config=self.lib.Z3_mk_config();self.context=self.lib.Z3_mk_context(config);self.lib.Z3_del_config(config)
        self.errors=[];handler_type=C.CFUNCTYPE(None,C.c_void_p,C.c_int)
        self.handler=handler_type(lambda _,code:self.errors.append(code));self.lib.Z3_set_error_handler.argtypes=[C.c_void_p,handler_type]
        self.lib.Z3_set_error_handler(self.context,self.handler)
        self.evaluate('(set-option :timeout 2000)\n(set-option :rlimit 3000000)')
    def evaluate(self,text):
        assert len(text.encode())<=2<<20,'CONCRETE_SMT_TEXT_BUDGET'
        value=self.lib.Z3_eval_smtlib2_string(self.context,text.encode()).decode().strip()
        assert not self.errors and '(error 'not in value,'CONCRETE_SMT_ERROR:'+value[:400]
        return value
    def values(self,names):
        if not names:return{}
        raw=self.evaluate('(get-value ('+' '.join(names)+'))');tokens=iter(re.findall(r'\(|\)|[^\s()]+',raw))
        def read(first=None):
            token=next(tokens)if first is None else first
            if token!='(':return token
            rows=[]
            for token in tokens:
                if token==')':return rows
                rows.append(read(token))
            raise ValueError('UNTERMINATED_SMT_VALUE')
        def integer(value):
            if isinstance(value,list):assert len(value)==2 and value[0]=='-';return-int(value[1])
            if value in['true','false']:return value=='true'
            return int(value)
        return{name:integer(value)for name,value in read()}
    def close(self):self.lib.Z3_del_context(self.context)

document=json.load(sys.stdin)
model=document['semantics'];declarations=model['declarations']
assert model['schema_version']=='source-state-transition-v1'
assert model['initial_storage']=='storage_0'
assert model['complete']and not model['unresolved']
name_ok=lambda name:re.fullmatch(r'[a-zA-Z_][a-zA-Z0-9_]*',name)
number=lambda value:'(- '+str(-int(value))+')'if int(value)<0 else str(int(value))
initial='((as const (Array Int Int)) 0)'
for slot,value in sorted(document['pre_storage'].items(),key=lambda row:int(row[0])):
    initial='(store '+initial+' '+number(slot)+' '+number(value)+')'
assertions=['(= storage_0 '+initial+')',model['domain']]
for name,value in document['fixed_variables'].items():
    assert name_ok(name)
    literal=str(value).lower()if isinstance(value,bool)else number(value)
    assertions.append('(= '+name+' '+literal+')')
for item in document['hashes']:
    function='keccak_'+str(len(item['words']))
    if '(declare-fun '+function+' 'in declarations:
        assertions.append('(= ('+function+' '+' '.join(number(word)for word in item['words'])+') '+number(item['hash'])+')')
paths=[*(dict(path,kind='success')for path in model['paths']),*(dict(path,kind='revert')for path in model['reverted_paths'])]
conditions=[]
for index,path in enumerate(paths):
    name='condition_'+str(index);conditions.append(name)
    declarations+='\n(declare-const '+name+' Bool)'
    assertions.append('(= '+name+' '+path['condition']+')')
solver=ConcreteSolver()
solver.evaluate(declarations+'\n'+'\n'.join('(assert '+a+')'for a in assertions))
decision=solver.evaluate('(check-sat)')
assert decision=='sat','CONCRETE_DOMAIN_NOT_SAT:'+decision
expected='revert'if document['expected_reverted']else'success'
expected_conditions=[c for c,p in zip(conditions,paths)if p['kind']==expected]
opposite=solver.evaluate('(push)\n(assert (not (or '+' '.join(expected_conditions)+')))\n(check-sat)');solver.evaluate('(pop)')
assert opposite=='unsat','EXECUTION_OUTCOME_NOT_DETERMINED:'+opposite
assert solver.evaluate('(check-sat)')=='sat'
base=solver.values(conditions);active=[i for i,(c,p)in enumerate(zip(conditions,paths))if p['kind']==expected and base[c]is True]
assert active,'NO_CONCRETE_PATH'
records=[]
for index in active:
    path=paths[index];writes=path.get('writes',path.get('attempted_writes',[]));observations=[]
    for ordinal,write in enumerate(writes):
        for key in['slot','old_value','new_value']:
            observations.append(('write_'+str(ordinal)+'_'+key,write[key]))
        offset=write['offset'];observations.append(('write_'+str(ordinal)+'_offset',str(offset)))
    final=path['storage_after']
    for ordinal,slot in enumerate(document['observe_slots']):observations.append(('final_'+str(ordinal),'(select '+final+' '+number(slot)+')'))
    # Parse with the same declarations to bind observation expressions; retain
    # all concrete constraints in this solver and select this exact path.
    extra='(push)\n(assert '+conditions[index]+')'
    for name,expression in observations:extra+='\n(declare-const '+name+' Int)\n(assert (= '+name+' '+expression+'))'
    solver.evaluate(extra);assert solver.evaluate('(check-sat)')=='sat','SELECTED_PATH_NOT_SAT'
    values=solver.values([name for name,_ in observations]);different=['(distinct '+name+' '+number(value)+')'for name,value in values.items()]
    unique=solver.evaluate('(push)\n(assert (or '+' '.join(different)+'))\n(check-sat)');solver.evaluate('(pop)')
    assert unique=='unsat','CONCRETE_OBSERVATION_NOT_UNIQUE:'+unique
    rows=[]
    for ordinal,write in enumerate(writes):
        rows.append({**{key:str(values['write_'+str(ordinal)+'_'+key])for key in['slot','old_value','new_value','offset']},
                     'root_ast_id':write['root_ast_id'],'bits':write['bits']})
    records.append({'path_id':path['path_id'],'outcome':expected,'writes':rows,
                    'final_storage':{str(slot):str(values['final_'+str(ordinal)])for ordinal,slot in enumerate(document['observe_slots'])},
                    'all_reported_values_unique':True})
    solver.evaluate('(pop)')
json.dump({'status':'PASS','paths':records,'outcome_determined':True,'hash_semantics':'only_concrete_trace_preimage_bindings','solver':solver.lib.Z3_get_full_version().decode()},sys.stdout)
solver.close()
