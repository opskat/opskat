package tool

import "sync"

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
