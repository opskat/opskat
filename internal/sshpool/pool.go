package sshpool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/singleflight"
)

// PoolDialer 创建 SSH 连接的接口，由调用方实现以解耦凭据解析和跳板机逻辑
type PoolDialer interface {
	DialAsset(ctx context.Context, assetID int64) (*ssh.Client, []io.Closer, error)
}

// poolEntry 连接池条目
//
// mu 只保护引用计数、时间戳和 closed，从不跨网络操作持有：探活与关闭连接都在锁外进行。
// closed 置位即"已退役"：之后 tryAcquire 一律失败，由置位的一方负责 closeConn。
type poolEntry struct {
	client   *ssh.Client
	closers  []io.Closer // 跳板机等中间连接
	assetID  int64
	dialedAt time.Time

	mu       sync.Mutex
	lastUsed time.Time
	refCount int
	closed   bool

	closeOnce sync.Once
}

// tryAcquire 增加引用计数；条目已退役时返回 false。
func (e *poolEntry) tryAcquire() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	e.refCount++
	e.lastUsed = time.Now()
	return true
}

// release 减少引用计数
func (e *poolEntry) release() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refCount--
	e.lastUsed = time.Now()
}

func (e *poolEntry) isClosed() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.closed
}

// retireIfIdle 无持有者且空闲超过 timeout 时让条目退役并返回 true；调用方随后负责 closeConn。
func (e *poolEntry) retireIfIdle(timeout time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.refCount > 0 || time.Since(e.lastUsed) <= timeout {
		return false
	}
	e.closed = true
	return true
}

// retireRefused 减少引用计数；已无持有者且连接建立超过 minAge 时让条目退役并返回 true，
// 调用方随后负责 closeConn。
func (e *poolEntry) retireRefused(minAge time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.refCount--
	e.lastUsed = time.Now()
	if e.refCount > 0 || time.Since(e.dialedAt) < minAge {
		return false
	}
	e.closed = true
	return true
}

// close 让条目退役并关闭连接。
func (e *poolEntry) close() {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	e.closeConn()
}

// closeConn 关闭连接及所有中间连接，只生效一次。可能阻塞在网络上，调用方不得持有任何锁。
func (e *poolEntry) closeConn() {
	e.closeOnce.Do(func() {
		if err := e.client.Close(); err != nil {
			logger.Default().Warn("close ssh client", zap.Int64("assetID", e.assetID), zap.Error(err))
		}
		// closers 里含 keepalive 的 stop（由 ssh_svc.Dial 挂入），一并关闭即停心跳。
		for _, c := range e.closers {
			if err := c.Close(); err != nil {
				logger.Default().Warn("close intermediate connection", zap.Int64("assetID", e.assetID), zap.Error(err))
			}
		}
	})
}

// PoolEntryInfo 连接池条目信息（用于 UI 展示）
type PoolEntryInfo struct {
	AssetID  int64 `json:"asset_id"`
	RefCount int   `json:"ref_count"`
	LastUsed int64 `json:"last_used"` // Unix timestamp
}

// Pool SSH 连接池
//
// 锁序：p.mu 之后才可取 entry.mu，且两者都不跨网络操作持有。
// sf 保证同一资产并发首次 Get 只拨号一次。
type Pool struct {
	mu        sync.RWMutex
	entries   map[int64]*poolEntry
	sf        singleflight.Group
	dialer    PoolDialer
	idleTime  time.Duration
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once

	// refusedRedialInterval 见 WithRefusedRedialInterval。
	refusedRedialInterval time.Duration
	// probeTimeout 是 Get 探活等待回复的上限，超时即视连接已死并重拨。
	probeTimeout time.Duration
}

// defaultRefusedRedialInterval 是 WithRefusedRedialInterval 的默认值。
const defaultRefusedRedialInterval = 30 * time.Second

// defaultProbeTimeout 是 Get 探活的默认等待上限。
const defaultProbeTimeout = 10 * time.Second

// Option 调整连接池的行为。
type Option func(*Pool)

// WithRefusedRedialInterval 设置一条连接建立多久之后，才会因为拒绝转发而被弃用重拨
// （见 ReleaseRefused）。它给重拨限速：服务器确实禁止转发时，驱动的重试循环每个间隔
// 最多换来一次 SSH 登录，而不是每次失败都登录一次。
func WithRefusedRedialInterval(d time.Duration) Option {
	return func(p *Pool) { p.refusedRedialInterval = d }
}

// withProbeTimeout 设置 Get 探活的等待上限（测试用）。
func withProbeTimeout(d time.Duration) Option {
	return func(p *Pool) { p.probeTimeout = d }
}

// NewPool 创建连接池
func NewPool(dialer PoolDialer, idleTimeout time.Duration, opts ...Option) *Pool {
	p := &Pool{
		entries:  make(map[int64]*poolEntry),
		dialer:   dialer,
		idleTime: idleTimeout,
		done:     make(chan struct{}),

		refusedRedialInterval: defaultRefusedRedialInterval,
		probeTimeout:          defaultProbeTimeout,
	}
	for _, opt := range opts {
		opt(p)
	}
	p.wg.Add(1)
	go p.cleanupLoop()
	return p
}

// Get 获取或创建一个 SSH 连接，调用方用完后必须调用 Release
//
// 缓存的连接先探活：探活不持锁、最多等 probeTimeout，没回复就当连接已死，关闭后重拨。
// 同一资产的并发拨号合并为一次。
func (p *Pool) Get(ctx context.Context, assetID int64) (*ssh.Client, error) {
	for {
		p.mu.RLock()
		entry := p.entries[assetID]
		p.mu.RUnlock()

		if entry != nil {
			alive, err := p.probe(ctx, entry)
			if err != nil {
				return nil, err
			}
			if alive && entry.tryAcquire() {
				return entry.client, nil
			}
			if !alive {
				p.removeEntry(assetID, entry)
			}
		}

		entry, err := p.dialShared(ctx, assetID)
		var abandoned *abandonedDialError
		if errors.As(err, &abandoned) && ctx.Err() == nil {
			// 合并拨号被发起方自己的 ctx 中止了，与本调用方无关：重新拨号。
			continue
		}
		if err != nil {
			return nil, err
		}
		if entry.tryAcquire() {
			return entry.client, nil
		}
		// 拨出的连接在取用前已被移除（Remove / 空闲清理），重新来过。
	}
}

// probe 探测连接是否存活，不持任何锁。等待超过 probeTimeout 视为已死；ctx 结束时返回其错误。
// 超时后挂起的请求在连接被关闭（removeEntry）时返回。
func (p *Pool) probe(ctx context.Context, e *poolEntry) (bool, error) {
	if e.isClosed() {
		return false, nil
	}
	replied := make(chan error, 1)
	go func() {
		_, _, err := e.client.SendRequest("keepalive@openssh.com", true, nil)
		replied <- err
	}()
	timer := time.NewTimer(p.probeTimeout)
	defer timer.Stop()
	select {
	case err := <-replied:
		return err == nil, nil
	case <-timer.C:
		logger.Default().Warn("ssh pool probe timed out, redialing", zap.Int64("assetID", e.assetID), zap.Duration("timeout", p.probeTimeout))
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// abandonedDialError 标记一次因发起方 ctx 结束而失败的合并拨号。拨号用的是发起方的 ctx，
// 同时等这次拨号的其它调用方拿到它时若自己的 ctx 仍有效，应当重新拨号而不是跟着失败。
type abandonedDialError struct{ err error }

func (e *abandonedDialError) Error() string { return e.err.Error() }
func (e *abandonedDialError) Unwrap() error { return e.err }

// dialShared 拨号并把新连接放进池里；同一资产同时只有一次拨号，其余调用方等它的结果。
// 返回的条目尚未计引用，由调用方 tryAcquire。发起方 ctx 结束导致的失败以
// *abandonedDialError 交回。
func (p *Pool) dialShared(ctx context.Context, assetID int64) (*poolEntry, error) {
	ch := p.sf.DoChan(strconv.FormatInt(assetID, 10), func() (any, error) {
		// 上一轮合并拨号可能刚放进池里。
		p.mu.RLock()
		existing := p.entries[assetID]
		p.mu.RUnlock()
		if existing != nil && !existing.isClosed() {
			return existing, nil
		}

		client, closers, err := p.dialer.DialAsset(ctx, assetID)
		if err != nil {
			err = fmt.Errorf("dial asset %d: %w", assetID, err)
			if ctx.Err() != nil {
				return nil, &abandonedDialError{err: err}
			}
			return nil, err
		}
		now := time.Now()
		entry := &poolEntry{
			client:   client,
			closers:  closers,
			assetID:  assetID,
			dialedAt: now,
			lastUsed: now,
		}
		p.mu.Lock()
		p.entries[assetID] = entry
		p.mu.Unlock()
		return entry, nil
	})
	select {
	case r := <-ch:
		if r.Err != nil {
			return nil, r.Err
		}
		return r.Val.(*poolEntry), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// removeEntry 把 entry 移出池（池里已换成别的条目时不动池）并关闭它。
func (p *Pool) removeEntry(assetID int64, entry *poolEntry) {
	p.mu.Lock()
	if p.entries[assetID] == entry {
		delete(p.entries, assetID)
	}
	p.mu.Unlock()
	entry.close()
}

// Release 释放连接引用
func (p *Pool) Release(assetID int64) {
	p.mu.RLock()
	entry, ok := p.entries[assetID]
	p.mu.RUnlock()
	if ok {
		entry.release()
	}
}

// ReleaseRefused 释放连接引用，并报告 client 刚以 ssh.Prohibited 拒绝了一次通道打开。
//
// sshd 的转发策略（AllowTcpForwarding / PermitOpen）在连接建立时就定下了，之后改配置
// 再 reload 也只影响新连接，所以这条连接留在池里就会一直拒绝下去。它已无其他持有者、
// 且建立已超过 refusedRedialInterval 时，把它移出池并关闭，返回 true：调用方重新 Get
// 会得到一条按服务器当前策略建立的新连接。仍有持有者时不动它 —— 别的隧道还在用。
func (p *Pool) ReleaseRefused(assetID int64, client *ssh.Client) bool {
	p.mu.Lock()
	entry, ok := p.entries[assetID]
	if !ok || entry.client != client {
		p.mu.Unlock()
		// client 已被移出池（资产保存、连接断开），池里没有它的引用可释放；再 Get 就是另一条连接。
		return true
	}
	if !entry.retireRefused(p.refusedRedialInterval) {
		p.mu.Unlock()
		return false
	}
	delete(p.entries, assetID)
	p.mu.Unlock()
	entry.closeConn()
	logger.Default().Info("closed connection that refused forwarding", zap.Int64("assetID", assetID))
	return true
}

// Remove 强制移除并关闭连接
func (p *Pool) Remove(assetID int64) {
	p.mu.Lock()
	entry, ok := p.entries[assetID]
	if ok {
		delete(p.entries, assetID)
	}
	p.mu.Unlock()
	if ok {
		entry.close()
	}
}

// snapshot 返回池中当前条目的副本，供调用方在锁外逐个处理。
func (p *Pool) snapshot() []*poolEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	entries := make([]*poolEntry, 0, len(p.entries))
	for _, entry := range p.entries {
		entries = append(entries, entry)
	}
	return entries
}

// List 返回所有连接池条目信息
func (p *Pool) List() []PoolEntryInfo {
	entries := p.snapshot()
	infos := make([]PoolEntryInfo, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		infos = append(infos, PoolEntryInfo{
			AssetID:  entry.assetID,
			RefCount: entry.refCount,
			LastUsed: entry.lastUsed.Unix(),
		})
		entry.mu.Unlock()
	}
	return infos
}

// Close 关闭连接池（可安全多次调用）
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		p.wg.Wait()

		p.mu.Lock()
		entries := p.entries
		p.entries = make(map[int64]*poolEntry)
		p.mu.Unlock()
		for _, entry := range entries {
			entry.close()
		}
	})
}

// cleanupLoop 后台清理空闲连接
func (p *Pool) cleanupLoop() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.cleanupIdle()
		}
	}
}

// cleanupIdle 关闭空闲超时的连接：逐条判定时不持 p.mu，关闭在所有锁外进行。
func (p *Pool) cleanupIdle() {
	var idle []*poolEntry
	for _, entry := range p.snapshot() {
		if entry.retireIfIdle(p.idleTime) {
			idle = append(idle, entry)
		}
	}
	if len(idle) == 0 {
		return
	}
	p.mu.Lock()
	for _, entry := range idle {
		if p.entries[entry.assetID] == entry {
			delete(p.entries, entry.assetID)
		}
	}
	p.mu.Unlock()
	for _, entry := range idle {
		entry.closeConn()
		logger.Default().Info("closed idle connection", zap.Int64("assetID", entry.assetID))
	}
}
