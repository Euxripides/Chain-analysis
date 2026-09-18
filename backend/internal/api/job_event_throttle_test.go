package api

import (
	"testing"
	"time"
)

// S7-WAL-01：进度事件落盘节流器的行为契约。
//
// 这三条性质是“实时推送不变、落盘降到 ≤1/10 秒”的前提，任何一条被破坏都会
// 要么退回原来的 WAL 浪费，要么让状态/终态事件被误节流。

func TestProgressThrottleFirstEventPersistsThenSuppressesWithinWindow(t *testing.T) {
	var th jobEventThrottle
	now := time.Now()
	if !th.shouldPersistProgress("job-1", now) {
		t.Fatal("任务的第一条进度事件必须落盘")
	}
	th.markProgressPersisted("job-1", now)
	if th.shouldPersistProgress("job-1", now.Add(progressEventPersistInterval-time.Millisecond)) {
		t.Fatal("窗口内的进度事件必须被节流（不落盘）")
	}
	if !th.shouldPersistProgress("job-1", now.Add(progressEventPersistInterval)) {
		t.Fatal("到达 10 秒边界时必须恢复落盘")
	}
}

// shouldPersistProgress 必须是**只读判断**：落盘失败时没有被标记，
// 下一条进度事件要能立刻重试，而不是白白丢掉一整个窗口的进度历史。
func TestProgressThrottleCheckIsReadOnlySoFailuresRetryImmediately(t *testing.T) {
	var th jobEventThrottle
	now := time.Now()
	if !th.shouldPersistProgress("job-1", now) {
		t.Fatal("第一次判断应当放行")
	}
	// 模拟 INSERT 失败：不调用 markProgressPersisted。
	if !th.shouldPersistProgress("job-1", now) {
		t.Fatal("未成功落盘的判断不能消耗窗口，否则失败后会静默丢失 10 秒进度")
	}
}

func TestProgressThrottleIsPerJob(t *testing.T) {
	var th jobEventThrottle
	now := time.Now()
	th.markProgressPersisted("job-a", now)
	if !th.shouldPersistProgress("job-b", now) {
		t.Fatal("节流必须按 job 隔离，不能因为 job-a 刚写过就压掉 job-b")
	}
	if th.shouldPersistProgress("job-a", now) {
		t.Fatal("job-a 在自己的窗口内仍应被节流")
	}
}

// 节流只应作用于进度类事件：非进度事件在 emitJobEvent 里直接走持久化分支，
// 这里固化 event_type 常量，避免有人改动它而让节流静默扩大或失效。
func TestProgressEventTypeConstant(t *testing.T) {
	// events.progress.<jobID> 经 jobEventRoute 解析出的 event_type 就是 "progress"。
	if got := progressEventType; got != "progress" {
		t.Fatalf("progressEventType = %q, want %q", got, "progress")
	}
	if progressEventPersistInterval != 10*time.Second {
		t.Fatalf("progressEventPersistInterval = %v, want 10s", progressEventPersistInterval)
	}
}

// 惰性清理：长期运行后 map 不能无界增长（只保留最近 10 分钟内出现过的 job）。
func TestProgressThrottlePrunesStaleJobs(t *testing.T) {
	var th jobEventThrottle
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 300; i++ {
		th.markProgressPersisted(string(rune('a'+i%26))+string(rune('0'+i/26)), base)
	}
	if len(th.last) <= 256 {
		t.Fatalf("map 应当已经超过清理阈值，实际 %d", len(th.last))
	}
	// 触发一次清理（新时间戳 + 超过阈值）。
	th.markProgressPersisted("live-job", time.Now())
	if len(th.last) > 300 {
		t.Fatalf("清理未生效，map 仍有 %d 项", len(th.last))
	}
	if _, ok := th.last["live-job"]; !ok {
		t.Fatal("清理不能删掉刚刚写入的活跃 job")
	}
}
