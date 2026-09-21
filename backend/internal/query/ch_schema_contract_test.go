package query

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// migrationTables 把走 SELECT * 的 ClickHouse 表映射到接收行的结构体。
//
// clickhouse-go 的 ScanStruct 是严格匹配：结果里任何一列在结构体上没有对应的
// ch tag，整条查询都会以 missing destination name 失败。历史上 0008 与 0010
// 两次迁移都因此打断了线上读路径（活动读 500、逐笔明细 500、CSV 导出失败）。
// 这个测试让"加列"和"补结构体"必须在同一个提交里发生。
var migrationTables = map[string]any{
	"transactions":     txRow{},
	"token_transfers":  tokenTransferRow{},
	"nft_transfers":    nftTransferRow{},
	"logs":             logRow{},
	"address_balances": balanceRow{},
	"address_activity": activityRow{},
	"internal_calls":   internalTransferRow{},
}

var (
	createTableRe = regexp.MustCompile("(?is)CREATE[[:space:]]+TABLE[[:space:]]+(?:IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+)?blockchain\\.([a-z_]+)[[:space:]]*\\(")
	// 一条 ALTER TABLE 可以带多个逗号分隔的 ADD COLUMN，因此先切出语句再逐列匹配。
	alterTableRe = regexp.MustCompile("(?is)ALTER[[:space:]]+TABLE[[:space:]]+blockchain\\.([a-z_]+)(.*?)(?:;|$)")
	addColumnRe  = regexp.MustCompile("(?is)ADD[[:space:]]+COLUMN[[:space:]]+(?:IF[[:space:]]+NOT[[:space:]]+EXISTS[[:space:]]+)?([a-z_][a-z_0-9]*)")
)

// columnListKeywords 是列清单里可能出现、但不是列名的行首关键字。
var columnListKeywords = map[string]bool{
	"PRIMARY": true, "INDEX": true, "CONSTRAINT": true, "PROJECTION": true,
	"SAMPLE": true, "TTL": true, "ORDER": true, "PARTITION": true,
	"SETTINGS": true, "ENGINE": true, "KEY": true,
}

func TestRowStructsCoverEveryMigratedColumn(t *testing.T) {
	columns := migratedColumns(t)
	tables := make([]string, 0, len(migrationTables))
	for table := range migrationTables {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		want := columns[table]
		if len(want) == 0 {
			t.Fatalf("blockchain.%s: migrations contain no CREATE TABLE for this table", table)
		}
		structType := reflect.TypeOf(migrationTables[table])
		got := chTags(structType)
		var missing, extra []string
		for name := range want {
			if !got[name] {
				missing = append(missing, name)
			}
		}
		for name := range got {
			if !want[name] {
				extra = append(extra, name)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("blockchain.%s: %s cannot receive columns %v; a SELECT * scan fails with missing destination name", table, structType, missing)
		}
		if len(extra) > 0 {
			t.Errorf("blockchain.%s: %s declares unknown columns %v", table, structType, extra)
		}
	}
}

// migratedColumns 解析 backend/migrations/clickhouse/*.sql（按文件名顺序，迁移
// 顺序即执行顺序），得到每张表的最终列集合。
func migratedColumns(t *testing.T) map[string]map[string]bool {
	t.Helper()
	dir := filepath.Join("..", "..", "migrations", "clickhouse")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	columns := map[string]map[string]bool{}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		sql := stripSQLComments(string(raw))
		for _, match := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
			table := sql[match[2]:match[3]]
			body, err := createTableBody(sql, match[1]-1)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			set := columns[table]
			if set == nil {
				set = map[string]bool{}
				columns[table] = set
			}
			for _, column := range parseColumnList(body) {
				set[column] = true
			}
		}
		for _, alter := range alterTableRe.FindAllStringSubmatch(sql, -1) {
			table, body := alter[1], alter[2]
			set := columns[table]
			if set == nil {
				set = map[string]bool{}
				columns[table] = set
			}
			for _, column := range addColumnRe.FindAllStringSubmatch(body, -1) {
				set[column[1]] = true
			}
		}
	}
	return columns
}

// createTableBody 返回 CREATE TABLE 语句括号内（含嵌套类型括号）的列清单文本。
// open 指向列清单左括号（正则匹配结束于该字符）。
func createTableBody(sql string, open int) (string, error) {
	depth := 0
	for i := open; i < len(sql); i++ {
		switch sql[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return sql[open+1 : i], nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced parentheses after column list")
}

func parseColumnList(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), ","))
		if line == "" {
			continue
		}
		name := strings.Fields(line)[0]
		if columnListKeywords[strings.ToUpper(name)] {
			continue
		}
		out = append(out, name)
	}
	return out
}

// stripSQLComments 去掉 -- 之后的注释；迁移文件不在字符串字面量里使用 --，
// 因此按行截断是安全的。
func stripSQLComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func chTags(structType reflect.Type) map[string]bool {
	out := make(map[string]bool, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		tag := structType.Field(i).Tag.Get("ch")
		if tag == "" {
			continue
		}
		out[strings.Split(tag, ",")[0]] = true
	}
	return out
}
