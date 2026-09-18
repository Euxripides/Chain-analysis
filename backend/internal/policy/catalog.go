// Package policy 实现 §4.2 的授权模型：RBAC + 工作区作用域 + 少量 ABAC 条件。
// 权限目录必须与 migrations/postgres/0041_rbac.sql 保持一致；
// 新增权限时两侧同时修改，并由 policy.Catalog 与数据库做一致性校验（CI）。
package policy

import "strings"

// RiskLevel 与 0041_rbac.sql 的 permissions.risk_level 对齐。
const (
	RiskLow      = "low"
	RiskMedium   = "medium"
	RiskHigh     = "high"
	RiskCritical = "critical"
)

type Permission struct {
	Key         string
	Category    string
	Description string
	RiskLevel   string
	Sensitive   bool
}

// Catalog 是代码侧的权限目录。它同时用于路由声明校验和单元测试。
var Catalog = map[string]Permission{
	// 案件
	"case:create":         {"case:create", "case", "创建案件", RiskLow, false},
	"case:read":           {"case:read", "case", "查看案件", RiskLow, false},
	"case:update":         {"case:update", "case", "修改案件", RiskMedium, false},
	"case:archive":        {"case:archive", "case", "归档/恢复案件", RiskMedium, false},
	"case:delete":         {"case:delete", "case", "删除案件", RiskHigh, false},
	"case:member:manage":  {"case:member:manage", "case", "管理案件成员", RiskHigh, false},
	"case:activity:read":  {"case:activity:read", "case", "查看案件动态", RiskLow, false},
	"case:comment:create": {"case:comment:create", "case", "发表案件评论", RiskLow, false},
	"case:subject:manage": {"case:subject:manage", "case", "管理案件对象", RiskMedium, false},
	// 调查
	"investigation:create":        {"investigation:create", "investigation", "创建调查任务", RiskLow, false},
	"investigation:read":          {"investigation:read", "investigation", "查看调查任务", RiskLow, false},
	"investigation:update":        {"investigation:update", "investigation", "修改调查参数", RiskMedium, false},
	"investigation:run":           {"investigation:run", "investigation", "启动/暂停/继续调查", RiskMedium, false},
	"investigation:start":         {"investigation:start", "investigation", "启动调查", RiskMedium, false},
	"investigation:pause":         {"investigation:pause", "investigation", "暂停调查", RiskMedium, false},
	"investigation:resume":        {"investigation:resume", "investigation", "恢复调查", RiskMedium, false},
	"investigation:retry":         {"investigation:retry", "investigation", "重试调查", RiskMedium, false},
	"investigation:submit_review": {"investigation:submit_review", "investigation", "提交调查复核", RiskMedium, false},
	"investigation:cancel":        {"investigation:cancel", "investigation", "取消调查", RiskMedium, false},
	"investigation:approve":       {"investigation:approve", "investigation", "复核调查结论", RiskHigh, false},
	"investigation:delete":        {"investigation:delete", "investigation", "删除调查", RiskHigh, false},
	// 结论与证据
	"finding:create":           {"finding:create", "finding", "新增 Finding", RiskLow, false},
	"finding:read":             {"finding:read", "finding", "查看 Finding", RiskLow, false},
	"finding:update":           {"finding:update", "finding", "修改 Finding", RiskMedium, false},
	"finding:review":           {"finding:review", "finding", "复核 Finding", RiskHigh, false},
	"evidence:list":            {"evidence:list", "evidence", "列出证据", RiskLow, false},
	"evidence:read":            {"evidence:read", "evidence", "查看证据元数据", RiskLow, false},
	"evidence:create":          {"evidence:create", "evidence", "添加证据", RiskLow, false},
	"evidence:download":        {"evidence:download", "evidence", "下载证据文件", RiskMedium, true},
	"evidence:delete":          {"evidence:delete", "evidence", "移除证据", RiskHigh, false},
	"evidence:sensitive:read":  {"evidence:sensitive:read", "evidence", "读取敏感证据正文", RiskCritical, true},
	"evidence:snapshot:create": {"evidence:snapshot:create", "evidence", "固化证据快照", RiskMedium, false},
	// 图谱
	"canvas:create":          {"canvas:create", "canvas", "创建图谱画布", RiskLow, false},
	"canvas:read":            {"canvas:read", "canvas", "查看图谱", RiskLow, false},
	"canvas:update":          {"canvas:update", "canvas", "编辑图谱", RiskMedium, false},
	"canvas:delete":          {"canvas:delete", "canvas", "删除图谱", RiskHigh, false},
	"canvas:snapshot:create": {"canvas:snapshot:create", "canvas", "创建图谱快照", RiskMedium, false},
	// 数据查询
	"explorer:read":    {"explorer:read", "explorer", "查询本地数据", RiskLow, false},
	"explorer:fetch":   {"explorer:fetch", "explorer", "触发链上取数", RiskMedium, false},
	"address:read":     {"address:read", "explorer", "查看地址详情", RiskLow, false},
	"transaction:read": {"transaction:read", "explorer", "查看交易详情", RiskLow, false},
	"token:read":       {"token:read", "explorer", "查看 Token 元数据", RiskLow, false},
	"search:global":    {"search:global", "explorer", "使用全局搜索", RiskLow, false},
	// 下载与任务
	"download:preview":   {"download:preview", "download", "预览 Coverage 与影响", RiskLow, false},
	"download:create":    {"download:create", "download", "创建下载任务", RiskMedium, false},
	"download:refresh":   {"download:refresh", "download", "显式刷新并新增 Provider 取数", RiskHigh, false},
	"download:read":      {"download:read", "download", "查看下载任务", RiskLow, false},
	"download:reuse":     {"download:reuse", "download", "复用已有 Coverage 数据", RiskLow, false},
	"download:control":   {"download:control", "download", "暂停/继续/取消下载", RiskMedium, false},
	"download:delete":    {"download:delete", "download", "删除下载任务数据", RiskHigh, false},
	"job:read":           {"job:read", "job", "查看任务", RiskLow, false},
	"job:control":        {"job:control", "job", "控制任务", RiskMedium, false},
	"job:retry":          {"job:retry", "job", "重试任务", RiskMedium, false},
	"job:list:workspace": {"job:list:workspace", "job", "列出工作区内任务", RiskLow, false},
	"job:diagnose":       {"job:diagnose", "job", "查看任务诊断信息", RiskMedium, false},
	"job:admin":          {"job:admin", "job", "全局任务控制", RiskHigh, false},
	// 导出与报告
	"export:create":   {"export:create", "export", "创建导出", RiskMedium, false},
	"export:read":     {"export:read", "export", "查看导出", RiskLow, false},
	"export:download": {"export:download", "export", "下载导出文件", RiskMedium, true},
	"export:delete":   {"export:delete", "export", "删除导出", RiskHigh, false},
	"report:create":   {"report:create", "report", "生成报告", RiskLow, false},
	"report:review":   {"report:review", "report", "复核报告", RiskHigh, false},
	"report:read":     {"report:read", "report", "查看报告", RiskLow, false},
	"report:download": {"report:download", "report", "下载报告", RiskMedium, true},
	"report:approve":  {"report:approve", "report", "审批报告", RiskHigh, false},
	"report:sign":     {"report:sign", "report", "签署报告", RiskCritical, false},
	"report:delete":   {"report:delete", "report", "删除报告", RiskHigh, false},
	// 协作与个人
	"notification:read":   {"notification:read", "notification", "查看通知", RiskLow, false},
	"notification:manage": {"notification:manage", "notification", "管理通知偏好", RiskLow, false},
	"inbox:read":          {"inbox:read", "notification", "查看待我处理", RiskLow, false},
	"inbox:assign":        {"inbox:assign", "notification", "分派待办", RiskMedium, false},
	"profile:read":        {"profile:read", "self", "查看个人资料", RiskLow, false},
	"profile:update":      {"profile:update", "self", "修改个人资料", RiskLow, false},
	"preference:update":   {"preference:update", "self", "修改使用偏好", RiskLow, false},
	"security:update":     {"security:update", "self", "修改安全设置", RiskHigh, false},
	"session:self:revoke": {"session:self:revoke", "self", "撤销自己的会话", RiskMedium, false},
	// 后台
	"admin:dashboard:read":              {"admin:dashboard:read", "admin.ops", "查看运营总览", RiskLow, false},
	"admin:org:read":                    {"admin:org:read", "admin.identity", "查看组织", RiskLow, false},
	"admin:org:manage":                  {"admin:org:manage", "admin.identity", "管理组织", RiskHigh, false},
	"admin:workspace:read":              {"admin:workspace:read", "admin.identity", "查看工作区", RiskLow, false},
	"admin:workspace:manage":            {"admin:workspace:manage", "admin.identity", "管理工作区", RiskHigh, false},
	"admin:user:read":                   {"admin:user:read", "admin.identity", "查看用户", RiskMedium, false},
	"admin:user:create":                 {"admin:user:create", "admin.identity", "创建用户", RiskHigh, false},
	"admin:user:update":                 {"admin:user:update", "admin.identity", "修改用户", RiskHigh, false},
	"admin:user:disable":                {"admin:user:disable", "admin.identity", "停用用户", RiskCritical, false},
	"admin:user:invite":                 {"admin:user:invite", "admin.identity", "邀请用户", RiskMedium, false},
	"admin:user:password:reset":         {"admin:user:password:reset", "admin.identity", "重置用户密码", RiskCritical, false},
	"admin:user:reset_mfa":              {"admin:user:reset_mfa", "admin.identity", "重置用户 MFA", RiskCritical, false},
	"admin:user:delete":                 {"admin:user:delete", "admin.identity", "删除（离职）用户：软删 + 匿名化 + 归档其个人空间，保留审计引用", RiskCritical, true},
	"admin:role:read":                   {"admin:role:read", "admin.identity", "查看角色", RiskLow, false},
	"admin:role:manage":                 {"admin:role:manage", "admin.identity", "管理角色与权限", RiskCritical, false},
	"admin:permission:read":             {"admin:permission:read", "admin.identity", "查看权限目录", RiskLow, false},
	"admin:member:read":                 {"admin:member:read", "admin.identity", "查看成员关系", RiskMedium, false},
	"admin:member:manage":               {"admin:member:manage", "admin.identity", "管理成员关系", RiskHigh, false},
	"admin:session:read":                {"admin:session:read", "admin.security", "查看会话", RiskMedium, false},
	"admin:session:revoke":              {"admin:session:revoke", "admin.security", "强制下线会话", RiskCritical, false},
	"admin:login-event:read":            {"admin:login-event:read", "admin.security", "查看登录记录", RiskMedium, true},
	"admin:risk:read":                   {"admin:risk:read", "admin.security", "查看风险事件", RiskMedium, false},
	"admin:risk:manage":                 {"admin:risk:manage", "admin.security", "处置风险事件", RiskHigh, false},
	"admin:audit:read":                  {"admin:audit:read", "admin.security", "查看审计日志", RiskMedium, true},
	"admin:audit:export":                {"admin:audit:export", "admin.security", "导出审计日志", RiskCritical, true},
	"admin:audit:verify":                {"admin:audit:verify", "admin.security", "校验审计哈希链", RiskMedium, false},
	"admin:audit:seal":                  {"admin:audit:seal", "admin.security", "封存审计链完整性缺口边界", RiskCritical, false},
	"admin:apikey:read":                 {"admin:apikey:read", "admin.security", "查看 API/MCP 密钥", RiskHigh, true},
	"admin:apikey:create":               {"admin:apikey:create", "admin.security", "创建 API/MCP 密钥", RiskCritical, false},
	"admin:apikey:revoke":               {"admin:apikey:revoke", "admin.security", "吊销 API/MCP 密钥", RiskCritical, false},
	"admin:killswitch:read":             {"admin:killswitch:read", "admin.control", "查看 Kill Switch", RiskMedium, false},
	"admin:killswitch:engage":           {"admin:killswitch:engage", "admin.control", "启用 Kill Switch", RiskCritical, false},
	"admin:killswitch:release":          {"admin:killswitch:release", "admin.control", "解除 Kill Switch", RiskCritical, false},
	"admin:approval:read":               {"admin:approval:read", "admin.control", "查看审批请求", RiskMedium, false},
	"admin:approval:decide":             {"admin:approval:decide", "admin.control", "审批高风险操作", RiskCritical, false},
	"admin:alert:read":                  {"admin:alert:read", "admin.control", "查看告警", RiskLow, false},
	"admin:alert:manage":                {"admin:alert:manage", "admin.control", "处置告警", RiskHigh, false},
	"admin:provider:read":               {"admin:provider:read", "admin.provider", "查看 Provider", RiskLow, false},
	"admin:provider:update":             {"admin:provider:update", "admin.provider", "修改 Provider 配置", RiskHigh, false},
	"admin:provider:key:rotate":         {"admin:provider:key:rotate", "admin.provider", "轮换 Provider 密钥", RiskCritical, false},
	"admin:routing:read":                {"admin:routing:read", "admin.provider", "查看路由与限流", RiskLow, false},
	"admin:routing:update":              {"admin:routing:update", "admin.provider", "修改路由与限流", RiskHigh, false},
	"admin:performance:read":            {"admin:performance:read", "admin.provider", "查看性能模式", RiskLow, false},
	"admin:performance:update":          {"admin:performance:update", "admin.provider", "修改性能模式", RiskHigh, false},
	"admin:cost:read":                   {"admin:cost:read", "admin.provider", "查看成本", RiskMedium, false},
	"admin:budget:read":                 {"admin:budget:read", "admin.provider", "查看预算", RiskMedium, false},
	"admin:budget:update":               {"admin:budget:update", "admin.provider", "修改预算", RiskCritical, false},
	"admin:request:read":                {"admin:request:read", "admin.provider", "查看请求审计", RiskMedium, true},
	"admin:job:list":                    {"admin:job:list", "admin.data", "列出全局任务", RiskLow, false},
	"admin:job:read":                    {"admin:job:read", "admin.data", "查看全局任务", RiskLow, false},
	"admin:job:control":                 {"admin:job:control", "admin.data", "控制全局任务", RiskHigh, false},
	"admin:job:cancel":                  {"admin:job:cancel", "admin.data", "取消任意任务", RiskCritical, false},
	"admin:job:retry":                   {"admin:job:retry", "admin.data", "重试任意任务", RiskHigh, false},
	"admin:coverage:read":               {"admin:coverage:read", "admin.data", "查看 Coverage 与数据质量", RiskLow, false},
	"admin:dataset:read":                {"admin:dataset:read", "admin.data", "查看 Dataset", RiskLow, false},
	"admin:dataset:manage":              {"admin:dataset:manage", "admin.data", "重建 Dataset", RiskCritical, false},
	"admin:export:list":                 {"admin:export:list", "admin.data", "列出全局导出", RiskMedium, true},
	"admin:export:read":                 {"admin:export:read", "admin.data", "查看全部导出", RiskMedium, true},
	"admin:export:delete":               {"admin:export:delete", "admin.data", "删除导出对象", RiskCritical, false},
	"admin:storage:read":                {"admin:storage:read", "admin.data", "查看对象存储用量", RiskLow, false},
	"admin:storage:manage":              {"admin:storage:manage", "admin.data", "管理对象存储", RiskHigh, false},
	"admin:retention:read":              {"admin:retention:read", "admin.data", "查看保留策略", RiskLow, false},
	"admin:retention:manage":            {"admin:retention:manage", "admin.data", "管理保留策略", RiskCritical, false},
	"admin:deletion:read":               {"admin:deletion:read", "admin.data", "查看删除请求", RiskMedium, false},
	"admin:deletion:request":            {"admin:deletion:request", "admin.data", "发起删除请求", RiskCritical, false},
	"admin:deletion:approve":            {"admin:deletion:approve", "admin.data", "审批删除请求", RiskCritical, false},
	"admin:config:read":                 {"admin:config:read", "admin.system", "查看系统配置", RiskMedium, false},
	"admin:config:update":               {"admin:config:update", "admin.system", "修改系统配置", RiskCritical, false},
	"admin:config:history:read":         {"admin:config:history:read", "admin.system", "查看配置历史", RiskMedium, false},
	"admin:flag:read":                   {"admin:flag:read", "admin.system", "查看功能开关", RiskLow, false},
	"admin:flag:update":                 {"admin:flag:update", "admin.system", "修改功能开关", RiskHigh, false},
	"admin:backup:read":                 {"admin:backup:read", "admin.system", "查看备份", RiskMedium, false},
	"admin:backup:create":               {"admin:backup:create", "admin.system", "创建备份", RiskHigh, false},
	"admin:backup:restore":              {"admin:backup:restore", "admin.system", "恢复备份", RiskCritical, false},
	"admin:version:read":                {"admin:version:read", "admin.system", "查看版本与迁移", RiskLow, false},
	"admin:version:manage":              {"admin:version:manage", "admin.system", "执行版本与迁移操作", RiskCritical, false},
	"admin:notification-channel:read":   {"admin:notification-channel:read", "admin.system", "查看通知渠道", RiskLow, false},
	"admin:notification-channel:manage": {"admin:notification-channel:manage", "admin.system", "管理通知渠道", RiskHigh, false},
	"admin:system:read":                 {"admin:system:read", "admin.system", "查看系统健康", RiskLow, false},
	"dashboard:read":                    {"dashboard:read", "dashboard", "查看个人工作台", RiskLow, false},
	"search:read":                       {"search:read", "explorer", "执行全局搜索", RiskLow, false},
	"case:list":                         {"case:list", "case", "列出案件", RiskLow, false},
	"case:state:update":                 {"case:state:update", "case", "案件状态命令", RiskMedium, false},
	"case:member:list":                  {"case:member:list", "case", "查看案件成员", RiskLow, false},
	"subject:list":                      {"subject:list", "case", "查看案件对象", RiskLow, false},
	"subject:create":                    {"subject:create", "case", "新增案件对象", RiskMedium, false},
	"subject:update":                    {"subject:update", "case", "修改案件对象", RiskMedium, false},
	"subject:remove":                    {"subject:remove", "case", "移除案件对象", RiskHigh, false},
	"comment:list":                      {"comment:list", "case", "查看案件评论", RiskLow, false},
	"comment:create":                    {"comment:create", "case", "发表案件评论", RiskLow, false},
	"investigation:list":                {"investigation:list", "investigation", "列出调查任务", RiskLow, false},
	"finding:list":                      {"finding:list", "finding", "列出 Finding", RiskLow, false},
	"evidence:review":                   {"evidence:review", "evidence", "复核证据状态", RiskHigh, false},
	"canvas:list":                       {"canvas:list", "canvas", "列出图谱画布", RiskLow, false},
	"canvas:branch":                     {"canvas:branch", "canvas", "创建图谱分支", RiskMedium, false},
	"canvas:snapshot":                   {"canvas:snapshot", "canvas", "创建/查看图谱快照", RiskMedium, false},
	"report:list":                       {"report:list", "report", "列出报告", RiskLow, false},
	"job:list:self":                     {"job:list:self", "job", "列出本人任务", RiskLow, false},
	"export:list":                       {"export:list", "export", "列出导出", RiskLow, false},
	"notification:list":                 {"notification:list", "notification", "列出通知", RiskLow, false},
	"notification:update":               {"notification:update", "notification", "更新通知状态", RiskLow, false},
	"profile:update:self":               {"profile:update:self", "self", "修改本人资料", RiskLow, false},
	"admin:metrics:read":                {"admin:metrics:read", "admin.system", "查看平台指标", RiskLow, false},
}

// SystemRoles 是 §4.1 定义的九个系统角色。
var SystemRoles = []string{
	"owner", "platform_admin", "security_admin", "auditor",
	"investigator", "analyst", "reviewer", "viewer", "api_client",
}

// Lookup 返回权限定义。
func Lookup(key string) (Permission, bool) {
	p, ok := Catalog[key]
	return p, ok
}

// IsAdminPermission 判断是否属于后台管理命名空间。
func IsAdminPermission(key string) bool {
	return strings.HasPrefix(key, "admin:")
}

// Sensitive 判断权限是否属于敏感读取（需要额外审计）。
func Sensitive(key string) bool {
	if p, ok := Catalog[key]; ok {
		return p.Sensitive
	}
	return false
}

// RiskOf 返回权限风险等级，未登记权限按 critical 处理（fail closed）。
func RiskOf(key string) string {
	if p, ok := Catalog[key]; ok {
		return p.RiskLevel
	}
	return RiskCritical
}

// Keys 返回稳定的权限键列表（用于后台权限目录页与测试）。
func Keys() []string {
	out := make([]string, 0, len(Catalog))
	for k := range Catalog {
		out = append(out, k)
	}
	return out
}
