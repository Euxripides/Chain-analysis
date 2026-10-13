package api

import (
	"context"
	"time"

	"github.com/minio/minio-go/v7"
)

/*
 * 预生成产物的清理循环。
 *
 * 预生成是投机产物：草稿改到一半就被覆盖的那些永远不会被导出，产物留在 MinIO 里
 * 只是垃圾。export_prefetch 行有 expires_at（默认 1 小时），这里周期性把过期行连同
 * 对象一起删掉。
 *
 * 为什么必须做：不做的话每次表单变动都会留下一个 60+ MiB 的对象（173 万行 csv+zip
 * 实测 62 MiB），一天下来就是几十 GB。命中提升时对象会转交给正式导出（那条不在这里
 * 删），因此这里的删除只针对"没人认领"的产物。
 */

// prefetchGCInterval 是清理周期。过期时间是 1 小时，因此 10 分钟一轮足够及时；
// 一轮最多删 200 行，避免一次性对对象存储发起大量删除。
const prefetchGCInterval = 10 * time.Minute

// StartPrefetchGC 启动清理循环；ctx 取消后退出。
func (s *Server) StartPrefetchGC(ctx context.Context) {
	if s.deps.Store == nil || s.deps.Raw == nil {
		return
	}
	go func() {
		// 启动后先等一个周期：避免与刚重启时的其它启动工作抢资源。
		timer := time.NewTimer(prefetchGCInterval)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			s.sweepExpiredPrefetch(ctx)
			if err := s.cleanupDatasetExports(ctx); err != nil && s.deps.Log != nil {
				s.deps.Log.Warn("dataset export cleanup failed", "err", err)
			}
			timer.Reset(prefetchGCInterval)
		}
	}()
}

// sweepExpiredPrefetch 删掉一批过期预生成行及其对象。
//
// 失败不致命：下一轮会再试。这里刻意只记日志——清理是维护性工作，不该因为对象存储
// 一次抖动就把 API 打挂。
func (s *Server) sweepExpiredPrefetch(ctx context.Context) {
	rows, err := s.deps.Store.ExpirePrefetches(ctx, 200)
	if err != nil {
		if s.deps.Log != nil {
			s.deps.Log.Warn("prefetch gc: expire query failed", "err", err.Error())
		}
		return
	}
	removed := 0
	for _, r := range rows {
		if r.ObjectRef == "" || r.ObjectBucket == "" {
			continue
		}
		if err := s.deps.Raw.Client().RemoveObject(ctx, r.ObjectBucket, r.ObjectRef, minio.RemoveObjectOptions{}); err != nil {
			if s.deps.Log != nil {
				s.deps.Log.Warn("prefetch gc: object remove failed",
					"object_bucket", r.ObjectBucket, "object_ref", r.ObjectRef, "err", err.Error())
			}
			continue
		}
		removed++
	}
	if len(rows) > 0 && s.deps.Log != nil {
		s.deps.Log.Info("prefetch gc swept expired prefetches", "rows", len(rows), "objects_removed", removed)
	}
}
