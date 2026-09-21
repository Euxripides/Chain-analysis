package drpcmgmt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// 本文件是管理接口的宽松解析层。
//
// 官方文档（开发文档 §3.3）只说明 Statistics API 返回账户 balance 与按 client_key/network/
// connection/provider_id/method 聚合的 request_count / cu，Keys API 返回每个 Key 的
// cu_spent_today / cu_day_limit / rate_limit / status / key_id / name / 更新时间；
// 没有给出完整响应样例，字段也可能被上游改名。因此这里不按固定 struct 解码，而是：
//
//  1. 把 json.RawMessage 用 json.Decoder + UseNumber 解成 any（number 保持原始文本，避免精度损失）；
//  2. 递归收集候选字段，字段名统一小写去分隔符后比对（request_count / requestCount 等价）；
//  3. 取值规则：同一语义取"最浅深度"的候选（总量优先于明细）；同深度的多个候选求和
//     （按 client_key/network/... 分组时，各分组之和就是总量）；
//  4. 同一对象内同语义的多个别名只取优先级最高的一个，避免把同一个数算两遍；
//  5. 找不到就留零值/nil，由调用方标注 unexpected_shape——绝不猜测数值。

const (
	kindBalance   = "balance"
	kindRequests  = "requests"
	kindCU        = "cu"
	kindUnit      = "unit"
	kindTier      = "tier"
	kindUpdatedAt = "updated_at"
)

// normalizeKey 统一字段名：小写并去掉下划线/连字符/空格/点。
// 这样 "request_count"、"requestCount"、"Request Count" 落到同一个键上。
func normalizeKey(key string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		switch r {
		case '_', '-', ' ', '.', '\t':
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func normalizeMap(object map[string]any) map[string]any {
	out := make(map[string]any, len(object))
	for key, value := range object {
		out[normalizeKey(key)] = value
	}
	return out
}

func lookupNorm(normalized map[string]any, names ...string) (any, bool) {
	for _, name := range names {
		if value, ok := normalized[name]; ok {
			return value, true
		}
	}
	return nil, false
}

// metricKind 判断字段名对应的语义与优先级（0 最高）。返回空字符串表示不是指标字段。
func metricKind(key string) (string, int) {
	switch normalizeKey(key) {
	case "balance":
		return kindBalance, 0
	case "accountbalance", "totalbalance":
		return kindBalance, 1
	case "remainingbalance":
		return kindBalance, 2
	case "requestcount":
		return kindRequests, 0
	case "requests":
		return kindRequests, 1
	case "totalrequests", "requeststotal", "requestscount":
		return kindRequests, 2
	case "cu":
		return kindCU, 0
	case "cuused", "usedcu":
		return kindCU, 1
	case "credits", "creditsused", "cutotal", "totalcu":
		return kindCU, 2
	case "computeunits":
		return kindCU, 3
	case "balanceunit":
		return kindUnit, 0
	case "currency", "unit":
		return kindUnit, 1
	case "tier":
		return kindTier, 0
	case "accounttier", "plan":
		return kindTier, 1
	case "subscription", "package":
		return kindTier, 2
	case "updatedat":
		return kindUpdatedAt, 0
	case "asof", "lastupdated", "generatedat":
		return kindUpdatedAt, 1
	}
	return "", 0
}

type metricSample struct {
	depth    int
	kind     string
	priority int
	value    any
}

// collectMetricSamples 递归收集候选字段。数组元素深度 +1；
// 同一对象内同语义的多个别名只保留优先级最高的一个。
func collectMetricSamples(node any, depth int, out *[]metricSample) {
	switch value := node.(type) {
	case map[string]any:
		best := map[string]metricSample{}
		for key, raw := range value {
			if kind, priority := metricKind(key); kind != "" {
				if current, ok := best[kind]; !ok || priority < current.priority {
					best[kind] = metricSample{depth: depth, kind: kind, priority: priority, value: raw}
				}
			}
			collectMetricSamples(raw, depth+1, out)
		}
		for _, sample := range best {
			*out = append(*out, sample)
		}
	case []any:
		for _, item := range value {
			collectMetricSamples(item, depth+1, out)
		}
	}
}

// metricsTotals 是一个 range 区间的聚合结果。
type metricsTotals struct {
	Balance *decimal.Decimal
	// BalanceText 是上游返回的余额原文（含尾零/科学计数法原样保留），落库与展示优先用它。
	BalanceText string
	BalanceUnit string
	Tier        string
	Requests    int64
	CU          int64
	UpdatedAt   *time.Time

	SawBalance  bool
	SawRequests bool
	SawCU       bool
}

// parseMetrics 解析 Statistics API 响应。返回的 missing 列出本次响应里找不到的语义字段
// （balance / request_count / cu），调用方据此标注 unexpected_shape。
func parseMetrics(raw json.RawMessage) (metricsTotals, []string, error) {
	root, err := decodeLoose(raw)
	if err != nil {
		return metricsTotals{}, nil, err
	}
	var samples []metricSample
	collectMetricSamples(root, 0, &samples)
	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].depth != samples[j].depth {
			return samples[i].depth < samples[j].depth
		}
		return samples[i].priority < samples[j].priority
	})

	var totals metricsTotals
	var missing []string

	if sample, ok := firstConvertible(samples, kindBalance, func(value any) bool {
		_, ok := toDecimal(value)
		return ok
	}); ok {
		if value, ok := toDecimal(sample.value); ok {
			totals.Balance = &value
			totals.BalanceText = rawNumberText(sample.value, value)
			totals.SawBalance = true
		}
	}
	if !totals.SawBalance {
		missing = append(missing, "balance")
	}

	if sum, ok := sumAtMinDepth(samples, kindRequests); ok {
		totals.Requests = sum
		totals.SawRequests = true
	} else {
		missing = append(missing, "request_count")
	}
	if sum, ok := sumAtMinDepth(samples, kindCU); ok {
		totals.CU = sum
		totals.SawCU = true
	} else {
		missing = append(missing, "cu")
	}

	if sample, ok := firstConvertible(samples, kindUnit, isText); ok {
		if text, ok := toText(sample.value); ok {
			totals.BalanceUnit = strings.TrimSpace(text)
		}
	}
	if sample, ok := firstConvertible(samples, kindTier, isText); ok {
		if text, ok := toText(sample.value); ok {
			totals.Tier = strings.TrimSpace(text)
		}
	}
	if sample, ok := firstConvertible(samples, kindUpdatedAt, isTime); ok {
		if parsed, ok := toTime(sample.value); ok {
			totals.UpdatedAt = &parsed
		}
	}
	return totals, missing, nil
}

// rawNumberText 取数值的原始文本：json.Number 与字符串原样返回（只去首尾空白），
// 其它形态回退到 decimal 的规范化输出。
func rawNumberText(value any, parsed decimal.Decimal) string {
	switch typed := value.(type) {
	case json.Number:
		return strings.TrimSpace(typed.String())
	case string:
		return strings.TrimSpace(typed)
	}
	return parsed.String()
}

func isText(value any) bool {
	_, ok := toText(value)
	return ok
}

func isTime(value any) bool {
	_, ok := toTime(value)
	return ok
}

// firstConvertible 按（深度，优先级）顺序返回第一个可转换的候选。
func firstConvertible(samples []metricSample, kind string, convertible func(any) bool) (metricSample, bool) {
	for _, sample := range samples {
		if sample.kind != kind {
			continue
		}
		if convertible(sample.value) {
			return sample, true
		}
	}
	return metricSample{}, false
}

// sumAtMinDepth 在最浅的**可解析为整数**深度上求和：
// 上游若同时给出总量与明细，只取总量；若只给分组数组，各分组之和即为总量。
func sumAtMinDepth(samples []metricSample, kind string) (int64, bool) {
	minDepth := 0
	found := false
	var total int64
	for _, sample := range samples {
		if sample.kind != kind {
			continue
		}
		value, ok := toInt64(sample.value)
		if !ok {
			continue
		}
		if !found || sample.depth < minDepth {
			minDepth, total, found = sample.depth, value, true
			continue
		}
		if sample.depth == minDepth {
			total += value
		}
	}
	return total, found
}

// ---- Keys API ----

// teamKey 是 Keys API 列表里与本系统相关的一个 Key。
type teamKey struct {
	ID           string
	Name         string
	Enabled      *bool
	CUSpentToday *int64
	CUDayLimit   *int64
	RateLimit    *float64
	Tier         string
	UpdatedAt    *time.Time
	Value        string // 上游若返回 Key 明文，仅用于本地算指纹，绝不落库/进日志
	Fingerprint  string
}

var (
	keyIDFields      = []string{"keyid", "id", "key"}
	keyNameFields    = []string{"name", "keyname", "label", "title"}
	keyStatusFields  = []string{"status", "enabled", "active", "isenabled", "isactive"}
	keySpentFields   = []string{"cuspenttoday", "cutoday", "usedcu", "cuspent", "spenttoday"}
	keyLimitFields   = []string{"cudaylimit", "dailyculimit", "culimit", "daylimit", "culimitdaily"}
	keyRateFields    = []string{"ratelimit", "ratelimitpersecond", "rpslimit", "requestsperminute"}
	keyUpdatedFields = []string{"updatedat", "lastupdated", "updated", "lastusedat", "createdat"}
	keyValueFields   = []string{"apikey", "keyvalue", "value", "token", "secret", "key"}
	keyTierFields    = []string{"tier", "plan", "accounttier", "subscription", "package"}
)

type keyCandidate struct {
	object map[string]any
	strong bool
}

// parseTeamKeys 解析 Keys API 响应：接受 {data:{...}}、直接对象、对象数组、
// data 里嵌数组等形态（开发文档 §3.3 未给样例）。
func parseTeamKeys(raw json.RawMessage) ([]teamKey, error) {
	root, err := decodeLoose(raw)
	if err != nil {
		return nil, err
	}
	var candidates []keyCandidate
	collectKeyCandidates(root, &candidates)

	hasStrong := false
	for _, candidate := range candidates {
		if candidate.strong {
			hasStrong = true
			break
		}
	}
	keys := make([]teamKey, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if hasStrong && !candidate.strong {
			// 有强信号对象时忽略弱信号对象，避免把 team/profile 之类的元数据当成 Key。
			continue
		}
		key := extractTeamKey(candidate.object)
		if key.ID == "" && key.Name == "" && key.Enabled == nil &&
			key.CUSpentToday == nil && key.CUDayLimit == nil && key.RateLimit == nil {
			continue
		}
		dedupe := key.ID + "\x00" + key.Name
		if seen[dedupe] {
			continue
		}
		seen[dedupe] = true
		keys = append(keys, key)
	}
	return keys, nil
}

// collectKeyCandidates 找到"像 Key 的对象"。命中即视为叶子，不再深入其内部，
// 避免把 Key 对象里的嵌套元数据重复计成一个 Key。
func collectKeyCandidates(node any, out *[]keyCandidate) {
	switch value := node.(type) {
	case map[string]any:
		if strong, weak := keyObjectSignals(value); strong || weak {
			*out = append(*out, keyCandidate{object: value, strong: strong})
			return
		}
		for _, child := range value {
			collectKeyCandidates(child, out)
		}
	case []any:
		for _, child := range value {
			collectKeyCandidates(child, out)
		}
	}
}

// keyObjectSignals 区分强信号（Key 专有字段）与弱信号（通用字段）。
func keyObjectSignals(object map[string]any) (strong, weak bool) {
	normalized := normalizeMap(object)
	for _, name := range []string{"keyid", "cuspenttoday", "cudaylimit", "ratelimit", "apikey", "keyvalue", "key"} {
		if _, ok := normalized[name]; ok {
			strong = true
			break
		}
	}
	weakCount := 0
	for _, name := range []string{"name", "status", "enabled", "updatedat", "id"} {
		if _, ok := normalized[name]; ok {
			weakCount++
		}
	}
	return strong, weakCount >= 2
}

func extractTeamKey(object map[string]any) teamKey {
	normalized := normalizeMap(object)
	var key teamKey
	if value, ok := lookupNorm(normalized, keyIDFields...); ok {
		if text, ok := toText(value); ok {
			key.ID = strings.TrimSpace(text)
		}
	}
	if value, ok := lookupNorm(normalized, keyNameFields...); ok {
		if text, ok := toText(value); ok {
			key.Name = strings.TrimSpace(text)
		}
	}
	if value, ok := lookupNorm(normalized, keyStatusFields...); ok {
		if enabled, ok := toBool(value); ok {
			key.Enabled = &enabled
		}
	}
	if value, ok := lookupNorm(normalized, keySpentFields...); ok {
		if spent, ok := toInt64(value); ok {
			key.CUSpentToday = &spent
		}
	}
	if value, ok := lookupNorm(normalized, keyLimitFields...); ok {
		if limit, ok := toInt64(value); ok {
			key.CUDayLimit = &limit
		}
	}
	if value, ok := lookupNorm(normalized, keyRateFields...); ok {
		if rate, ok := toFloat64(value); ok {
			key.RateLimit = &rate
		}
	}
	if value, ok := lookupNorm(normalized, keyTierFields...); ok {
		if text, ok := toText(value); ok {
			key.Tier = strings.TrimSpace(text)
		}
	}
	if value, ok := lookupNorm(normalized, keyUpdatedFields...); ok {
		if parsed, ok := toTime(value); ok {
			key.UpdatedAt = &parsed
		}
	}
	if value, ok := lookupNorm(normalized, keyValueFields...); ok {
		if text, ok := toText(value); ok {
			key.Value = strings.TrimSpace(text)
			if key.Value != "" {
				key.Fingerprint = fingerprint(key.Value)
			}
		}
	}
	return key
}

// selectTeamKey 选出本系统实际使用的 runtime key：
// 1) API Key 指纹（sha256 前 16 hex，与 provider_secrets.fingerprint 同法）；
// 2) Key 名；3) Key ID —— app 注入的 selector 可能不是指纹，而是名字或 ID；
// 都匹配不到时退回"status 为启用且 cu_spent_today 最大"的那个（正在干活的 Key 就是它）。
func selectTeamKey(keys []teamKey, selector string) (teamKey, bool) {
	selector = strings.TrimSpace(selector)
	if selector != "" {
		for _, key := range keys {
			if key.Fingerprint != "" && strings.EqualFold(key.Fingerprint, selector) {
				return key, true
			}
		}
		for _, key := range keys {
			if key.Name != "" && strings.EqualFold(key.Name, selector) {
				return key, true
			}
		}
		for _, key := range keys {
			if key.ID != "" && strings.EqualFold(key.ID, selector) {
				return key, true
			}
		}
	}
	best := teamKey{}
	found := false
	for _, key := range keys {
		if key.Enabled == nil || !*key.Enabled {
			continue
		}
		if !found || spentOf(key) > spentOf(best) {
			best, found = key, true
		}
	}
	return best, found
}

func spentOf(key teamKey) int64 {
	if key.CUSpentToday == nil {
		return 0
	}
	return *key.CUSpentToday
}

// fingerprint 与 internal/secret 的 preview 同法：sha256 前 16 hex。
func fingerprint(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])[:16]
}

// ---- 基础转换 ----

func decodeLoose(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func toDecimal(value any) (decimal.Decimal, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := decimal.NewFromString(strings.TrimSpace(typed.String()))
		if err != nil {
			return decimal.Decimal{}, false
		}
		return parsed, true
	case string:
		cleaned := strings.NewReplacer(",", "", " ", "").Replace(strings.TrimSpace(typed))
		cleaned = strings.TrimPrefix(cleaned, "+")
		if cleaned == "" {
			return decimal.Decimal{}, false
		}
		parsed, err := decimal.NewFromString(cleaned)
		if err != nil {
			return decimal.Decimal{}, false
		}
		return parsed, true
	case float64:
		return decimal.NewFromFloat(typed), true
	case float32:
		return decimal.NewFromFloat32(typed), true
	case int:
		return decimal.NewFromInt(int64(typed)), true
	case int64:
		return decimal.NewFromInt(typed), true
	case uint64:
		return decimal.NewFromUint64(typed), true
	}
	return decimal.Decimal{}, false
}

func toInt64(value any) (int64, bool) {
	parsed, ok := toDecimal(value)
	if !ok {
		return 0, false
	}
	return parsed.IntPart(), true
}

func toFloat64(value any) (float64, bool) {
	parsed, ok := toDecimal(value)
	if !ok {
		return 0, false
	}
	result, exact := parsed.Float64()
	if !exact && result == 0 {
		return 0, false
	}
	return result, true
}

func toText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(typed), true
	}
	return "", false
}

func toBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "enabled", "active", "ok", "yes", "1":
			return true, true
		case "false", "disabled", "inactive", "revoked", "suspended", "no", "0":
			return false, true
		}
	case json.Number:
		if number, err := typed.Int64(); err == nil {
			return number != 0, true
		}
	case float64:
		return typed != 0, true
	}
	return false, false
}

// toTime 接受 RFC3339（含纳秒）、"2006-01-02 15:04:05"、纯日期、秒级时间戳（数字或字符串）。
func toTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case json.Number:
		seconds, err := typed.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(seconds, 0).UTC(), true
	case float64:
		return time.Unix(int64(typed), 0).UTC(), true
	case string:
		return parseTimeLoose(typed)
	}
	return time.Time{}, false
}

func parseTimeLoose(raw string) (time.Time, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), true
		}
	}
	if seconds, err := strconv.ParseInt(text, 10, 64); err == nil && seconds > 0 {
		return time.Unix(seconds, 0).UTC(), true
	}
	return time.Time{}, false
}
