// Package conntest 维护资产「表单连接测试」的 runtime 分发注册表。
//
// 各连接测试是 binder 的实例方法(持有 live manager/pool),无法在 init() 注册;
// 故 binder 在 New() 时把去掉信封的 tester 注册进来,由 system binder 的
// TestAssetConnection 统一查表分发(共享 i18n ctx + 超时 + testreg 取消信封)。
package conntest

import (
	"context"
	"sync"
)

// TestFunc 用给定 ctx(已含超时/取消)测试一份未保存的资产配置。
// configJSON 是前端配置的 JSON;plainPassword 为空时由 tester 自行兜底解析。
type TestFunc func(ctx context.Context, configJSON, plainPassword string) error

// DetailedTestFunc 与 TestFunc 相同，但连通时额外返回一句给用户看的详情（如通用资产的
// HTTP 状态码与经过的隧道）。详情不得含任何密钥或注入值。
type DetailedTestFunc func(ctx context.Context, configJSON, plainPassword string) (string, error)

var (
	mu      sync.RWMutex
	testers = make(map[string]DetailedTestFunc)
)

// Register 登记某资产类型的 tester(同类型重复登记以最后一次为准)。
func Register(assetType string, fn TestFunc) {
	RegisterDetailed(assetType, func(ctx context.Context, configJSON, plainPassword string) (string, error) {
		return "", fn(ctx, configJSON, plainPassword)
	})
}

// RegisterDetailed 登记一个连通时带详情的 tester；Lookup 与 LookupDetailed 都能取到它。
func RegisterDetailed(assetType string, fn DetailedTestFunc) {
	mu.Lock()
	testers[assetType] = fn
	mu.Unlock()
}

// Unregister 移除某资产类型的 tester(主要供测试清理)。
func Unregister(assetType string) {
	mu.Lock()
	delete(testers, assetType)
	mu.Unlock()
}

// Lookup 取某资产类型的 tester;未注册返回 ok=false。详情型 tester 的详情被丢弃。
func Lookup(assetType string) (TestFunc, bool) {
	fn, ok := LookupDetailed(assetType)
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, configJSON, plainPassword string) error {
		_, err := fn(ctx, configJSON, plainPassword)
		return err
	}, true
}

// LookupDetailed 取某资产类型的 tester；只登记了 TestFunc 的类型详情恒为空。
func LookupDetailed(assetType string) (DetailedTestFunc, bool) {
	mu.RLock()
	fn, ok := testers[assetType]
	mu.RUnlock()
	return fn, ok
}
