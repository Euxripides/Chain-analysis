package api

// §7.3 的**静态契约测试**：功能开关必须挂在真实请求路径上，且必须挂对地方。
//
// 为什么用源码断言而不是运行时断言：这里要钉住的是"接线本身"——
// 某条路由有没有被 requireFlag 包住、开关 key 是什么、哪条路由**不得**被包住，
// 都是编译期就能回答的问题，不需要起一个数据库。运行时行为由
// acceptance-evidence/dual-system-v1/tests/run-sprint7-featureflags.sh 用真实请求验证。

import (
	"strings"
	"testing"
)

// readAPISource 复用 sse_publish_bound_test.go 里的同一个 helper（同包，读源码做契约断言）。

// routeLine 返回注册了某条路由的那一行（找不到返回空串）。
func routeLine(src, route string) string {
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "\""+route+"\",") {
			return l
		}
	}
	return ""
}

// TestCapabilityRoutesAreEvaluatedOnRealRequestPaths：能力面（读视图）必须挂开关。
func TestCapabilityRoutesAreEvaluatedOnRealRequestPaths(t *testing.T) {
	cases := []struct {
		file, route, flag string
	}{
		{"userv2.go", "/explorer/addresses/{chain}/{address}/overview", "user.explorer_v2"},
		{"userv2.go", "/explorer/addresses/{chain}/{address}/activity", "user.explorer_v2"},
		{"userv2.go", "/explorer/transactions/{chain}/{hash}", "user.explorer_v2"},
		{"sprint6_governance.go", "/alerts", "admin.alert_center"},
		{"sprint6_governance.go", "/alerts/{id}", "admin.alert_center"},
		{"sprint6_governance.go", "/alerts/{id}/commands/{command}", "admin.alert_center"},
		{"sprint6_control.go", "/control/state", "admin.control_plane_v2"},
		{"sprint6_control.go", "/control/actions", "admin.control_plane_v2"},
	}
	for _, tc := range cases {
		line := routeLine(readAPISource(t, tc.file), tc.route)
		if line == "" {
			t.Errorf("%s: 找不到路由 %s 的注册行", tc.file, tc.route)
			continue
		}
		if !strings.Contains(line, "s.requireFlag(\""+tc.flag+"\"") {
			t.Errorf("%s 的路由 %s 没有挂在功能开关 %s 之下（§7.3 要求真实请求路径求值）：\n%s",
				tc.file, tc.route, tc.flag, strings.TrimSpace(line))
		}
	}
}

// TestSafetyAndRecoveryRoutesAreNotBehindFeatureFlags：安全与恢复路径**不得**挂开关。
//
// 把恢复能力做成可灰度的开关，等于给出一个"悄悄关掉恢复"的入口。
// 这条断言防止后来者"顺手"给 Kill Switch 的执行/审批/恢复也加一个开关。
func TestSafetyAndRecoveryRoutesAreNotBehindFeatureFlags(t *testing.T) {
	src := readAPISource(t, "sprint6_control.go")
	mustStayOpen := []string{
		"\"/control/kill-switch/requests/{id}/approve\"",
		"\"/control/kill-switch/requests/{id}/reject\"",
		"\"/control/kill-switch/requests/{id}/execute\"",
		"\"/control/kill-switch/requests/{id}/restore\"",
		"r.Post(\"/control/kill-switch/requests\"",
	}
	for _, l := range strings.Split(src, "\n") {
		for _, route := range mustStayOpen {
			if !strings.Contains(l, route) {
				continue
			}
			if strings.Contains(l, "requireFlag") {
				t.Errorf("恢复/审批路径不得挂在功能开关下（route=%s）：\n%s", route, strings.TrimSpace(l))
			}
		}
	}
}

// TestRequireFlagFailsClosed：读不到开关必须拒绝，而不是默认放行。
func TestRequireFlagFailsClosed(t *testing.T) {
	src := readAPISource(t, "featureflag_gate.go")
	for _, want := range []string{
		"errFeatureFlagStore",
		"flag_not_found",
		"evaluation_error",
		"ErrorFeatureDisabled",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("featureflag_gate.go 缺少 %s：开关读不到时必须拒绝，而不是默认放行", want)
		}
	}
}

// TestTrialEvaluationSharesTheRealPathAlgorithm：后台试算与真实路径必须共用同一份算法。
func TestTrialEvaluationSharesTheRealPathAlgorithm(t *testing.T) {
	src := readAPISource(t, "sprint6_governance.go")
	if !strings.Contains(src, "featureflag.Evaluate(") {
		t.Errorf("s6EvaluateFlag 必须调用 featureflag.Evaluate，不能保留第二份求值算法")
	}
	if strings.Contains(src, "bucket := int(sum[0])<<8") {
		t.Errorf("sprint6_governance.go 里仍存在独立的分桶实现，算法已分叉")
	}
}
