// Command auditverify 对指定数据库执行审计哈希链校验（只读）。
//
// 用途（S7-DR-01 / S7-DOC-01）：
//   - 备份恢复演练后验证恢复库中的哈希链仍然自洽；
//   - 事故处置时在不启动 API 的前提下核对链状态。
//
// 用法：
//
//	DATABASE_URL=postgres://... ./auditverify [-chain audit,login] [-from 0] [-to 0]
//
// 退出码：0 = 所有链 valid=true 且 integrity_status 非 undeclared_gap/boundary_conflict；
//
//	1 = 存在校验失败（输出 JSON 供证据留档）。
package main

import (
	"context"

	"encoding/json"
	"flag"
	"fmt"
	"github.com/blockchain-platform/blockchain-platform/internal/dbtx"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blockchain-platform/blockchain-platform/internal/audit"
)

// reRoleName keeps -db-role to a plain lower-case Postgres identifier: no quotes, spaces, dots or upper
// case, i.e. nothing that could change the meaning of the SET ROLE statement. Anything else is rejected
// before a connection is even attempted.
var reRoleName = regexp.MustCompile(`^[a-z_][a-z0-9_$]{0,62}$`)

func main() {
	chainsFlag := flag.String("chain", "", "逗号分隔的链名；默认校验全部已注册链")
	from := flag.Int64("from", 0, "起始 seq（0 表示按链自身起点）")
	to := flag.Int64("to", 0, "结束 seq（0 表示到链头）")
	timeout := flag.Duration("timeout", 60*time.Second, "整体超时")
	// S7-DR fix (2026-09-13): the app credential blockchain_api is a member of blockchain_app but
	// rolinherit=false, so querying the audit tables directly fails with permission denied (which made
	// "verify the chain after a restore" impossible). The API process relies on SET ROLE to the runtime
	// role; the standalone verifier must take the same path. No role => previous behaviour (no switch),
	// so privileges are never silently widened.
	dbRole := flag.String("db-role", os.Getenv("DB_RUNTIME_ROLE"),
		"SET ROLE to this role after connecting (app credential does not inherit member privileges); empty = no switch")
	flag.Parse()
	roleName := strings.TrimSpace(*dbRole)
	// Fail closed on anything that is not a plain identifier: no partial parsing, no guessing.
	if roleName != "" && !reRoleName.MatchString(roleName) {
		fmt.Fprintf(os.Stderr, "invalid -db-role %q: must match %s\n", roleName, reRoleName.String())
		os.Exit(2)
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "缺少 DATABASE_URL")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接参数解析失败: %v\n", err)
		os.Exit(2)
	}
	if roleName != "" {
		// Identifier quoting on top of the regexp check. A failed SET ROLE makes the connection
		// unusable and the error surfaces from Acquire, so the process cannot continue as the login
		// identity by accident (missing membership, missing role, revoked role -> fail closed).
		role := pgx.Identifier{roleName}.Sanitize()
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			if _, err := conn.Exec(ctx, "SET ROLE "+role); err != nil {
				return fmt.Errorf("SET ROLE %s failed: %w", roleName, err)
			}
			return nil
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接数据库失败: %v\n", err)
		os.Exit(2)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "数据库不可达: %v\n", err)
		os.Exit(2)
	}

	chains := []string{audit.ChainAudit, audit.ChainLogin}
	if strings.TrimSpace(*chainsFlag) != "" {
		chains = strings.Split(*chainsFlag, ",")
		for i := range chains {
			chains[i] = strings.TrimSpace(chains[i])
		}
	}

	type output struct {
		CheckedAt   time.Time                 `json:"checked_at"`
		DBRole      string                    `json:"db_role"`
		SessionUser string                    `json:"session_user"`
		CurrentUser string                    `json:"current_user"`
		CurrentRole string                    `json:"current_role"`
		Capability  map[string]int64          `json:"capability_probe"`
		Results     []audit.VerifyChainResult `json:"results"`
		OK          bool                      `json:"ok"`
	}
	out := output{CheckedAt: time.Now().UTC(), OK: true, DBRole: roleName, Capability: map[string]int64{},
		Results: []audit.VerifyChainResult{}}
	// Evidence + fail-closed identity check: session_user is the login identity (the app credential),
	// current_user/current_role are the effective identity. When -db-role was requested they MUST match
	// it, otherwise the chain could be verified as the wrong principal without anyone noticing.
	if err := pool.QueryRow(ctx, "SELECT session_user, current_user, current_role").Scan(
		&out.SessionUser, &out.CurrentUser, &out.CurrentRole); err != nil {
		fmt.Fprintf(os.Stderr, "读取会话身份失败: %v\n", err)
		os.Exit(2)
	}
	if roleName != "" && out.CurrentUser != roleName {
		fmt.Fprintf(os.Stderr, "SET ROLE 校验失败: current_user=%s 与请求角色 %s 不一致\n", out.CurrentUser, roleName)
		os.Exit(2)
	}
	// Capability probe: DR chain verification reads exactly these two tables. A real read (not a
	// privilege lookup) is what fails closed when the credential cannot reach them.
	for _, p := range []struct {
		name string
		sql  string
	}{
		{"audit_events_v2", "SELECT count(*) FROM audit_events_v2"},
		{"audit_integrity_incidents", "SELECT count(*) FROM audit_integrity_incidents"},
	} {
		var n int64
		if err := pool.QueryRow(ctx, p.sql).Scan(&n); err != nil {
			fmt.Fprintf(os.Stderr, "能力探测失败 %s: %v\n", p.name, err)
			os.Exit(2)
		}
		out.Capability[p.name] = n
	}

	for _, chain := range chains {
		if chain == "" {
			continue
		}
		res, err := audit.VerifyChain(ctx, dbtx.New(pool), chain, *from, *to)
		if err != nil {
			fmt.Fprintf(os.Stderr, "链 %s 校验失败: %v\n", chain, err)
			os.Exit(2)
		}
		out.Results = append(out.Results, res)
		switch res.IntegrityStatus {
		case "intact", "sealed_incident", "declared_events":
			// 可接受：sealed_incident 表示连续缺口已按 ADR-018 走双人封存流程；
			// declared_events 表示无缺口但存在逐条声明的事实（ADR-019）。
			// 两者都必须携带非零的声明计数，否则状态名本身就成了"放行开关"。
			if res.IntegrityStatus == "declared_events" &&
				res.DeclaredLostCount+res.DeclaredAnomalyCount == 0 {
				out.OK = false
			}
		default:
			out.OK = false
		}
		if !res.Valid {
			out.OK = false
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
	if !out.OK {
		os.Exit(1)
	}
}
