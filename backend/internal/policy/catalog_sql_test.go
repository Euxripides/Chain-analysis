package policy

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 权限目录必须与 migrations/postgres/0041_rbac.sql 的种子数据完全一致。
// 任何只改一侧的提交都会在这里失败（§28.5 验收：管理 endpoint 权限声明覆盖率 100%）。
func TestCatalogMatchesMigrationSeed(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations", "postgres")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no postgres migrations found")
	}
	rowRe := regexp.MustCompile(`\('([a-z0-9:_\-]+)',\s*'([a-z0-9._\-]+)',\s*'([^']*)',\s*'(low|medium|high|critical)',\s*(true|false)\)`)
	seen := map[string]bool{}
	for _, sqlPath := range files {
		raw, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("read migration %s: %v", sqlPath, err)
		}
		body := string(raw)
		for offset := 0; ; {
			rel := strings.Index(body[offset:], "INSERT INTO permissions (key, category, description, risk_level, sensitive) VALUES")
			if rel < 0 {
				break
			}
			start := offset + rel
			end := strings.Index(body[start:], "ON CONFLICT")
			if end < 0 {
				t.Fatalf("%s: permission seed block without ON CONFLICT", sqlPath)
			}
			block := body[start : start+end]
			for _, m := range rowRe.FindAllStringSubmatch(block, -1) {
				key, category, desc, risk, sensitive := m[1], m[2], m[3], m[4], m[5]
				seen[key] = true
				def, ok := Catalog[key]
				if !ok {
					t.Errorf("SQL 中存在但 Go catalog 缺失的权限：%s", key)
					continue
				}
				if def.Category != category {
					t.Errorf("%s category: sql=%q go=%q", key, category, def.Category)
				}
				if def.Description != desc {
					t.Errorf("%s description: sql=%q go=%q", key, desc, def.Description)
				}
				if def.RiskLevel != risk {
					t.Errorf("%s risk_level: sql=%q go=%q", key, risk, def.RiskLevel)
				}
				if def.Sensitive != (sensitive == "true") {
					t.Errorf("%s sensitive: sql=%q go=%v", key, sensitive, def.Sensitive)
				}
			}
			offset = start + end
		}
	}
	if len(seen) < 100 {
		t.Fatalf("SQL 种子解析出的权限过少（%d），解析正则可能已失效", len(seen))
	}

	var missing []string
	for key := range Catalog {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Go catalog 中存在但 SQL 种子缺失的权限（会导致授权时数据库无对应 FK）：%v", missing)
	}
}

// 角色种子引用的每个权限键都必须在 permissions 表中存在，否则迁移会因外键失败。
func TestRoleSeedPermissionsExist(t *testing.T) {
	dir := filepath.Join("..", "..", "migrations", "postgres")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	refRe := regexp.MustCompile(`'([a-z0-9:_\-]+)'`)
	unnestRe := regexp.MustCompile(`(?s)unnest\(ARRAY\[(.*?)\]\)`)
	for _, sqlPath := range files {
		raw, err := os.ReadFile(sqlPath)
		if err != nil {
			t.Fatalf("read migration %s: %v", sqlPath, err)
		}
		for _, m := range unnestRe.FindAllStringSubmatch(string(raw), -1) {
			for _, k := range refRe.FindAllStringSubmatch(m[1], -1) {
				if _, ok := Catalog[k[1]]; !ok {
					t.Errorf("%s 角色种子引用了未登记的权限键：%s", filepath.Base(sqlPath), k[1])
				}
			}
		}
	}
}
