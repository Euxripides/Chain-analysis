// admin-init：一次性管理员初始化工具（§28.4 S2-MIG-01）。
//
// 设计要求：不写入任何默认密码。密码只能由维护者通过标准输入或
// ADMIN_INITIAL_PASSWORD 环境变量提供，工具只保存 Argon2id 哈希。
//
// 用法：
//
//	go run ./cmd/admin-init -email admin@local
//	echo 'S3cure-Passphrase-Here' | go run ./cmd/admin-init -email admin@local -stdin
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/blockchain-platform/blockchain-platform/internal/config"
	"github.com/blockchain-platform/blockchain-platform/internal/iam"
	"github.com/blockchain-platform/blockchain-platform/internal/logger"
	"github.com/blockchain-platform/blockchain-platform/internal/store"
)

func main() {
	email := flag.String("email", "", "身份邮箱（默认取 AUTH_BOOTSTRAP_EMAIL，再取库中第一个身份）")
	fromStdin := flag.Bool("stdin", false, "从标准输入读取密码（管道场景）；默认从终端交互读取")
	force := flag.Bool("force", false, "即使已有密码凭据也允许覆盖")
	flag.Parse()

	ctx := context.Background()
	cfg := config.Load()
	log := logger.New("info")

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	repo := iam.NewRepo(st.Pool)
	target := strings.TrimSpace(*email)
	if target == "" {
		target = strings.TrimSpace(cfg.Auth.BootstrapEmail)
	}

	var identity iam.Identity
	if target != "" {
		identity, err = repo.GetIdentityByEmail(ctx, target)
	} else {
		identity, err = firstIdentity(ctx, repo)
	}
	if err != nil {
		log.Error("identity not found", "err", err, "email", target)
		os.Exit(1)
	}

	if identity.HasPassword && !*force {
		fmt.Fprintf(os.Stderr,
			"身份 %s 已有密码凭据。若确实需要重置，请使用管理员 API 或加 -force 重新运行。\n", identity.Email)
		os.Exit(2)
	}

	password, err := readPassword(*fromStdin)
	if err != nil {
		log.Error("read password failed", "err", err)
		os.Exit(1)
	}
	if len([]rune(password)) < cfg.Auth.MinPasswordLength {
		fmt.Fprintf(os.Stderr, "密码长度不足：至少需要 %d 个字符。\n", cfg.Auth.MinPasswordLength)
		os.Exit(3)
	}

	hash, err := iam.HashPassword(password, iam.DefaultParams())
	if err != nil {
		log.Error("hash failed", "err", err)
		os.Exit(1)
	}
	if err := repo.SetPassword(ctx, identity.ID, hash, true); err != nil {
		log.Error("set password failed", "err", err)
		os.Exit(1)
	}
	_ = iam.RecordLoginEvent(ctx, st.Pool, cfg.Env, iam.LoginEvent{
		EventType: iam.EventPasswordReset, Result: "success", IdentityID: identity.ID,
		SubjectEmail: identity.Email, Audience: iam.AudienceAdmin, RiskLevel: "critical",
		Detail: map[string]any{"source": "admin-init-cli", "must_change_password": true, "forced": *force},
	})

	fmt.Printf("已为 %s 设置初始密码（要求首次登录后修改）。\n", identity.Email)
	fmt.Println("该账号的全部既有会话已被撤销。")
}

func firstIdentity(ctx context.Context, repo *iam.Repo) (iam.Identity, error) {
	list, _, err := repo.ListIdentities(ctx, iam.IdentityFilter{Limit: 1})
	if err != nil {
		return iam.Identity{}, err
	}
	if len(list) == 0 {
		return iam.Identity{}, iam.ErrIdentityNotFound
	}
	return list[0], nil
}

func readPassword(fromStdin bool) (string, error) {
	if v := strings.TrimSpace(os.Getenv("ADMIN_INITIAL_PASSWORD")); v != "" {
		return v, nil
	}
	if fromStdin || !term.IsTerminal(int(os.Stdin.Fd())) {
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Fprint(os.Stderr, "请输入初始密码（输入不可见）：")
	raw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "请再次输入以确认：")
	again, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(raw) != string(again) {
		return "", fmt.Errorf("两次输入不一致")
	}
	return string(raw), nil
}
