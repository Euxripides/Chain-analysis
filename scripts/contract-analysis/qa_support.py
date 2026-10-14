"""Private QA helpers. Use only the isolated contract_analysis_qa database."""
import json, os, pathlib, secrets, http.cookiejar, urllib.request, urllib.error

ROOT = pathlib.Path(__file__).resolve().parents[2]
PRIVATE = pathlib.Path('/tmp/contract-analysis-private-20261014')
BASE = os.environ.get('CONTRACT_QA_API', 'http://127.0.0.1:18081')

def dotenv(path):
    values = {}
    for line in pathlib.Path(path).read_text().splitlines():
        if '=' in line and not line.lstrip().startswith('#'):
            key, value = line.split('=', 1)
            values[key.strip()] = value.strip().strip('"').strip("'")
    return values

class Client:
    def __init__(self, token=None):
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar))
        self.token = token
    def call(self, path, body=None, method=None, csrf=True):
        headers = {'Content-Type': 'application/json', 'Origin': BASE, 'User-Agent': 'contract-analysis-qa/1'}
        if self.token: headers['Authorization'] = 'Bearer ' + self.token
        if csrf:
            for cookie in self.jar:
                if cookie.name.endswith('_csrf'): headers['X-CSRF-Token'] = cookie.value
        request = urllib.request.Request(BASE+path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method or ('POST' if body is not None else 'GET'))
        try:
            with self.opener.open(request, timeout=55) as response: return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            try: payload=json.load(error)
            except Exception: payload={'error':{'code':'NON_JSON_RESPONSE'}}
            return error.code, payload
    def must(self, path, body=None):
        status, result=self.call(path,body)
        if status!=200 or result.get('error'): raise RuntimeError(str((path,status,result.get('error'))))
        return result['data']
    def login(self, actor):
        result=self.must('/api/auth/v1/login', {'identifier':actor['email'],'password':actor['password'],'audience':'user','workspace_id':actor['workspace']})
        return result
    def browser_state(self):
        return {'cookies':[{'name':c.name,'value':c.value,'domain':'127.0.0.1','path':c.path,'httpOnly':c.has_nonstandard_attr('HttpOnly'),'secure':c.secure,'sameSite':'Lax','expires':c.expires or -1} for c in self.jar], 'origins':[]}

def actors():
    stored=PRIVATE/'actors.json'
    if stored.exists(): return json.loads(stored.read_text())
    rows=[]
    for line in (PRIVATE/'credentials.env').read_text().splitlines():
        if '=' not in line: continue
        key,value=line.split('=',1)
        if key.endswith('_EMAIL'): rows.append({'email':value})
        elif key.endswith('_PW'): rows[-1]['password']=value
        elif key.endswith('_WS'): rows[-1]['workspace']=value
    for row in rows:
        label='viewer' if 'viewer' in row['email'] else 'other' if 'other' in row['email'] else 'investigator'
        row['label']=label
        client=Client();client.login(row)
        row['password_next']='Ca1!'+secrets.token_urlsafe(24)
        # Persist before a reversible fixture-only change so interruption is recoverable.
        stored.write_text(json.dumps(rows));stored.chmod(0o600)
        client.must('/api/auth/v1/password/change',{'current_password':row['password'],'new_password':row['password_next']})
        row['password']=row.pop('password_next')
        stored.write_text(json.dumps(rows));stored.chmod(0o600)
    return rows

def fixture_clients():
    result={}
    for actor in actors():
        client=Client();client.login(actor)
        session=client.must('/api/auth/v1/session')
        if session['workspace_id'] != actor['workspace']: raise RuntimeError('fixture workspace mismatch')
        state=PRIVATE/(actor['label']+'-browser.json');state.write_text(json.dumps(client.browser_state()));state.chmod(0o600)
        result[actor['label']]=client
    return result

if __name__=='__main__':
    for name,client in fixture_clients().items():
        session=client.must('/api/auth/v1/session')
        print(json.dumps({'actor':name,'workspace':session['workspace_id'],'permissions':session['permissions'],'must_change_password':session['must_change_password']}))
