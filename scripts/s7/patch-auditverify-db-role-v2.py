#!/usr/bin/env python3
# patch-auditverify-db-role-v2.py -- hardening of the S7-DR SET ROLE path in cmd/auditverify.
#
# Root requirements (15:34): safe SET ROLE via pgxpool AfterConnect, but the role name is never raw input;
# only a strictly validated/quoted identifier is accepted; after connecting, verify current_user /
# session_user / current_role; no role, invalid role, missing membership and failed SET ROLE must ALL fail
# closed; the app credential must really read audit_events_v2 AND audit_integrity_incidents; no PUBLIC grants.
#
# v1 (already applied, preserved) introduced -db-role + AfterConnect + session/current user output.
# v2 adds: regexp identifier validation (fail closed before connecting), effective-identity verification,
# and a real-read capability probe over exactly the two tables DR verification depends on.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/backend/cmd/auditverify/main.go"
EXPECT_SRC_SHA = "49847367bb7d942e722cced0918b555a570cd55db081272d0dba637fb040fc0d"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL baseline sha mismatch (want %s)" % EXPECT_SRC_SHA)
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("import-regexp",
  '\t"os"\n\t"strings"',
  '\t"os"\n\t"regexp"\n\t"strings"'))

P.append(("role-var",
  'func main() {\n\tchainsFlag := flag.String',
  '// reRoleName keeps -db-role to a plain lower-case Postgres identifier: no quotes, spaces, dots or upper\n'
  '// case, i.e. nothing that could change the meaning of the SET ROLE statement. Anything else is rejected\n'
  '// before a connection is even attempted.\n'
  'var reRoleName = regexp.MustCompile(`^[a-z_][a-z0-9_$]{0,62}$`)\n'
  '\n'
  'func main() {\n\tchainsFlag := flag.String'))

P.append(("flag-validate",
  '\tdbRole := flag.String("db-role", os.Getenv("DB_RUNTIME_ROLE"),\n'
  '\t\t"SET ROLE to this role after connecting (app credential does not inherit member privileges); empty = no switch")\n'
  '\tflag.Parse()',
  '\tdbRole := flag.String("db-role", os.Getenv("DB_RUNTIME_ROLE"),\n'
  '\t\t"SET ROLE to this role after connecting (app credential does not inherit member privileges); empty = no switch")\n'
  '\tflag.Parse()\n'
  '\troleName := strings.TrimSpace(*dbRole)\n'
  '\t// Fail closed on anything that is not a plain identifier: no partial parsing, no guessing.\n'
  '\tif roleName != "" && !reRoleName.MatchString(roleName) {\n'
  '\t\tfmt.Fprintf(os.Stderr, "invalid -db-role %q: must match %s\\n", roleName, reRoleName.String())\n'
  '\t\tos.Exit(2)\n'
  '\t}'))

P.append(("afterconnect",
  '\tif *dbRole != "" {\n'
  '\t\trole := pgx.Identifier{*dbRole}.Sanitize()\n'
  '\t\tcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {\n'
  '\t\t\t_, err := conn.Exec(ctx, "SET ROLE "+role)\n'
  '\t\t\treturn err\n'
  '\t\t}\n'
  '\t}',
  '\tif roleName != "" {\n'
  '\t\t// Identifier quoting on top of the regexp check. A failed SET ROLE makes the connection\n'
  '\t\t// unusable and the error surfaces from Acquire, so the process cannot continue as the login\n'
  '\t\t// identity by accident (missing membership, missing role, revoked role -> fail closed).\n'
  '\t\trole := pgx.Identifier{roleName}.Sanitize()\n'
  '\t\tcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {\n'
  '\t\t\tif _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {\n'
  '\t\t\t\treturn fmt.Errorf("SET ROLE %s failed: %w", roleName, err)\n'
  '\t\t\t}\n'
  '\t\t\treturn nil\n'
  '\t\t}\n'
  '\t}'))

P.append(("output-struct",
  '\ttype output struct {\n'
  '\t\tCheckedAt   time.Time                 `json:"checked_at"`\n'
  '\t\tDBRole      string                    `json:"db_role"`\n'
  '\t\tSessionUser string                    `json:"session_user"`\n'
  '\t\tCurrentUser string                    `json:"current_user"`\n'
  '\t\tResults     []audit.VerifyChainResult `json:"results"`\n'
  '\t\tOK          bool                      `json:"ok"`\n'
  '\t}\n'
  '\tout := output{CheckedAt: time.Now().UTC(), OK: true, DBRole: *dbRole, Results: []audit.VerifyChainResult{}}',
  '\ttype output struct {\n'
  '\t\tCheckedAt   time.Time                 `json:"checked_at"`\n'
  '\t\tDBRole      string                    `json:"db_role"`\n'
  '\t\tSessionUser string                    `json:"session_user"`\n'
  '\t\tCurrentUser string                    `json:"current_user"`\n'
  '\t\tCurrentRole string                    `json:"current_role"`\n'
  '\t\tCapability  map[string]int64          `json:"capability_probe"`\n'
  '\t\tResults     []audit.VerifyChainResult `json:"results"`\n'
  '\t\tOK          bool                      `json:"ok"`\n'
  '\t}\n'
  '\tout := output{CheckedAt: time.Now().UTC(), OK: true, DBRole: roleName, Capability: map[string]int64{},\n'
  '\t\tResults: []audit.VerifyChainResult{}}'))

P.append(("identity-probe",
  '\t// Evidence: the login identity (session_user = app credential) and the query identity\n'
  '\t// (current_user = runtime role) are recorded separately.\n'
  '\tif err := pool.QueryRow(ctx, "SELECT session_user, current_user").Scan(&out.SessionUser, &out.CurrentUser); err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "读取会话身份失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}',
  '\t// Evidence + fail-closed identity check: session_user is the login identity (the app credential),\n'
  '\t// current_user/current_role are the effective identity. When -db-role was requested they MUST match\n'
  '\t// it, otherwise the chain could be verified as the wrong principal without anyone noticing.\n'
  '\tif err := pool.QueryRow(ctx, "SELECT session_user, current_user, current_role").Scan(\n'
  '\t\t&out.SessionUser, &out.CurrentUser, &out.CurrentRole); err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "读取会话身份失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}\n'
  '\tif roleName != "" && out.CurrentUser != roleName {\n'
  '\t\tfmt.Fprintf(os.Stderr, "SET ROLE 校验失败: current_user=%s 与请求角色 %s 不一致\\n", out.CurrentUser, roleName)\n'
  '\t\tos.Exit(2)\n'
  '\t}\n'
  '\t// Capability probe: DR chain verification reads exactly these two tables. A real read (not a\n'
  '\t// privilege lookup) is what fails closed when the credential cannot reach them.\n'
  '\tfor _, p := range []struct {\n'
  '\t\tname string\n'
  '\t\tsql  string\n'
  '\t}{\n'
  '\t\t{"audit_events_v2", "SELECT count(*) FROM audit_events_v2"},\n'
  '\t\t{"audit_integrity_incidents", "SELECT count(*) FROM audit_integrity_incidents"},\n'
  '\t} {\n'
  '\t\tvar n int64\n'
  '\t\tif err := pool.QueryRow(ctx, p.sql).Scan(&n); err != nil {\n'
  '\t\t\tfmt.Fprintf(os.Stderr, "能力探测失败 %s: %v\\n", p.name, err)\n'
  '\t\t\tos.Exit(2)\n'
  '\t\t}\n'
  '\t\tout.Capability[p.name] = n\n'
  '\t}'))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new, 1); print("applied %s" % tag)

open(SRC, "w", encoding="utf-8", newline="\n").write(text)
out = open(SRC, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (SRC, len(out), hashlib.sha256(out).hexdigest()))
