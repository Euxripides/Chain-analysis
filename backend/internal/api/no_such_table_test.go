package api

// 回归守卫：禁止再写「查询不存在的表」这类缺陷。
//
// 2026-09-16 在本包内连续定位到 5 处同类缺陷（dataset_jobs、shards、raw_objects、
// download_jobs.downloaded_rows、dataset_coverage.from_timestamp）。它们的共同表现是：
// 语句失败 -> 请求事务进入 aborted 状态 -> 提交门（scope commit gate）把整个请求
// 改写成 409 AUTHZ_WORKSPACE_SCOPE_STALE —— 真实原因在响应里完全看不到，
// 于是「后端写错了表名」在外观上变成了「作用域校验不通过」。
//
// 本测试把已知不存在的表名固化下来，扫到即失败；新增表名前请先核对
// information_schema（backend/migrations/postgres/*.sql 是权威来源）。

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestNoQueriesAgainstNonexistentTables(t *testing.T) {
	banned := map[string]string{
		"dataset_jobs": "不存在：逐地址进度在 download_subjects，下载任务在 download_jobs",
		"shards":       "不存在：分片表是 download_shards",
		"raw_objects":  "不存在：原始对象清单在 download_shards.raw_object",
	}
	pattern := regexp.MustCompile("(?i)\\b(?:from|join|into|update)\\s+([a-z_][a-z0-9_]*)")
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	problems := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			// 去掉行注释，避免注释里的说明文字被当成 SQL。
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			for _, m := range pattern.FindAllStringSubmatch(line, -1) {
				table := strings.ToLower(m[1])
				if why, bad := banned[table]; bad {
					problems = append(problems, name+":"+strconv.Itoa(i+1)+": "+table+" -> "+why)
				}
			}
		}
	}
	if len(problems) > 0 {
		t.Fatalf("发现指向不存在表的 SQL：\n%s", strings.Join(problems, "\n"))
	}
}
