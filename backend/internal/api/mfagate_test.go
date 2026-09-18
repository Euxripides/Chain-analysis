package api

// 适配层的失败模式：typed-nil 陷阱。
//
// newMFAChallengeGate 返回接口类型，若 store 为 nil 时返回具体指针，
// iam 侧 gate != nil 会为真，登录流程就会在调用 Enrolled 时 panic —— 这是
// Go 里最容易漏、也最难在集成测试里复现的一类缺陷。

import (
	"testing"

	"github.com/blockchain-platform/blockchain-platform/internal/iam"
)

func TestNewMFAChallengeGateWithNilStoreIsNilInterface(t *testing.T) {
	var gate iam.MFAChallengeGate = newMFAChallengeGate(nil)
	if gate != nil {
		t.Fatal("nil store must produce a nil interface, otherwise login would call into a nil store")
	}
}
