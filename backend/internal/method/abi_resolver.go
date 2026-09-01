package method

import (
	"context"
	"strings"
	"time"
)

// ABIResolver 合约 ABI 精确解析（§21）。
// 优先级仅次于人工覆盖，永远高于公共签名库（§62-5）。
type ABIResolver struct {
	registry *Registry
}

func NewABIResolver(registry *Registry) *ABIResolver { return &ABIResolver{registry: registry} }

// Resolve 实现 Resolver 接口。
func (a *ABIResolver) Resolve(ctx context.Context, req MethodResolveRequest) (MethodResolution, error) {
	_ = ctx
	methodType, selector := Classify(req.Input, req.Value, req.ToIsNull)
	if res, ok := StructuralResolution(methodType); ok {
		return res, nil
	}
	if res, ok := a.ResolveSelector(req.Chain, req.ContractAddress, selector); ok {
		return res, nil
	}
	return unresolved(selector, methodType, nil), nil
}

// ResolveSelector 按 chain + contract + selector 查 ABI。
// Proxy 合约：代理 ABI 未命中时回落到 implementation ABI（§14/§30 优先级 3）。
func (a *ABIResolver) ResolveSelector(chain, contract, selector string) (MethodResolution, bool) {
	m, record, ok := a.lookupMethod(chain, contract, selector)
	if !ok {
		return MethodResolution{}, false
	}
	confidence := ConfidenceVerified
	if !record.Verified {
		// ABI 存在但来源未验证（例如用户上传）：不能声称 verified
		confidence = ConfidenceHigh
	}
	ResolveContractABITotal.Inc()
	return MethodResolution{
		MethodID:   m.Selector,
		Name:       m.Name,
		Signature:  m.Signature,
		Source:     SourceContractABI,
		Verified:   record.Verified,
		Confidence: confidence,
		MethodType: TypeContractCall,
		Candidates: []MethodCandidate{{Name: m.Name, Signature: m.Signature, Source: SourceContractABI, Verified: record.Verified}},
		ResolvedAt: time.Now().UTC(),
	}, true
}

// DecodeInput 用合约 ABI 解码参数（§21/§22）。
func (a *ABIResolver) DecodeInput(chain, contract, input string) (*ABIMethod, []DecodedParam, bool) {
	selector := ExtractMethodID(input)
	if selector == "" {
		return nil, nil, false
	}
	m, _, ok := a.lookupMethod(chain, contract, selector)
	if !ok {
		return nil, nil, false
	}
	params, err := DecodeInput(m, input)
	if err != nil {
		return m, nil, false
	}
	return m, params, true
}

// DecodeLog 用合约 ABI 解码事件（§29）。
func (a *ABIResolver) DecodeLog(chain, contract string, topics []string, data string) (*ABIEvent, []DecodedParam, bool) {
	abi, _, ok := a.registry.ABI(chain, contract)
	if !ok {
		return nil, nil, false
	}
	if len(topics) > 0 {
		if event, found := abi.Event(strings.ToLower(topics[0])); found {
			params, err := DecodeEventLog(event, topics, data)
			if err == nil {
				return event, params, true
			}
		}
	}
	// anonymous event 没有 topic0。只有 ABI 中恰好一个候选能完整解码时才采用，
	// 多候选保持 Unknown，避免猜测。
	var matched *ABIEvent
	var matchedParams []DecodedParam
	for _, event := range abi.AnonymousEvents {
		params, err := DecodeEventLog(event, topics, data)
		if err != nil {
			continue
		}
		if matched != nil {
			return nil, nil, false
		}
		matched, matchedParams = event, params
	}
	return matched, matchedParams, matched != nil
}

func (a *ABIResolver) lookupMethod(chain, contract, selector string) (*ABIMethod, ABIRecord, bool) {
	if a == nil || a.registry == nil || selector == "" || contract == "" {
		return nil, ABIRecord{}, false
	}
	abi, record, ok := a.registry.ABI(chain, contract)
	if ok {
		if m, found := abi.Lookup(selector); found {
			return m, record, true
		}
		// Proxy → Implementation（§14）
		if impl := strings.ToLower(strings.TrimSpace(record.ImplementationAddress)); impl != "" && impl != strings.ToLower(contract) {
			if implABI, implRecord, implOK := a.registry.ABI(chain, impl); implOK {
				if m, found := implABI.Lookup(selector); found {
					return m, implRecord, true
				}
			}
		}
	}
	return nil, ABIRecord{}, false
}
