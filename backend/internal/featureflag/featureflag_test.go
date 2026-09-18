package featureflag

import (
	"context"
	"testing"
	"time"
)

// 这些用例钉住的是"后台试算与真实请求路径共用同一个算法"这件事本身：
// 只要 Evaluate 的语义被改动，两处一起变，不会出现"试算说开、线上说关"。

func base() Flag {
	return Flag{Key: "user.explorer_v2", Environment: "all", Enabled: true, DefaultValue: false,
		Percentage: 100, ScopeType: "global", Revision: 7}
}

func TestEvaluateScopeAndRollout(t *testing.T) {
	cases := []struct {
		name    string
		flag    Flag
		subject string
		org, ws string
		env     string
		want    bool
		wantWhy string
	}{
		{"全量开启", base(), "u1", "o1", "w1", "development", true, "scope_match_full_rollout"},
		{"环境不匹配回落到默认值", Flag{Key: "k", Environment: "production", Enabled: true, Percentage: 100, ScopeType: "global"}, "u1", "o1", "w1", "development", false, "environment_mismatch"},
		{"environment=all 对任何环境生效", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "global"}, "u1", "o1", "w1", "production", true, "scope_match_full_rollout"},
		{"关闭时回落到默认值", Flag{Key: "k", Environment: "all", Enabled: false, DefaultValue: true, Percentage: 100, ScopeType: "global"}, "u1", "o1", "w1", "development", true, "disabled"},
		{"关闭且默认值为假即拒绝", Flag{Key: "k", Environment: "all", Enabled: false, DefaultValue: false, Percentage: 100, ScopeType: "global"}, "u1", "o1", "w1", "development", false, "disabled"},
		{"组织作用域命中", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "organization", ScopeOrgID: "o1"}, "u1", "o1", "w1", "development", true, "scope_match_full_rollout"},
		{"组织作用域不命中", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "organization", ScopeOrgID: "o2"}, "u1", "o1", "w1", "development", false, "scope_mismatch"},
		{"工作区作用域命中", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "workspace", ScopeWorkspaceID: "w1"}, "u1", "o1", "w1", "development", true, "scope_match_full_rollout"},
		{"用户作用域命中（S7-DB-01：必须用 scope_user_id）", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "user", ScopeOrgID: "u1", ScopeUserID: "u1"}, "u1", "o1", "w1", "development", true, "scope_match_full_rollout"},
		{"用户作用域不命中", Flag{Key: "k", Environment: "all", Enabled: true, Percentage: 100, ScopeType: "user", ScopeUserID: "u2"}, "u1", "o1", "w1", "development", false, "scope_mismatch"},
		{"百分比为 0 回落默认值", Flag{Key: "k", Environment: "all", Enabled: true, DefaultValue: false, Percentage: 0, ScopeType: "global"}, "u1", "o1", "w1", "development", false, "scope_match_zero_rollout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.flag, tc.subject, tc.org, tc.ws, tc.env)
			if got.Allowed != tc.want || got.Reason != tc.wantWhy {
				t.Fatalf("Evaluate = (%v,%q), want (%v,%q)", got.Allowed, got.Reason, tc.want, tc.wantWhy)
			}
			if got.Key != tc.flag.Key || got.Revision != tc.flag.Revision {
				t.Fatalf("Decision 未回带 key/revision: %+v", got)
			}
		})
	}
}

// 分桶必须确定性：同一个 (key, subject) 每次得到同一个结果，否则灰度会"忽开忽关"。
func TestEvaluatePercentageIsDeterministic(t *testing.T) {
	f := base()
	f.Percentage = 37
	first := Evaluate(f, "subject-1", "o", "w", "development")
	for i := 0; i < 50; i++ {
		if got := Evaluate(f, "subject-1", "o", "w", "development"); got != first {
			t.Fatalf("第 %d 次求值结果漂移: %+v != %+v", i, got, first)
		}
	}
	if first.Bucket < 0 || first.Bucket > 99 {
		t.Fatalf("bucket 越界: %d", first.Bucket)
	}
}

// 分桶必须**分布在主体之间**：37% 的开关在 1000 个主体上要落在合理区间，
// 否则"灰度"只是在全体上开或关。
func TestEvaluatePercentageSpreadsAcrossSubjects(t *testing.T) {
	f := base()
	f.Percentage = 37
	hit := 0
	for i := 0; i < 1000; i++ {
		if Evaluate(f, "subject-"+string(rune(0x4e00+i)), "o", "w", "development").Allowed {
			hit++
		}
	}
	if hit < 250 || hit > 500 {
		t.Fatalf("37%% 灰度命中 %d/1000，分布明显异常", hit)
	}
}

// Store 在无数据库时不得 panic，并且必须给出"开关不存在"这一可判定的结果。
func TestStoreWithoutPool(t *testing.T) {
	s := NewStore(nil, time.Second, nil)
	if _, ok, err := s.Lookup(context.Background(), "user.explorer_v2"); ok || err != nil {
		t.Fatalf("Lookup(no pool) = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
	var nilStore *Store
	if _, ok, err := nilStore.Lookup(context.Background(), "k"); ok || err != nil {
		t.Fatalf("nil Store 必须安全: (ok=%v, err=%v)", ok, err)
	}
	nilStore.Invalidate()
}
