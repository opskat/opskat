package tool

import (
	"context"
	"sync"
)

// DocGate 记录"某会话内，某资产类型的用法文档已经到过模型面前"。
// 满足条件有两条：模型显式调用过 help，或该类型文档已被注入本次 Send 的 system prompt。
// 生命周期与会话一致，与 LocalToolGate 的 allow-list 相同。
type DocGate struct {
	mu   sync.RWMutex
	seen map[int64]map[string]bool
}

func NewDocGate() *DocGate {
	return &DocGate{seen: make(map[int64]map[string]bool)}
}

// MarkDocumented 标记该会话已知晓该资产类型的用法。
func (g *DocGate) MarkDocumented(convID int64, assetType string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.seen[convID] == nil {
		g.seen[convID] = make(map[string]bool)
	}
	g.seen[convID][assetType] = true
}

// IsDocumented 查询该会话是否已知晓该资产类型的用法。
func (g *DocGate) IsDocumented(convID int64, assetType string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.seen[convID][assetType]
}

// Reset 清空某会话的记录。
func (g *DocGate) Reset(convID int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.seen, convID)
}

// --- context 注入 ---
//
// 真正按会话分片的 DocGate 尚未接入 runner（下一个任务的工作）：在那之前，
// defaultDocGate 是唯一实例，被进程内所有调用方共享。这比设计意图粗一档
// （不同会话之间会互相"学会"同一类型），但不是安全隐患——DocGate 只是引导
// 机制，真正的边界是权限检查；下一个任务接入 runner 后，各会话会通过
// WithDocGate 注入各自的实例。

type docGateKeyType struct{}

var defaultDocGate = NewDocGate()

// WithDocGate 把 *DocGate 注入 ctx，覆盖进程级默认实例。
func WithDocGate(ctx context.Context, gate *DocGate) context.Context {
	return context.WithValue(ctx, docGateKeyType{}, gate)
}

// GetDocGate 返回 ctx 上注入的 *DocGate；未注入时回退到进程级默认实例。
// 调用方必须把 nil 返回值当作"放行"处理——DocGate 是引导机制，不是安全边界，
// 真正的边界是权限检查。
func GetDocGate(ctx context.Context) *DocGate {
	if g, ok := ctx.Value(docGateKeyType{}).(*DocGate); ok {
		return g
	}
	return defaultDocGate
}
