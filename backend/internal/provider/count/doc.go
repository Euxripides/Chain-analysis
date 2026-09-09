// Package count 定义 Count Oracle Provider Port（V1.3 §2-§45）。
//
// 本包是 Count Plane 与外部 Count Provider 之间的最小稳定边界：
//   - 规范类型（CountRequest / CountResult / CountResolution）
//   - CountProvider 最小接口（ID / Capabilities / Count / Health）
//   - 可选能力接口（Batch / MultiDataset / Async，P1 预留）
//   - ProviderCapabilities / DatasetCapability / CapabilityStatus
//   - RateScope / RateBucketPolicy（并行能力的限流抽象，禁止把 Chainbase 1 RPS 写死进核心）
//   - ProviderRegistry
//
// 约束（V1.3 §103）：Planner 不 import 本包；本包不 import 任何 Provider 实现；
// 新 Provider 只需要实现本包接口并在 cmd/worker 注册即可接入，Download Planner 0 行修改。
package count
