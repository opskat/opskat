// Package assetconn 维护「按资产处置在用连接」的注册表。
//
// 资产被删除或改了连接配置之后，各协议管理器里还挂着这个资产的会话/客户端（SSH 终端、
// RDP/VNC 会话、Kafka 客户端、连接池条目……）。asset_svc 不能反向 import 每一个协议
// 服务去逐个处理，那既会形成循环依赖，也把删除流程变成一张按类型分支的表。
//
// 因此这里提供一个注册式 seam，两类钩子对应两种事件：
//
//   - Closer（Register）：**交互式会话**，用户开着的终端 / 远程桌面 / 串口。
//     只在资产被删除时关掉，由 asset_svc.Delete → CloseAsset 触发。
//   - Invalidator（RegisterInvalidator）：**缓存或池化的连接**，下次用到时会自动重拨。
//     资产改了配置（asset_svc.Update → InvalidateAsset）和被删除时都要丢弃。
//
// 分两类是因为"改配置"和"删资产"要的行为不同：改了备注就把用户开着的终端踢掉，比
// 让它连着旧配置更糟；而池化连接不丢，用户改完口令之后的查询还在用旧连接，改了等于没改。
// CloseAsset 是 InvalidateAsset 的**超集**（资产都删了，缓存当然也不能留），所以池化
// 那一侧只需登记 Invalidator，不必为删除路径再登记一遍。
//
// group_svc.Delete 连组带资产删时也在事务提交后逐个广播 CloseAsset——那条路走
// asset_repo.DeleteByGroupID，绕过了 asset_svc。
//
// 有三类**刻意没接**，不是漏了：
//   - k8s 日志流：streamID 形如 `k8s-log-<自增数>`，与 assetID 无关，接之前得先改 key 格式。
//   - internal/ai/helper.ConnCache：单次 AI Send 内的短期缓存，实例挂在 chat ctx 上无法枚举。
//   - 本地终端（localterm）：本地 shell 该不该随资产删除被杀，是产品决策而非技术限制。
package assetconn

import (
	"context"
	"maps"
	"slices"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Closer 关闭指定资产当前持有的交互式会话。
// 资产没有在用会话时应当直接返回 nil —— CloseAsset 会对所有已注册实现广播，
// 「这个资产不归我管」是常态而不是错误。
type Closer func(ctx context.Context, assetID int64) error

// Invalidator 丢弃指定资产被缓存/池化的连接，使下次使用时按最新配置重新拨号。
// 与 Closer 的约定相同：不归自己管时返回 nil。
type Invalidator func(ctx context.Context, assetID int64) error

var (
	mu           sync.RWMutex
	closers      = map[string]Closer{}
	invalidators = map[string]Invalidator{}
)

// Register 登记一个按资产关闭交互式会话的实现。name 只用于日志定位是哪个协议失败了。
// 同名重复登记以最后一次为准：钩子绑定的是 live manager 实例，管理器被重建时
// 旧实例上的会话已经无人可达，继续广播给它没有意义（与 conntest.Register 同语义）。
func Register(name string, closer Closer) {
	if name == "" || closer == nil {
		panic("assetconn: invalid closer registration")
	}
	mu.Lock()
	closers[name] = closer
	mu.Unlock()
}

// RegisterInvalidator 登记一个按资产丢弃缓存/池化连接的实现。语义同 Register。
func RegisterInvalidator(name string, invalidator Invalidator) {
	if name == "" || invalidator == nil {
		panic("assetconn: invalid invalidator registration")
	}
	mu.Lock()
	invalidators[name] = invalidator
	mu.Unlock()
}

// CloseAsset 资产被删除：关闭它的交互式会话，并丢弃它被缓存/池化的连接。
// 单个实现失败或 panic 只记日志并继续下一个：资产已经删了，
// 任何一个协议关不掉都不该拖累其它协议，更不该把删除流程 panic 掉。
func CloseAsset(ctx context.Context, assetID int64) {
	dispatch(ctx, "close", assetID, snapshot(closers), snapshot(invalidators))
}

// InvalidateAsset 资产配置被修改：只丢弃缓存/池化的连接，让下次使用按新配置重拨。
// 不动交互式会话——用户开着的终端不该因为改了一个字段就被掐断。
func InvalidateAsset(ctx context.Context, assetID int64) {
	dispatch(ctx, "invalidate", assetID, snapshot(invalidators))
}

// UnregisterForTest 摘掉一个已登记的钩子（两张表同名的都摘）。仅供其它包的测试用：
// 注册表是进程级的，测试注册的假钩子不摘掉会漏进后续用例。
// （包内测试直接改两张 map，见 registry_test.go 的 reset。）
func UnregisterForTest(name string) {
	mu.Lock()
	delete(closers, name)
	delete(invalidators, name)
	mu.Unlock()
}

// snapshot 在锁内复制一份，让实际调用发生在锁外：钩子里可能反过来碰到注册表
// （管理器重建时会重新 Register），持锁调用就死锁了。
func snapshot[F ~func(context.Context, int64) error](src map[string]F) map[string]Closer {
	mu.RLock()
	defer mu.RUnlock()
	out := make(map[string]Closer, len(src))
	for name, fn := range src {
		out[name] = Closer(fn)
	}
	return out
}

// dispatch 按表依次执行，表内按 name 排序（顺序确定，便于对日志）。
// **不合并成一张表**：同一个协议在两张表里各登记一个同名钩子是正常的
// （ssh 的会话关闭与隧道池失效都叫 "ssh"），合并会让其中一个被静默吃掉。
func dispatch(ctx context.Context, action string, assetID int64, tables ...map[string]Closer) {
	type hook struct {
		name string
		fn   Closer
	}
	var hooks []hook
	for _, table := range tables {
		names := slices.Sorted(maps.Keys(table))
		for _, name := range names {
			hooks = append(hooks, hook{name: name, fn: table[name]})
		}
	}
	if len(hooks) == 0 {
		return
	}

	logger.Ctx(ctx).Info(action+" asset connections start",
		zap.Int64("assetID", assetID), zap.Int("hooks", len(hooks)))
	for _, h := range hooks {
		runOne(ctx, action, h.name, h.fn, assetID)
	}
	logger.Ctx(ctx).Info(action+" asset connections end", zap.Int64("assetID", assetID))
}

func runOne(ctx context.Context, action, name string, fn Closer, assetID int64) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(ctx).Error(action+" asset connections panic recovered",
				zap.String("hook", name), zap.Int64("assetID", assetID),
				zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	if err := fn(ctx, assetID); err != nil {
		logger.Ctx(ctx).Error(action+" asset connections fail",
			zap.String("hook", name), zap.Int64("assetID", assetID), zap.Error(err))
	}
}
