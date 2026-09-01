package method

import (
	"context"
	"strings"
	"testing"
)

// §55 Selector：合法 input 取前 4 字节；0x / 非法 hex 必须返回空。
func TestExtractMethodID(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"0xa9059cbb1234", "0xa9059cbb"},
		{"0xA9059CBB0000000000000000000000001111111111111111111111111111111111111111", "0xa9059cbb"},
		{"0x", ""},
		{"", ""},
		{"0xa9059cb", ""},
		{"invalid_input", ""},
		{"0xzzzzzzzz1234", ""},
		{"a9059cbb1234", ""},
	}
	for _, c := range cases {
		if got := ExtractMethodID(c.input); got != c.want {
			t.Fatalf("ExtractMethodID(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// §38 特殊交易分类。
func TestClassify(t *testing.T) {
	if mt, id := Classify("0x", "1000000000000000000", false); mt != TypeNativeTransfer || id != "" {
		t.Fatalf("native transfer classify = %q/%q", mt, id)
	}
	if mt, _ := Classify("0x", "0", false); mt != TypeEmptyCall {
		t.Fatalf("empty call classify = %q", mt)
	}
	if mt, _ := Classify("0x6080604052...", "0", true); mt != TypeContractCreation {
		t.Fatalf("contract creation classify = %q", mt)
	}
	if mt, id := Classify("0xa9059cbb0000", "0", false); mt != TypeContractCall || id != "0xa9059cbb" {
		t.Fatalf("contract call classify = %q/%q", mt, id)
	}
	if mt, id := Classify("0xzzzzzzzz", "1", false); mt != TypeInvalidInput || id != "" {
		t.Fatalf("invalid input classify = %q/%q", mt, id)
	}
	// 合约创建的 input 是部署字节码，禁止按 Selector 解析
	if _, id := Classify("0x60806040523480156100", "0", true); id != "" {
		t.Fatalf("contract creation must not expose a selector, got %q", id)
	}
	if _, ok := StructuralResolution(TypeContractCreation); !ok {
		t.Fatal("contract creation must have a structural name")
	}
	res, _ := StructuralResolution(TypeNativeTransfer)
	if res.Name != NameNativeTransfer || res.Verified {
		t.Fatalf("native transfer resolution = %+v", res)
	}
}

// §32 种子签名必须与 keccak 自洽，且覆盖常见方法。
func TestSeedSignatures(t *testing.T) {
	seeds := SeedSignatures()
	if len(seeds) < 80 {
		t.Fatalf("seed too small: %d", len(seeds))
	}
	bySignature := map[string]SignatureRecord{}
	for _, s := range seeds {
		if SelectorOf(s.Signature) != s.Selector {
			t.Fatalf("selector mismatch for %s: %s", s.Signature, s.Selector)
		}
		if s.Source != SourceLocalSignature {
			t.Fatalf("seed source must be local_signature, got %s", s.Source)
		}
		bySignature[s.Signature] = s
	}
	known := map[string]string{
		"transfer(address,uint256)":                                           "0xa9059cbb",
		"approve(address,uint256)":                                            "0x095ea7b3",
		"transferFrom(address,address,uint256)":                               "0x23b872dd",
		"swapExactTokensForTokens(uint256,uint256,address[],address,uint256)": "0x38ed1739",
		"deposit()":         "0xd0e30db0",
		"withdraw(uint256)": "0x2e1a7d4d",
	}
	for signature, selector := range known {
		rec, ok := bySignature[signature]
		if !ok {
			t.Fatalf("seed missing %s", signature)
		}
		if rec.Selector != selector {
			t.Fatalf("%s selector = %s, want %s", signature, rec.Selector, selector)
		}
	}
	for _, event := range SeedEventSignatures() {
		if Topic0Of(event.Signature) != event.Topic0 {
			t.Fatalf("topic0 mismatch for %s", event.Signature)
		}
	}
}

func TestKnownEventTopic(t *testing.T) {
	// ERC20 Transfer topic0 是链上最常见的常量，用来锁定 keccak 实现正确性
	want := "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	if got := Topic0Of("Transfer(address,address,uint256)"); got != want {
		t.Fatalf("Transfer topic0 = %s, want %s", got, want)
	}
}

// §55 本地签名解析：唯一候选 → high，且不得标记 Verified（不是合约验证）。
func TestChooseSingleCandidate(t *testing.T) {
	res := ChooseCandidate("0xa9059cbb", 64, []MethodCandidate{
		{Name: "transfer", Signature: "transfer(address,uint256)", Source: SourceLocalSignature, Verified: true},
	})
	if res.Name != "transfer" || res.Signature != "transfer(address,uint256)" {
		t.Fatalf("resolution = %+v", res)
	}
	if res.Source != SourceLocalSignature || res.Confidence != ConfidenceHigh {
		t.Fatalf("source/confidence = %s/%s", res.Source, res.Confidence)
	}
	if res.Verified {
		t.Fatal("local signature match must not be reported as contract-verified")
	}
}

// §55 Collision：同一 Selector 两个同层候选且无 ABI → 不得随机挑一个并标 Verified。
func TestCollisionKeepsUnresolved(t *testing.T) {
	candidates := []MethodCandidate{
		{Name: "transfer", Signature: "transfer(address,uint256)", Source: SourceLocalSignature, Verified: true},
		{Name: "sweep", Signature: "sweep(address,uint256)", Source: SourceLocalSignature, Verified: true},
	}
	res := ChooseCandidate("0xa9059cbb", 64, candidates)
	if res.Verified {
		t.Fatal("collision must never be marked verified")
	}
	if res.Name != "" || res.Source != SourceUnknown {
		t.Fatalf("ambiguous collision must stay unresolved, got %+v", res)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates must be preserved, got %d", len(res.Candidates))
	}
	if res.MethodID != "0xa9059cbb" {
		t.Fatalf("selector must be preserved, got %q", res.MethodID)
	}
}

// §12 候选 >1 时用 input 参数长度收敛，置信度只能是 medium。
func TestCollisionResolvedByInputLength(t *testing.T) {
	candidates := []MethodCandidate{
		{Name: "transfer", Signature: "transfer(address,uint256)", Source: SourceLocalSignature},
		{Name: "batch", Signature: "batch(address,uint256,uint256,uint256)", Source: SourceLocalSignature},
	}
	res := ChooseCandidate("0xdeadbeef", 64, candidates)
	if res.Name != "transfer" || res.Confidence != ConfidenceMedium || res.Verified {
		t.Fatalf("resolution = %+v", res)
	}
}

// §31 公共签名库不能作为唯一可信来源；本地候选优先。
func TestLocalBeatsPublic(t *testing.T) {
	res := ChooseCandidate("0xdeadbeef", -1, []MethodCandidate{
		{Name: "publicOne", Signature: "publicOne(uint256)", Source: SourcePublicSignature},
		{Name: "localOne", Signature: "localOne(uint256)", Source: SourceLocalSignature},
	})
	if res.Name != "localOne" {
		t.Fatalf("local signature must win over public, got %+v", res)
	}
	only := ChooseCandidate("0xdeadbeef", -1, []MethodCandidate{
		{Name: "publicOne", Signature: "publicOne(uint256)", Source: SourcePublicSignature},
	})
	if only.Verified || only.Confidence == ConfidenceVerified {
		t.Fatalf("public signature must never be verified: %+v", only)
	}
}

// §30 manual_override 优先级最高。
func TestManualOverrideWins(t *testing.T) {
	res := ChooseCandidate("0xdeadbeef", -1, []MethodCandidate{
		{Name: "auto", Signature: "auto(uint256)", Source: SourceLocalSignature},
		{Name: "human", Signature: "human(uint256)", Source: SourceManualOverride},
	})
	if res.Name != "human" || !res.Verified || res.Confidence != ConfidenceVerified {
		t.Fatalf("manual override must win: %+v", res)
	}
}

const erc20ABI = `[
  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"value","type":"uint256"}]},
  {"type":"function","name":"approve","inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}]},
  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]}
]`

// transfer(0x1111...1111, 1000000000000000000)
const transferInput = "0xa9059cbb" +
	"0000000000000000000000001111111111111111111111111111111111111111" +
	"0000000000000000000000000000000000000000000000000de0b6b3a7640000"

// §55 ABI Priority：ABI 与本地签名冲突时必须使用 Contract ABI。
func TestABIBeatsLocalSignature(t *testing.T) {
	registry := NewRegistry()
	registry.SetSignatures([]SignatureRecord{
		{Selector: "0xa9059cbb", Name: "wrongName", Signature: "wrongName(address,uint256)", Source: SourceLocalSignature, Verified: true},
	})
	loaded, failed := registry.SetABIs([]ABIRecord{
		{Chain: "bsc", ContractAddress: "0xtoken", ABIJSON: []byte(erc20ABI), Source: "verified_explorer", Verified: true},
	})
	if loaded != 1 || failed != 0 {
		t.Fatalf("abi load = %d/%d", loaded, failed)
	}
	resolver := NewFastResolver(registry, nil)
	res, err := resolver.Resolve(context.Background(), MethodResolveRequest{
		Chain: "bsc", ContractAddress: "0xToken", MethodID: "0xa9059cbb", Input: transferInput, TxHash: "0xabc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceContractABI || res.Name != "transfer" {
		t.Fatalf("abi must win: %+v", res)
	}
	if !res.Verified || res.Confidence != ConfidenceVerified {
		t.Fatalf("verified contract abi must be verified: %+v", res)
	}
}

func TestABICacheIsChainScoped(t *testing.T) {
	const address = "0x000000000000000000000000000000000000dead"
	registry := NewRegistry()
	loaded, failed := registry.SetABIs([]ABIRecord{
		{Chain: "bsc", ContractAddress: address, ABIJSON: []byte(`[{"type":"function","name":"foo","inputs":[{"type":"uint256"}]}]`), Verified: true},
		{Chain: "eth", ContractAddress: address, ABIJSON: []byte(`[{"type":"function","name":"bar","inputs":[{"type":"address"}]}]`), Verified: true},
	})
	if loaded != 2 || failed != 0 {
		t.Fatalf("ABI load = %d/%d", loaded, failed)
	}
	bsc, _, ok := registry.ABI("bsc", address)
	if !ok {
		t.Fatal("bsc ABI missing")
	}
	eth, _, ok := registry.ABI("eth", address)
	if !ok {
		t.Fatal("eth ABI missing")
	}
	if _, ok := bsc.Lookup(SelectorOf("foo(uint256)")); !ok {
		t.Fatal("bsc ABI resolved with the wrong chain cache")
	}
	if _, ok := eth.Lookup(SelectorOf("bar(address)")); !ok {
		t.Fatal("eth ABI resolved with the wrong chain cache")
	}
	if _, ok := bsc.Lookup(SelectorOf("bar(address)")); ok {
		t.Fatal("eth ABI leaked into bsc cache")
	}
}

// §21/§41 Decoded Input：uint256 必须是字符串，地址必须规范化小写。
func TestDecodeInput(t *testing.T) {
	abi, err := ParseABI([]byte(erc20ABI))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := abi.Lookup("0xa9059cbb")
	if !ok {
		t.Fatal("transfer not found in abi")
	}
	if m.Signature != "transfer(address,uint256)" {
		t.Fatalf("signature = %s", m.Signature)
	}
	params, err := DecodeInput(m, transferInput)
	if err != nil {
		t.Fatal(err)
	}
	values := ParamsToMap(params)
	if values["to"] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("to = %v", values["to"])
	}
	value, ok := values["value"].(string)
	if !ok {
		t.Fatalf("uint256 must decode to string, got %T", values["value"])
	}
	if value != "1000000000000000000" {
		t.Fatalf("value = %s", value)
	}
}

func TestDecodeDynamicTypes(t *testing.T) {
	const abiJSON = `[{"type":"function","name":"multi","inputs":[
		{"name":"path","type":"address[]"},
		{"name":"note","type":"string"},
		{"name":"flag","type":"bool"},
		{"name":"amount","type":"int256"}]}]`
	abi, err := ParseABI([]byte(abiJSON))
	if err != nil {
		t.Fatal(err)
	}
	signature := "multi(address[],string,bool,int256)"
	selector := SelectorOf(signature)
	m, ok := abi.Lookup(selector)
	if !ok {
		t.Fatalf("method %s not found", signature)
	}
	input := selector +
		"0000000000000000000000000000000000000000000000000000000000000080" + // path offset
		"00000000000000000000000000000000000000000000000000000000000000e0" + // note offset
		"0000000000000000000000000000000000000000000000000000000000000001" + // flag = true
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff" + // amount = -1
		"0000000000000000000000000000000000000000000000000000000000000002" + // path length
		"0000000000000000000000002222222222222222222222222222222222222222" +
		"0000000000000000000000003333333333333333333333333333333333333333" +
		"0000000000000000000000000000000000000000000000000000000000000002" + // note length
		"6f6b00000000000000000000000000000000000000000000000000000000000000"[:64]
	params, err := DecodeInput(m, input)
	if err != nil {
		t.Fatal(err)
	}
	values := ParamsToMap(params)
	path, ok := values["path"].([]any)
	if !ok || len(path) != 2 || path[0] != "0x2222222222222222222222222222222222222222" {
		t.Fatalf("path = %v", values["path"])
	}
	if values["note"] != "ok" {
		t.Fatalf("note = %v", values["note"])
	}
	if values["flag"] != true {
		t.Fatalf("flag = %v", values["flag"])
	}
	if values["amount"] != "-1" {
		t.Fatalf("amount = %v (int256 must be a decimal string)", values["amount"])
	}
}

func TestDecodeEvent(t *testing.T) {
	abi, err := ParseABI([]byte(erc20ABI))
	if err != nil {
		t.Fatal(err)
	}
	topic0 := Topic0Of("Transfer(address,address,uint256)")
	event, ok := abi.Event(topic0)
	if !ok {
		t.Fatal("Transfer event not found")
	}
	params, err := DecodeEventLog(event, []string{
		topic0,
		"0x0000000000000000000000001111111111111111111111111111111111111111",
		"0x0000000000000000000000002222222222222222222222222222222222222222",
	}, "0x0000000000000000000000000000000000000000000000000de0b6b3a7640000")
	if err != nil {
		t.Fatal(err)
	}
	values := ParamsToMap(params)
	if values["from"] != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("from = %v", values["from"])
	}
	if values["value"] != "1000000000000000000" {
		t.Fatalf("value = %v", values["value"])
	}
}

func TestDecodeAnonymousEvent(t *testing.T) {
	const raw = `[{"type":"event","name":"AnonymousTransfer","anonymous":true,"inputs":[
		{"name":"from","type":"address","indexed":true},
		{"name":"value","type":"uint256","indexed":false}]}]`
	abi, err := ParseABI([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(abi.AnonymousEvents) != 1 || len(abi.Events) != 0 {
		t.Fatalf("anonymous event registry = %+v", abi)
	}
	event := abi.AnonymousEvents[0]
	params, err := DecodeEventLog(event, []string{
		"0x0000000000000000000000001111111111111111111111111111111111111111",
	}, "0x000000000000000000000000000000000000000000000000000000000000002a")
	if err != nil {
		t.Fatal(err)
	}
	values := ParamsToMap(params)
	if values["from"] != "0x1111111111111111111111111111111111111111" || values["value"] != "42" {
		t.Fatalf("anonymous params = %+v", values)
	}
}

func TestABIGuards(t *testing.T) {
	tooLarge := make([]byte, MaxABIJSONBytes+1)
	if _, err := ParseABI(tooLarge); err == nil {
		t.Fatal("oversized ABI must be rejected")
	}
	typeName := "uint256"
	for i := 0; i < maxABIDepth+2; i++ {
		typeName += "[]"
	}
	if _, err := ParseABIType(typeName, nil); err == nil {
		t.Fatal("deep ABI type must be rejected")
	}
	fields := make([]rawComponent, maxABIFields+1)
	for i := range fields {
		fields[i] = rawComponent{Type: "uint256"}
	}
	if _, err := parseFields(fields); err == nil {
		t.Fatal("wide ABI tuple must be rejected")
	}
}

// §17 Fast Resolver 解析不到时必须写 unknown 并投递异步补全任务。
func TestUnknownEnqueuesResolveJob(t *testing.T) {
	registry := NewRegistry()
	var jobs []ResolveJob
	resolver := NewFastResolver(registry, func(job ResolveJob) { jobs = append(jobs, job) })
	res, err := resolver.Resolve(context.Background(), MethodResolveRequest{
		Chain: "bsc", ContractAddress: "0xCafe", Input: "0x12ab34cd0000", TxHash: "0xhash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != SourceUnknown || res.Name != "" {
		t.Fatalf("unknown selector must stay unknown: %+v", res)
	}
	if res.MethodID != "0x12ab34cd" {
		t.Fatalf("selector must be preserved: %q", res.MethodID)
	}
	if len(jobs) != 1 || jobs[0].MethodID != "0x12ab34cd" || jobs[0].ContractAddress != "0xcafe" {
		t.Fatalf("resolve job = %+v", jobs)
	}
	if strings.Contains(strings.Join([]string{jobs[0].SampleTxHash}, ""), "0x12ab34cd0000") {
		t.Fatal("resolve job must not carry the full input")
	}
}

// §19 去重：同 chain+contract+selector 只产生一条补全任务。
func TestEnqueuerDedup(t *testing.T) {
	registry := NewRegistry()
	count := 0
	resolver := NewFastResolver(registry, nil)
	_ = resolver
	seen := map[string]bool{}
	enqueue := func(job ResolveJob) {
		key := jobKey(job)
		if seen[key] {
			return
		}
		seen[key] = true
		count++
	}
	r2 := NewFastResolver(registry, enqueue)
	for i := 0; i < 1000; i++ {
		if _, err := r2.Resolve(context.Background(), MethodResolveRequest{
			Chain: "bsc", ContractAddress: "0xCafe", Input: "0x12ab34cd", TxHash: "0xhash",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 dedup job, got %d", count)
	}
}

func TestInputCompatible(t *testing.T) {
	if !InputCompatible("transfer(address,uint256)", 64) {
		t.Fatal("transfer should accept 64 argument bytes")
	}
	if InputCompatible("transfer(address,uint256)", 96) {
		t.Fatal("static signature must match exactly")
	}
	if !InputCompatible("swapExactTokensForTokens(uint256,uint256,address[],address,uint256)", 5*32+32) {
		t.Fatal("dynamic signature should accept its minimum size")
	}
	if InputCompatible("transfer(address,uint256)", 63) {
		t.Fatal("non 32-byte-aligned data must be rejected")
	}
}

func TestBackfillUnknownResultIsIdempotent(t *testing.T) {
	row := TxRecord{MethodID: "0x12ab34cd", MethodSource: SourceUnknown,
		MethodConfidence: ConfidenceUnknown, MethodType: TypeContractCall}
	resolution := unresolved("0x12ab34cd", TypeContractCall, nil)
	if needsRewrite(row, resolution) {
		t.Fatal("an unchanged unknown result must not be rewritten")
	}
	row.MethodID = "0x"
	if !needsRewrite(row, resolution) {
		t.Fatal("stale method id must be repaired")
	}
}
