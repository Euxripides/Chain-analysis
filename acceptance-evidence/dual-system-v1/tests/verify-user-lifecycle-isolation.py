#!/usr/bin/env python3
# -*- coding: utf-8 -*-
# S8 后端能力验证（隔离夹具组织；Python 驱动，避免 shell 引号问题）
import json, os, subprocess, sys, time, urllib.parse as up

BASE = 'http://127.0.0.1:8080'
ROOT = '/home/euripides/projects/blockchain-platform'
ORG_SLUG = 's8verify6'; WS_SLUG = 's8v6-ws'
O_EMAIL = 's8v6-owner@%s.local' % ORG_SLUG
P_EMAIL = 's8v6-plat@%s.local' % ORG_SLUG
V_EMAIL = 's8v6-viewer@%s.local' % ORG_SLUG
PW = 'S8v-Verify-2026!aA'
PASS = 0; FAIL = 0

def _env():
    # 把 deploy/.env 的全部键灌入子进程环境（fixture/migrate 需要 MIGRATION_DATABASE_URL 等），
    # 同时从迁移 DSN 里取出 PGPASSWORD 供 psql 使用。
    env = dict(os.environ)
    for line in open(ROOT + '/deploy/.env', encoding='utf-8'):
        line = line.strip()
        if not line or line.startswith('#') or '=' not in line:
            continue
        k, v = line.split('=', 1)
        env[k] = v.strip().strip('"')
    env['PGPASSWORD'] = up.urlparse(env.get('MIGRATION_DATABASE_URL', '')).password or ''
    return env

ENV = _env()
PSQL = ['psql', '-h', '127.0.0.1', '-p', '5433', '-U', 'blockchain', '-d', 'blockchain_platform', '-At']

def q(sql):
    p = subprocess.run(PSQL + ['-c', sql], capture_output=True, text=True, env=ENV)
    return p.stdout.strip()

def check(name, got, want):
    global PASS, FAIL
    ok = str(got) == str(want)
    if ok: PASS += 1
    else: FAIL += 1
    print('  %s  %-52s %s%s' % ('PASS' if ok else 'FAIL', name, got, '' if ok else '   (want=%s)' % want))

def curl(args, body_file='/tmp/_body.json'):
    cmd = ['curl', '-s', '-o', body_file, '-w', '%{http_code}', '--max-time', '25'] + args
    p = subprocess.run(cmd, capture_output=True, text=True)
    body = ''
    if os.path.exists(body_file):
        body = open(body_file, encoding='utf-8').read()
    return p.stdout.strip(), body

def login(jar, ident, pw, aud):
    payload = json.dumps({'identifier': ident, 'password': pw, 'audience': aud})
    code, _ = curl(['-c', jar, '-H', 'Content-Type: application/json', '-d', payload, BASE + '/api/auth/v1/login'])
    return code

def sess(jar, out='/tmp/_sess.json'):
    code, _ = curl(['-b', jar, '-o', out, BASE + '/api/auth/v1/session'], body_file=out)
    try:
        return code, (json.load(open(out, encoding='utf-8')).get('data') or {})
    except Exception:
        return code, {}

def first_login(jar, ident, pw, aud):
    login(jar, ident, pw, aud)
    _, data = sess(jar)
    csrf = data.get('csrf_token') or ''
    payload = json.dumps({'current_password': pw, 'new_password': PW})
    code, _ = curl(['-b', jar, '-H', 'X-CSRF-Token: ' + csrf, '-H', 'Content-Type: application/json',
                    '-X', 'POST', '-d', payload, BASE + '/api/auth/v1/password/change'])
    return code

def sh(cmd):
    p = subprocess.run(cmd, shell=True, capture_output=True, text=True, env=ENV, cwd=ROOT + '/backend')
    return p.returncode, (p.stdout + p.stderr).strip()

print('== 0. 既有数据快照 ==')
FILTER = "o.slug not like 's8verify%'"
REAL_SHARED = q('select count(*) from workspaces w join organizations o on o.id=w.organization_id where w.kind=%s and %s' % ("'shared'", FILTER))
REAL_WS = q('select count(*) from workspaces w join organizations o on o.id=w.organization_id where %s' % FILTER)
print('  既有 shared=%s 既有工作区总数=%s' % (REAL_SHARED, REAL_WS))

print('== 1. 隔离夹具 ==')
rc, out = sh('PATH=/usr/local/go/bin:$PATH go build -o /tmp/fixture-identity ./cmd/fixture-identity')
if rc != 0:
    print('  build fixture failed:', out); sys.exit(2)
rc, out = sh('/tmp/fixture-identity -org-slug %s -org-name "S8 验证（隔离夹具 v6）" -workspaces %s -identities "owner:%s:%s,platform_admin:%s:%s,viewer:%s:%s" -out /tmp/s8v5.env'
             % (ORG_SLUG, WS_SLUG, O_EMAIL, WS_SLUG, P_EMAIL, WS_SLUG, V_EMAIL, WS_SLUG))
if rc != 0:
    print('  fixture failed:', out); sys.exit(2)
creds = {}
for line in open('/tmp/s8v5.env', encoding='utf-8'):
    if '=' in line:
        k, v = line.strip().split('=', 1); creds[k] = v
ORG = creds['S7JS_ORG_ID']
_M = "'%s','%s','%s'" % (O_EMAIL, P_EMAIL, V_EMAIL)
# 幂等重跑：上一轮结束时会停用夹具身份，这里把它们恢复成可登录状态（仅限本隔离组织）
q("update identities set status='active', deleted_at=null, must_change_password=true where email in (%s)" % _M)
q("update organization_memberships set status='active' where identity_id in (select id from identities where email in (%s))" % _M)
q("update workspace_memberships set status='active' where identity_id in (select id from identities where email in (%s))" % _M)
q("update member_role_bindings set revoked_at=null where identity_id in (select id from identities where email in (%s))" % _M)
print('  org=%s' % ORG)

print('== 2. owner（真实管理员角色）：建号即建个人空间 ==')
check('owner 首登改密', first_login('/tmp/j_owner', O_EMAIL, creds['S7JS_OWNER_PW'], 'admin'), '200')
check('owner 重新登录', login('/tmp/j_owner', O_EMAIL, PW, 'admin'), '200')
_, s_owner = sess('/tmp/j_owner', '/tmp/s_owner.json')
CSRF = s_owner.get('csrf_token') or ''
UEMAIL = 's8v6-user-%d@%s.local' % (int(time.time()), ORG_SLUG)
payload = json.dumps({'email': UEMAIL, 'display_name': 'S8 验证用户', 'reason': 'S8 验证：建号即建个人空间'})
code, body = curl(['-b', '/tmp/j_owner', '-H', 'X-CSRF-Token: ' + CSRF, '-H', 'Content-Type: application/json',
                   '-X', 'POST', '-d', payload, BASE + '/api/admin/v1/users'])
print('  create=%s body=%s' % (code, body[:160]))
check('创建用户 HTTP', code, '201')
try:
    cdata = json.loads(body).get('data') or {}
except Exception:
    cdata = {}
NEWID = cdata.get('id') or ''; UPW = cdata.get('initial_password') or ''; PWSID = cdata.get('personal_workspace_id') or ''
check('返回 personal_workspace_id', 'yes' if PWSID else 'no', 'yes')
check('一次性初始密码已返回', 'yes' if UPW else 'no', 'yes')
check('个人空间 kind=personal', q("select kind from workspaces where id='%s'" % PWSID), 'personal')
check('个人空间 owner=新用户', q("select owner_identity_id from workspaces where id='%s'" % PWSID), NEWID)
check('个人空间 active 成员关系', q("select count(*) from workspace_memberships where workspace_id='%s' and identity_id='%s' and status='active'" % (PWSID, NEWID)), '1')
check('个人空间绑定 basic_user', q("select r.key from member_role_bindings b join roles r on r.id=b.role_id where b.identity_id='%s' and b.scope_id='%s' and b.revoked_at is null" % (NEWID, PWSID)), 'basic_user')
check('新用户不在共享工作区', q("select count(*) from workspace_memberships m join workspaces w on w.id=m.workspace_id where m.identity_id='%s' and w.kind='shared'" % NEWID), '0')

print('== 3. 新用户会话无 admin:* ==')
check('新用户登录', login('/tmp/j_user', UEMAIL, UPW, 'user'), '200')
_, s_user = sess('/tmp/j_user', '/tmp/s_user.json')
perms = s_user.get('permissions') or []
check('新用户 admin:* 数', len([p for p in perms if p.startswith('admin:')]), '0')
check('新用户权限数 >= 50', 'yes' if len(perms) >= 50 else 'no', 'yes')
check('新用户默认空间=个人空间', s_user.get('workspace_id'), PWSID)

print('== 4. 删除权限边界（管理员=全权；platform_admin/security_admin 亦有删除权）==')
own = s_owner.get('permissions') or []
check('owner 权限数 = 权限目录总数', len(own), q('select count(*) from permissions'))
check('owner 拥有 admin:user:delete', 'admin:user:delete' in own, 'True')
code, _ = curl(['-b', '/tmp/j_owner', '-H', 'X-CSRF-Token: ' + CSRF, '-H', 'Content-Type: application/json',
                '-X', 'DELETE', '-d', json.dumps({'reason': 'no confirm'}), BASE + '/api/admin/v1/users/' + NEWID])
check('缺少二次确认 → 428', code, '428')
code, _ = curl(['-b', '/tmp/j_owner', '-H', 'X-CSRF-Token: ' + CSRF, '-H', 'Content-Type: application/json',
                '-X', 'DELETE', '-d', json.dumps({'confirm': True, 'reason': 'S8 验证：管理员执行离职删除'}), BASE + '/api/admin/v1/users/' + NEWID])
check('owner（管理员）删除 → 200', code, '200')
check('platform_admin 首登改密', first_login('/tmp/j_plat', P_EMAIL, creds['S7JS_PLATFORM_ADMIN_PW'], 'admin'), '200')
check('platform_admin 重新登录', login('/tmp/j_plat', P_EMAIL, PW, 'admin'), '200')
_, s_plat = sess('/tmp/j_plat', '/tmp/s_plat.json')
CSRF2 = s_plat.get('csrf_token') or ''
check('platform_admin 无 admin:user:create', 'admin:user:create' in (s_plat.get('permissions') or []), 'False')
check('platform_admin 有 admin:user:delete', 'admin:user:delete' in (s_plat.get('permissions') or []), 'True')
code, _ = curl(['-b', '/tmp/j_plat', '-H', 'X-CSRF-Token: ' + CSRF2, '-H', 'Content-Type: application/json',
                '-X', 'POST', '-d', json.dumps({'email': 'x@%s.local' % ORG_SLUG, 'reason': 'denied'}), BASE + '/api/admin/v1/users'])
check('platform_admin 创建 → 403', code, '403')
code, _ = curl(['-b', '/tmp/j_plat', '-H', 'X-CSRF-Token: ' + CSRF2, '-H', 'Content-Type: application/json',
                '-X', 'DELETE', '-d', json.dumps({'confirm': True, 'reason': 'already deleted'}), BASE + '/api/admin/v1/users/' + NEWID])
check('对已删用户重复删除 → 409', code, '409')

print('== 5. 软删 / 匿名化 / 归档 / 撤权 ==')
check('status=deleted', q("select status from identities where id='%s'" % NEWID), 'deleted')
check('deleted_at 已写', q("select (deleted_at is not null)::text from identities where id='%s'" % NEWID), 'true')
check('邮箱匿名化', q("select (email like 'deleted+%%@invalid')::text from identities where id='%s'" % NEWID), 'true')
check('显示名匿名化', q("select display_name from identities where id='%s'" % NEWID), '已删除用户')
check('个人空间已归档', q("select (archived_at is not null)::text from workspaces where id='%s'" % PWSID), 'true')
check('角色绑定已撤销', q("select count(*) from member_role_bindings where identity_id='%s' and revoked_at is null" % NEWID), '0')
check('工作区成员关系移除', q("select count(*) from workspace_memberships where identity_id='%s' and status<>'removed'" % NEWID), '0')
check('组织成员关系移除', q("select count(*) from organization_memberships where identity_id='%s' and status<>'removed'" % NEWID), '0')
check('身份行保留', q("select count(*) from identities where id='%s'" % NEWID), '1')
code, _ = curl(['-b', '/tmp/j_owner', '-H', 'X-CSRF-Token: ' + CSRF, '-H', 'Content-Type: application/json',
                '-X', 'DELETE', '-d', json.dumps({'confirm': True, 'reason': 'repeat'}), BASE + '/api/admin/v1/users/' + NEWID])
check('重复删除 → 409', code, '409')

print('== 6. 已删除账号无法登录 ==')
ANON = q("select email from identities where id='%s'" % NEWID)
code, _ = curl(['-c', '/tmp/j_del', '-H', 'Content-Type: application/json', '-d', json.dumps({'identifier': ANON, 'password': UPW, 'audience': 'user'}), BASE + '/api/auth/v1/login'])
del_body = open('/tmp/_body.json', encoding='utf-8').read()
del_code = (json.loads(del_body).get('error') or {}).get('code')
check('已删除账号登录被拒（与停用同形）', code, '403')
check('错误码 = AUTHZ_ACCOUNT_DISABLED', del_code, 'AUTHZ_ACCOUNT_DISABLED')
check('未下发任何会话 Cookie', 'cs_user_session' in open('/tmp/j_del').read(), 'False')
check('该身份无有效会话', q("select count(*) from sessions where identity_id='%s' and revoked_at is null" % NEWID), '0')
check('登录事件 reason=account_deleted', q("select count(*) from login_events where identity_id='%s' and reason_code='account_deleted'" % NEWID), '1')
code, _ = sess('/tmp/j_user', '/tmp/x.json')
check('删除前旧会话已失效', code, '401')

print('== 7. viewer 双端点 403 ==')
check('viewer 首登改密', first_login('/tmp/j_view', V_EMAIL, creds['S7JS_VIEWER_PW'], 'admin'), '200')
check('viewer 重新登录', login('/tmp/j_view', V_EMAIL, PW, 'admin'), '200')
_, s_view = sess('/tmp/j_view', '/tmp/s_view.json')
CSRF3 = s_view.get('csrf_token') or ''
code, _ = curl(['-b', '/tmp/j_view', '-H', 'X-CSRF-Token: ' + CSRF3, '-H', 'Content-Type: application/json',
                '-X', 'POST', '-d', json.dumps({'email': 'y@%s.local' % ORG_SLUG, 'reason': 'denied'}), BASE + '/api/admin/v1/users'])
check('viewer 创建 → 403', code, '403')
code, _ = curl(['-b', '/tmp/j_view', '-H', 'X-CSRF-Token: ' + CSRF3, '-H', 'Content-Type: application/json',
                '-X', 'DELETE', '-d', json.dumps({'confirm': True, 'reason': 'denied'}), BASE + '/api/admin/v1/users/' + NEWID])
check('viewer 删除 → 403', code, '403')

print('== 8. 权限目录与既有数据不变 ==')
check('basic_user 的 admin:* 数', q("select count(*) from role_permissions rp join roles r on r.id=rp.role_id where r.key='basic_user' and rp.permission_key like 'admin:%%'"), '0')
check('admin:user:delete 授予范围', q("select string_agg(r.key, ',' order by r.key) from role_permissions rp join roles r on r.id=rp.role_id where rp.permission_key='admin:user:delete'"), 'owner,platform_admin,security_admin')
check('owner 权限数 = 权限目录总数（复查）', q("select count(*) from role_permissions rp join roles r on r.id=rp.role_id where r.key='owner'"), q('select count(*) from permissions'))
check('admin:user:create 授予范围（未改动）', q("select string_agg(r.key, ',' order by r.key) from role_permissions rp join roles r on r.id=rp.role_id where rp.permission_key='admin:user:create'"), 'owner')
check('既有 shared 工作区数不变', q('select count(*) from workspaces w join organizations o on o.id=w.organization_id where w.kind=%s and %s' % ("'shared'", FILTER)), REAL_SHARED)
check('既有工作区总数不变', q('select count(*) from workspaces w join organizations o on o.id=w.organization_id where %s' % FILTER), REAL_WS)

print('== 9. 清理夹具身份 ==')
M = "'%s','%s','%s'" % (O_EMAIL, P_EMAIL, V_EMAIL)
q("update identities set status='disabled', updated_at=now() where email in (%s)" % M)
q("update member_role_bindings set revoked_at=now() where identity_id in (select id from identities where email in (%s)) and revoked_at is null" % M)
q("update workspace_memberships set status='removed' where identity_id in (select id from identities where email in (%s))" % M)
q("update organization_memberships set status='removed' where identity_id in (select id from identities where email in (%s))" % M)
check('夹具身份已停用', q("select count(*) from identities where email in (%s) and status='disabled'" % M), '3')
print('  org=%s personal_ws=%s deleted_user=%s' % (ORG, PWSID, NEWID))
print('')
print('===== 结果：PASS=%d FAIL=%d =====' % (PASS, FAIL))
sys.exit(0 if FAIL == 0 else 1)
