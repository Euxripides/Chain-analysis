package api

// 数组字段不得序列化成 null（2026-09-16 白屏缺陷的 API 侧回归）。
//
// 现象：身份没有任何角色绑定时 Roles/Permissions 为 nil，响应里是 "roles": null，
// 前端渲染期抛 Cannot read properties of null (reading 'join') 并整页白屏。
// 契约上这两个字段是数组，就不该出现 null。

import (
	"encoding/json"
	"testing"
)

func TestJSONStringsNeverEmitsNull(t *testing.T) {
	var nilSlice []string
	for name, in := range map[string][]string{
		"nil":        nilSlice,
		"empty":      {},
		"one":        {"viewer"},
		"two":        {"viewer", "analyst"},
	} {
		encoded, err := json.Marshal(jsonStrings(in))
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if string(encoded) == "null" {
			t.Fatalf("%s: 必须序列化成 []，实际是 null", name)
		}
		var back []string
		if err := json.Unmarshal(encoded, &back); err != nil {
			t.Fatalf("%s: unmarshal %s: %v", name, encoded, err)
		}
		if len(back) != len(in) {
			t.Fatalf("%s: 往返后长度 %d != %d", name, len(back), len(in))
		}
	}
}
