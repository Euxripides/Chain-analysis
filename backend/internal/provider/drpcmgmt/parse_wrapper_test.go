package drpcmgmt

import "testing"

// 真实 Keys API 的响应形态（2026-09-21 实测，敏感值已替换）：
// 每个元素是「Key 对象 + 用量兄弟字段」的包装。
//
// 这个形态曾让解析器完全失效：外层同时含 key 与 cu_spent_today 两个强信号，
// 被误判成 Key 对象后停止下探，导致 name/status/api_key 全读不到、
// selectTeamKey 按指纹匹配失败（现场表现：后台密钥行只剩指纹，Key 名与当日 CU 全空）。
const realKeysResponse = `[
  {
    "key": {
      "key_id": "84475f64-b514-11f1-8257-125fcd0cdc6e",
      "name": "Key 1",
      "api_key": "AAAAAA111111111111111111111111111111111111111111_OLDKEY",
      "cu_day_limit": 0,
      "rate_limit": 0,
      "status": true,
      "archived": false,
      "methods_whitelist": null,
      "providers": null,
      "updated_at": "2026-09-20T16:58:34Z"
    },
    "cu_spent_today": 111,
    "public_keys": []
  },
  {
    "key": {
      "key_id": "3607bbfc-b5d0-11f1-8370-125fcd0cdc6e",
      "name": "Key 2",
      "api_key": "BBBBBB222222222222222222222222222222222222222222_NEWKEY",
      "cu_day_limit": 5000000,
      "rate_limit": 120000,
      "status": true,
      "updated_at": "2026-09-21T14:00:00Z"
    },
    "cu_spent_today": 222,
    "public_keys": []
  }
]`

func TestParseTeamKeysWrapperShape(t *testing.T) {
	keys, err := parseTeamKeys([]byte(realKeysResponse))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("应解析出 2 个 Key，得到 %d: %+v", len(keys), keys)
	}
	if keys[0].ID != "84475f64-b514-11f1-8257-125fcd0cdc6e" || keys[0].Name != "Key 1" {
		t.Fatalf("外层标量应并入内层对象，得到 %+v", keys[0])
	}
	if keys[0].CUSpentToday == nil || *keys[0].CUSpentToday != 111 {
		t.Fatalf("cu_spent_today 是外层兄弟字段，必须读到：%+v", keys[0].CUSpentToday)
	}
	if keys[0].Enabled == nil || !*keys[0].Enabled {
		t.Fatalf("status=true 必须映射成 enabled：%+v", keys[0].Enabled)
	}
	if keys[1].CUDayLimit == nil || *keys[1].CUDayLimit != 5000000 {
		t.Fatalf("cu_day_limit 未读到：%+v", keys[1].CUDayLimit)
	}
	if keys[1].RateLimit == nil || *keys[1].RateLimit != 120000 {
		t.Fatalf("rate_limit 未读到：%+v", keys[1].RateLimit)
	}

	// 指纹匹配必须选中真正在用的那把 Key。
	want := fingerprint("BBBBBB222222222222222222222222222222222222222222_NEWKEY")
	got, ok := selectTeamKey(keys, want)
	if !ok || got.ID != "3607bbfc-b5d0-11f1-8370-125fcd0cdc6e" {
		t.Fatalf("指纹匹配失败：ok=%v got=%+v", ok, got)
	}
}
