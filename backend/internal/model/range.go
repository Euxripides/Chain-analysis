package model

import "time"

// DownloadJobRange 计划阶段解析后的范围（“全量”必须解析成明确值，便于审计与恢复）。
type DownloadJobRange struct {
	RangeType     string
	FromBlock     *int64
	ToBlock       *int64
	FromTimestamp *time.Time
	ToTimestamp   *time.Time
	HeadBlock     int64
}
