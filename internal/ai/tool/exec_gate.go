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
// 按会话分片的 DocGate 由 internal/app/ai 在每次 Send 时通过 WithDocGate 注入
// （实例挂在 AI 结构体上，内部按 convID 分片，DeleteConversation 时 Reset）。
// 未注入 ctx 的调用方拿到的是 nil，必须等同于"放行"——DocGate 只是引导机制，
// 不是安全边界，真正的边界是权限检查。
//
// 这里刻意不提供进程级默认实例回退：曾经有过一个，但它被进程内所有调用方共享，
// 会话之间互相"学会"同一类型只是坏味道，真正的问题是它让测试行为依赖执行
// 顺序——两个都用裸 context.Background() 的测试并发/乱序跑（`go test -shuffle=on`）
// 时会互相污染对方的门禁状态，6 次里能有 5 次失败。没有默认实例，调用方要么显式
// 注入 *DocGate，要么显式接受 nil == 放行，没有第三种隐藏状态。

type docGateKeyType struct{}

// WithDocGate 把 *DocGate 注入 ctx。
func WithDocGate(ctx context.Context, gate *DocGate) context.Context {
	return context.WithValue(ctx, docGateKeyType{}, gate)
}

// GetDocGate 返回 ctx 上注入的 *DocGate；未注入时返回 nil。
// 调用方必须把 nil 返回值当作"放行"处理——DocGate 是引导机制，不是安全边界，
// 真正的边界是权限检查。
func GetDocGate(ctx context.Context) *DocGate {
	g, _ := ctx.Value(docGateKeyType{}).(*DocGate)
	return g
}
