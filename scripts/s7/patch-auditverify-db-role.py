#!/usr/bin/env python3
# patch-auditverify-db-role.py -- S7-DR fix: let the standalone chain verifier run with the APPLICATION
# credential, which is what a DR drill actually has.
#
# Measured root cause (r10v8 audit-chain-gate G2, preserved as evidence): with DATABASE_URL
# (user=blockchain_api) the verifier exits rc=2 with
#   ERROR: permission denied for table audit_integrity_incidents (SQLSTATE 42501)
# because blockchain_api is a member of blockchain_app but rolinherit=false: membership alone grants
# nothing, and the API process itself only works because it issues SET ROLE blockchain_app
# (DB_RUNTIME_ROLE). auditverify never did, so "verify the chain after a restore" was not achievable
# with the app credential at all.
#
# Fix (least privilege preserved: NO new GRANTs are issued): auditverify accepts -db-role (defaulting to
# DB_RUNTIME_ROLE) and issues SET ROLE on every pooled connection via pgxpool AfterConnect, exactly like
# the app. The JSON output records session_user vs current_user so the evidence proves the app credential
# stayed the login identity while the queries ran as the runtime role.
import hashlib, os, sys

R = os.environ.get("R10_GEN_ROOT", "/home/euripides/projects/blockchain-platform")
SRC = sys.argv[1] if len(sys.argv) > 1 else R + "/backend/cmd/auditverify/main.go"
EXPECT_SRC_SHA = "ea6ccc4a303d96bf207c52243de29fee7ce2ac17d3a312c845c8a6a12648520a"

raw = open(SRC, "rb").read()
sha = hashlib.sha256(raw).hexdigest()
print("src_sha256=%s" % sha)
if sha != EXPECT_SRC_SHA:
    print("FATAL baseline sha mismatch (want %s)" % EXPECT_SRC_SHA)
    sys.exit(3)
text = raw.decode("utf-8")
P = []

P.append(("flag",
  '\ttimeout := flag.Duration("timeout", 60*time.Second, "整体超时")\n\tflag.Parse()',
  '\ttimeout := flag.Duration("timeout", 60*time.Second, "整体超时")\n'
  '\t// S7-DR fix (2026-09-13): the app credential blockchain_api is a member of blockchain_app but\n'
  '\t// rolinherit=false, so querying the audit tables directly fails with permission denied (which made\n'
  '\t// "verify the chain after a restore" impossible). The API process relies on SET ROLE to the runtime\n'
  '\t// role; the standalone verifier must take the same path. No role => previous behaviour (no switch),\n'
  '\t// so privileges are never silently widened.\n'
  '\tdbRole := flag.String("db-role", os.Getenv("DB_RUNTIME_ROLE"),\n'
  '\t\t"SET ROLE to this role after connecting (app credential does not inherit member privileges); empty = no switch")\n'
  '\tflag.Parse()'))

P.append(("import-pgx",
  '\t"github.com/jackc/pgx/v5/pgxpool"',
  '\t"github.com/jackc/pgx/v5"\n\t"github.com/jackc/pgx/v5/pgxpool"'))

P.append(("pool-afterconnect",
  '\tpool, err := pgxpool.New(ctx, dsn)\n'
  '\tif err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "连接数据库失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}',
  '\tcfg, err := pgxpool.ParseConfig(dsn)\n'
  '\tif err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "连接参数解析失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}\n'
  '\tif *dbRole != "" {\n'
  '\t\trole := pgx.Identifier{*dbRole}.Sanitize()\n'
  '\t\tcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {\n'
  '\t\t\t_, err := conn.Exec(ctx, "SET ROLE "+role)\n'
  '\t\t\treturn err\n'
  '\t\t}\n'
  '\t}\n'
  '\tpool, err := pgxpool.NewWithConfig(ctx, cfg)\n'
  '\tif err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "连接数据库失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}'))

P.append(("output-struct",
  '\ttype output struct {\n'
  '\t\tCheckedAt time.Time                 `json:"checked_at"`\n'
  '\t\tResults   []audit.VerifyChainResult `json:"results"`\n'
  '\t\tOK        bool                      `json:"ok"`\n'
  '\t}\n'
  '\tout := output{CheckedAt: time.Now().UTC(), OK: true, Results: []audit.VerifyChainResult{}}',
  '\ttype output struct {\n'
  '\t\tCheckedAt   time.Time                 `json:"checked_at"`\n'
  '\t\tDBRole      string                    `json:"db_role"`\n'
  '\t\tSessionUser string                    `json:"session_user"`\n'
  '\t\tCurrentUser string                    `json:"current_user"`\n'
  '\t\tResults     []audit.VerifyChainResult `json:"results"`\n'
  '\t\tOK          bool                      `json:"ok"`\n'
  '\t}\n'
  '\tout := output{CheckedAt: time.Now().UTC(), OK: true, DBRole: *dbRole, Results: []audit.VerifyChainResult{}}\n'
  '\t// Evidence: the login identity (session_user = app credential) and the query identity\n'
  '\t// (current_user = runtime role) are recorded separately.\n'
  '\tif err := pool.QueryRow(ctx, "SELECT session_user, current_user").Scan(&out.SessionUser, &out.CurrentUser); err != nil {\n'
  '\t\tfmt.Fprintf(os.Stderr, "读取会话身份失败: %v\\n", err)\n'
  '\t\tos.Exit(2)\n'
  '\t}'))

for tag, old, new in P:
    n = text.count(old)
    if n != 1:
        print("FATAL patch %s matched %d times (want 1)" % (tag, n)); sys.exit(4)
    text = text.replace(old, new, 1); print("applied %s" % tag)

open(SRC, "w", encoding="utf-8", newline="\n").write(text)
out = open(SRC, "rb").read()
print("dst=%s bytes=%d sha256=%s" % (SRC, len(out), hashlib.sha256(out).hexdigest()))
