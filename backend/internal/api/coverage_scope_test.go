package api

import (
	"strings"
	"testing"
)

// ADR-025：完整性断言按 (subject, dataset) 判定。若把作业级状态重新加回 SQL，
// 同一 full-range 作业里已经全部完成并 commit 覆盖的 dataset 又会被兄弟 dataset 的
// 失败连坐（DL-20260921-0041 815/816、DL-20260916-0011 现场），所以这里把口径钉死。
func TestCoverageAssertionIsDatasetScoped(t *testing.T) {
	sql := actionCoverageAssertionSQL
	if !strings.Contains(sql, "range_type='full'") {
		t.Fatalf("assertion must require a full-range job: %s", sql)
	}
	if !strings.Contains(sql, "dd.status='COMPLETED'") {
		t.Fatalf("assertion must require the dataset itself to be COMPLETED: %s", sql)
	}
	if strings.Contains(sql, "j.status") {
		t.Fatalf("assertion must not gate on the download job status (ADR-025): %s", sql)
	}
}

// 断言 SQL 的占位符顺序必须与调用方一致：$1=chain、$2=addresses、$3=datasets。
func TestCoverageAssertionBindsChainAddressesDatasets(t *testing.T) {
	for _, placeholder := range []string{"j.chain=$1", "ds.subject=ANY($2)", "dd.dataset=ANY($3)"} {
		if !strings.Contains(actionCoverageAssertionSQL, placeholder) {
			t.Fatalf("assertion SQL lost binding %q: %s", placeholder, actionCoverageAssertionSQL)
		}
	}
}
