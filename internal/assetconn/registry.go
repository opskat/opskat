// Package assetconn 维护「按资产断开在用连接」的注册表。
//
// 资产被删除后，各协议管理器里还挂着这个资产的会话/客户端（SSH 终端、RDP/VNC 会话、
// Kafka 客户端、连接池条目……）。asset_svc 不能反向 import 每一个协议服务去逐个关闭，
// 那既会形成循环依赖，也把删除流程变成一张按类型分支的表。
//
// 因此这里提供一个注册式 seam：各管理器在装配处调用 Register 登记自己的关闭实现，
// asset_svc.Delete 删除成功后调用 CloseAsset 统一触发。
package assetconn

import (
	"context"
	"sort"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// Closer 关闭指定资产当前持有的连接/会话。
// 资产没有在用连接时应当直接返回 nil —— CloseAsset 会对所有已注册实现广播，
// 「这个资产不归我管」是常态而不是错误。
type Closer func(ctx context.Context, assetID int64) error

var (
	mu      sync.RWMutex
	closers = map[string]Closer{}
)

// Register 登记一个按资产关闭连接的实现。name 只用于日志定位是哪个协议关失败了。
// 同名重复登记以最后一次为准：closer 绑定的是 live manager 实例，管理器被重建时
// 旧实例上的会话已经无人可达，继续广播给它没有意义（与 conntest.Register 同语义）。
func Register(name string, closer Closer) {
	if name == "" || closer == nil {
		panic("assetconn: invalid closer registration")
	}
	mu.Lock()
	closers[name] = closer
	mu.Unlock()
}

// CloseAsset 通知所有已注册实现关闭该资产的连接。
// 单个实现失败或 panic 只记日志并继续下一个：资产已经删了，
// 任何一个协议关不掉都不该拖累其它协议，更不该把删除流程 panic 掉。
func CloseAsset(ctx context.Context, assetID int64) {
	mu.RLock()
	names := make([]string, 0, len(closers))
	snapshot := make(map[string]Closer, len(closers))
	for name, closer := range closers {
		names = append(names, name)
		snapshot[name] = closer
	}
	mu.RUnlock()
	if len(names) == 0 {
		return
	}
	sort.Strings(names)

	logger.Ctx(ctx).Info("close asset connections start",
		zap.Int64("assetID", assetID), zap.Int("closers", len(names)))
	for _, name := range names {
		closeOne(ctx, name, snapshot[name], assetID)
	}
	logger.Ctx(ctx).Info("close asset connections end", zap.Int64("assetID", assetID))
}

func closeOne(ctx context.Context, name string, closer Closer, assetID int64) {
	defer func() {
		if r := recover(); r != nil {
			logger.Ctx(ctx).Error("close asset connections panic recovered",
				zap.String("closer", name), zap.Int64("assetID", assetID),
				zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	if err := closer(ctx, assetID); err != nil {
		logger.Ctx(ctx).Error("close asset connections fail",
			zap.String("closer", name), zap.Int64("assetID", assetID), zap.Error(err))
	}
}
