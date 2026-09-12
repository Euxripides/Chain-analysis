package iam

import "testing"

// 回归：限流必须是"任一维度超限即阻断"。
// 早期实现写成 `!allow(account) && !allow(ip)`，两个桶互相抵消：
// 单 IP 撒网在任一维度耗尽前会一直被放行（§27 SEC-004）。
func TestLoginLimiterBlocksWhenEitherDimensionExhausted(t *testing.T) {
	l := newLoginLimiter(5) // 账号 5/分钟；IP 兜底 100/分钟

	if blocked, _ := l.blocked("1.2.3.4|a@x", "ip|1.2.3.4"); blocked {
		t.Fatal("空桶不应阻断")
	}
	// 只打同一个账号：账号维度耗尽后必须阻断，即使 IP 维度还很宽裕。
	for i := 0; i < 5; i++ {
		l.consume("1.2.3.4|a@x", "ip|1.2.3.4")
	}
	if blocked, retry := l.blocked("1.2.3.4|a@x", "ip|1.2.3.4"); !blocked || retry <= 0 {
		t.Fatalf("账号维度耗尽后必须阻断，retry=%d", retry)
	}
	// 其他账号在同一 IP 下不受影响（同一 NAT 出后的正常用户）。
	if blocked, _ := l.blocked("1.2.3.4|b@x", "ip|1.2.3.4"); blocked {
		t.Fatal("其他账号不应被同一 IP 上的单账号爆破牵连")
	}
}

func TestLoginLimiterIpBackstopBlocksSpraying(t *testing.T) {
	l := newLoginLimiter(5) // IP 兜底容量 max(5*5,100)=100
	for i := 0; i < 100; i++ {
		l.consume("1.2.3.4|victim"+string(rune('a'+i%26))+"@x", "ip|1.2.3.4")
	}
	if blocked, _ := l.blocked("1.2.3.4|fresh@x", "ip|1.2.3.4"); !blocked {
		t.Fatal("单 IP 跨多账号撒网必须被兜底维度阻断")
	}
	// 另一个 IP 不受影响。
	if blocked, _ := l.blocked("5.6.7.8|fresh@x", "ip|5.6.7.8"); blocked {
		t.Fatal("不同来源 IP 不应被牵连")
	}
}

func TestLoginLimiterResetOnSuccess(t *testing.T) {
	l := newLoginLimiter(3)
	for i := 0; i < 3; i++ {
		l.consume("1.1.1.1|u@x", "ip|1.1.1.1")
	}
	if blocked, _ := l.blocked("1.1.1.1|u@x", "ip|1.1.1.1"); !blocked {
		t.Fatal("应已耗尽")
	}
	l.reset("1.1.1.1|u@x")
	if blocked, _ := l.blocked("1.1.1.1|u@x", "ip|1.1.1.1"); blocked {
		t.Fatal("成功登录后应清空账号维度额度")
	}
}
