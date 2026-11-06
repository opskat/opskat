package connpool

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

// 面板类缓存的默认空闲时限与驱逐扫描间隔:超过 DefaultConnIdleTTL 未用的连接被关闭。
const (
	DefaultConnIdleTTL       = 5 * time.Minute
	DefaultConnEvictInterval = 30 * time.Second
)

// ErrConnInvalidated 表示拨号进行中该资产的缓存被失效（改配置 / 删除 / 关闭缓存）：
// 这次拨出的连接按旧配置建立，已被关闭，重试即按最新配置重拨。
var ErrConnInvalidated = errors.New("连接配置已变更，请重试")

// AssetConnCache 按资产持久化长连接(查询面板、Redis 浏览器),按 key(如 "<assetID>:<database>")缓存。
//
// 与 internal/ai/ConnCache 的差别:
//   - 生命周期:那个是每次 AI Send 用完即弃;这个是 App 生命周期 + 空闲 TTL。
//   - 后台 evictor(RunEvictor)周期扫描,超过 idleTTL 未使用的项目自动 Close + 移除。
//   - key 用 string,可以同时区分 assetID 和 database。
//
// 持有者负责启动 RunEvictor、退出时 Close,并在 assetconn 登记 DropAsset,
// 让资产改配置 / 删除后下次使用按新配置重拨。
//
// 并发模型:mu 保护 map 与失效代数;sf 保证同 key 并发首拨只发生一次。
// 拨号期间该资产被失效(DropAsset / Close)时,拨出的连接不进缓存(见 GetOrDial)。
type AssetConnCache[C io.Closer] struct {
	mu      sync.Mutex
	entries map[string]*assetConnEntry[C]
	// closeGen / dropGen 是 Close 与各资产 DropAsset 的累计次数,两者之和变了即说明拨号期间被失效过。
	closeGen uint64
	dropGen  map[int64]uint64
	sf       singleflight.Group
	idleTTL  time.Duration
	name     string // 仅用于日志区分,如 "database"、"redis"、"mongodb"
}

type assetConnEntry[C io.Closer] struct {
	client C
	tunnel io.Closer
	// assetID 让 DropAsset 能按资产回收。key 里虽然也带 assetID,但那是字符串前缀,
	// 拿 "1:" 去匹配会把 "12:xxx" 一起误伤,所以单独存一份。
	assetID  int64
	lastUsed atomic.Int64 // unix nano
	// holds 是正在占用这条连接的操作数(见 Hold)。非零时它不算空闲。
	holds atomic.Int64
}

// NewAssetConnCache 创建空缓存。name 仅用于日志区分。
func NewAssetConnCache[C io.Closer](name string, idleTTL time.Duration) *AssetConnCache[C] {
	return &AssetConnCache[C]{
		entries: make(map[string]*assetConnEntry[C]),
		dropGen: make(map[int64]uint64),
		idleTTL: idleTTL,
		name:    name,
	}
}

// GetOrDial 从缓存取连接,不存在则调 dial 并入缓存。
// assetID 只用于 DropAsset 按资产回收;缓存命中与否仍只看 key。
// 从进入 GetOrDial 到拨号完成之间该资产被失效时,dial 用的是失效前的配置:拨出的连接被关闭,
// 返回 ErrConnInvalidated,下次调用按最新配置重拨。
func (c *AssetConnCache[C]) GetOrDial(assetID int64, key string, dial func() (C, io.Closer, error)) (C, io.Closer, error) {
	c.mu.Lock()
	if e, ok := c.entries[key]; ok {
		e.lastUsed.Store(time.Now().UnixNano())
		client := e.client
		c.mu.Unlock()
		return client, nil, nil
	}
	gen := c.genLocked(assetID)
	c.mu.Unlock()

	v, err, _ := c.sf.Do(key, func() (any, error) {
		c.mu.Lock()
		if e, ok := c.entries[key]; ok {
			e.lastUsed.Store(time.Now().UnixNano())
			client := e.client
			c.mu.Unlock()
			return client, nil
		}
		c.mu.Unlock()

		client, closer, derr := dial()
		if derr != nil {
			return *new(C), derr
		}
		e := &assetConnEntry[C]{client: client, tunnel: closer, assetID: assetID}
		e.lastUsed.Store(time.Now().UnixNano())
		c.mu.Lock()
		if c.genLocked(assetID) != gen {
			c.mu.Unlock()
			c.closeEntry(key, e)
			return *new(C), ErrConnInvalidated
		}
		c.entries[key] = e
		c.mu.Unlock()
		return client, nil
	})
	if err != nil {
		var zero C
		return zero, nil, err
	}
	return v.(C), nil, nil
}

func (c *AssetConnCache[C]) genLocked(assetID int64) uint64 {
	return c.closeGen + c.dropGen[assetID]
}

// Hold 标记 key 的连接正被一次操作占用,直到调用返回的 release:占用期间空闲驱逐不收它。
// 一次操作可以比 idleTTL 长(整表导出的游标跨多次调用读同一条会话),只在取连接时记一次
// lastUsed 会让它读到一半被当成空闲的关掉。release 把 lastUsed 记到操作结束的时刻,只生效一次。
// 显式失效(Drop / DropAsset / Close)不看占用,照样关闭。key 不在缓存里时 release 是空操作。
func (c *AssetConnCache[C]) Hold(key string) (release func()) {
	c.mu.Lock()
	e, ok := c.entries[key]
	if ok {
		e.holds.Add(1)
	}
	c.mu.Unlock()
	if !ok {
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// 先记时刻再放开占用:驱逐扫描看到占用归零时,lastUsed 已经是新的。
			e.lastUsed.Store(time.Now().UnixNano())
			e.holds.Add(-1)
		})
	}
}

// Drop 关闭并移除指定 key 的连接。
func (c *AssetConnCache[C]) Drop(key string) {
	c.mu.Lock()
	e, ok := c.entries[key]
	delete(c.entries, key)
	c.mu.Unlock()
	if !ok {
		return
	}
	c.closeEntry(key, e)
}

// DropAsset 关闭并移除指定资产的全部连接(一个资产可能缓存了多个库/多个 db index)。
func (c *AssetConnCache[C]) DropAsset(assetID int64) {
	c.mu.Lock()
	c.dropGen[assetID]++
	var dropped map[string]*assetConnEntry[C]
	for key, e := range c.entries {
		if e.assetID == assetID {
			if dropped == nil {
				dropped = make(map[string]*assetConnEntry[C])
			}
			dropped[key] = e
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	for key, e := range dropped {
		c.closeEntry(key, e)
	}
}

// Close 关闭并移除所有连接。可重复调用,二次调用为空操作。
func (c *AssetConnCache[C]) Close() error {
	c.mu.Lock()
	c.closeGen++
	entries := c.entries
	c.entries = make(map[string]*assetConnEntry[C])
	c.mu.Unlock()
	for key, e := range entries {
		c.closeEntry(key, e)
	}
	return nil
}

// RunEvictor 周期驱逐,阻塞到 ctx 取消,调用方用 go 启动。每 evictInterval 扫一次,
// 把超过 idleTTL 未使用、且当前没有被占用的项关闭并移除。ctx 取消时退出。
func (c *AssetConnCache[C]) RunEvictor(ctx context.Context, evictInterval time.Duration) {
	ticker := time.NewTicker(evictInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.EvictIdle(now)
		}
	}
}

// EvictIdle 扫一次缓存(RunEvictor 每个周期调一次,测试可直接指定时刻),把 lastUsed 早于 (now - idleTTL) 且没有被占用(Hold)的项目驱逐。
func (c *AssetConnCache[C]) EvictIdle(now time.Time) {
	threshold := now.Add(-c.idleTTL).UnixNano()
	c.mu.Lock()
	var stale map[string]*assetConnEntry[C]
	for key, e := range c.entries {
		if e.holds.Load() == 0 && e.lastUsed.Load() < threshold {
			if stale == nil {
				stale = make(map[string]*assetConnEntry[C])
			}
			stale[key] = e
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	for key, e := range stale {
		c.closeEntry(key, e)
	}
}

// size 仅用于测试断言。
func (c *AssetConnCache[C]) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *AssetConnCache[C]) closeEntry(key string, e *assetConnEntry[C]) {
	if err := e.client.Close(); err != nil && !isExpectedCachedCloseErr(err) {
		logger.Default().Warn("close cached "+c.name+" connection", zap.String("key", key), zap.Error(err))
	}
	if e.tunnel != nil {
		if err := e.tunnel.Close(); err != nil && !isExpectedCachedCloseErr(err) {
			logger.Default().Warn("close "+c.name+" tunnel", zap.String("key", key), zap.Error(err))
		}
	}
}

func isExpectedCachedCloseErr(err error) bool {
	return err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}
