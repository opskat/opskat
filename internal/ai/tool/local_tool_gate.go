package tool

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/cago-frame/agents/agent"
	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/ai/policy"
)

// LocalToolApprovalRequest 是 LocalToolGate 发往前端的本地工具审批载荷。
//
// 与 permission.ApprovalItem 不同：本地工具没有 asset/group 概念，只有命令/路径本体；
// SubCommands 用于 local_bash 复合命令的展示与默认 pattern 生成。
type LocalToolApprovalRequest struct {
	ToolName        string   // "local_bash" | "local_write" | "local_edit"
	Command         string   // local_bash: 原始 command；local_write/local_edit: path
	Detail          string   // local_write 内容预览 / local_edit 改动预览，local_bash 留空
	SubCommands     []string // local_bash 按 mvdan.cc/sh 拆分的子命令；local_write/local_edit 为单条 = path
	DefaultPatterns []string // 默认 pattern（"git pull" → "git *"，path 默认原值），前端预填可编辑
}

// LocalToolConfirmFunc 由上层（App）注入，发起 Wails 事件并阻塞等待用户响应。
type LocalToolConfirmFunc func(ctx context.Context, req LocalToolApprovalRequest) permission.ApprovalResponse

// LocalToolGate 拦截 coding system 的 local_bash/local_write/local_edit 工具调用
// （即经 WrapLocalTool 重命名后的本地工具——见 internal/ai/local_tool_wrap.go）。
//
// 行为对齐远程 exec：local_bash 按 && / || / ; / | 拆出子命令，全部命中已存 pattern
// （* 通配，path.Match 语义）才放行；否则发起审批。"本次会话允许" 把用户编辑后的
// pattern 写入会话内存白名单，键为 conversationID。
type LocalToolGate struct {
	confirm LocalToolConfirmFunc
	allowed sync.Map // map[int64][]allowEntry
}

type allowEntry struct {
	Tool    string
	Pattern string
}

// NewLocalToolGate 构造 gate。
// confirm 为 nil 时测试场景可用，但任何未命中调用将直接 aictx.Deny。
func NewLocalToolGate(confirm LocalToolConfirmFunc) *LocalToolGate {
	return &LocalToolGate{confirm: confirm}
}

// Middleware 返回挂到 coding system 的 cago tool middleware。
// 仅与 local_(bash|write|edit) 这三件本地工具配套；调用方在 runner.go 用
// agent.Use(`^local_(bash|write|edit)$`, gate.Middleware()) 挂载。
//
// 行为：subjects 缺失或全部命中已存 pattern 时直接 Next 放行；无 confirm 回调
// 时 AbortWithDeny；用户 deny/无效响应 → AbortWithDeny；allowAll → 写白名单后 Next；
// 只有精确的 allow 才按单次允许处理。
func (g *LocalToolGate) Middleware() agent.ToolMiddleware {
	return func(c *agent.ToolContext) {
		toolName := c.ToolName

		subjects := extractSubjects(toolName, c.Input)
		if len(subjects) == 0 {
			// 输入异常（缺字段 / 空字符串），不阻塞，让工具自身处理并返回错误。
			c.Next()
			return
		}

		err := g.decide(c.Context(), subjects, LocalToolApprovalRequest{
			ToolName:        toolName,
			Command:         primaryCommand(toolName, c.Input),
			Detail:          detailOf(toolName, c.Input),
			SubCommands:     subjects,
			DefaultPatterns: defaultPatterns(toolName, subjects),
		})
		if err != nil {
			c.AbortWithDeny(err.Error())
			return
		}
		c.Next()
	}
}

// LocalWriteToolName 是本地写工具在门禁里的名字：白名单按它分片，审批弹框也按它渲染。
// 传输面的本地目的端复用它（见 CheckLocalWrites），因此"本次会话允许 /tmp/*"对模型直接
// 调用 local_write 和对 cp 的本地落点是同一条记录 —— 两份白名单会当场分叉。
const LocalWriteToolName = "local_write"

// 传输面（handleCp）与 exec 的 `object get --file=` 都经这个接口拿到它，见
// helper.WithLocalWriteGate；写成断言是为了签名漂移在这里报错，而不是在注入点。
var _ helper.LocalWriteGate = (*LocalToolGate)(nil)

// CheckLocalWrites 让一批本地写路径过一次 local_write 门禁，实现 helper.LocalWriteGate。
//
// 与模型直接调用 local_write 工具**走的是同一条判定**（同一份会话白名单、同一个对话框、
// 同一套 pattern 匹配），只是主体由调用方给出而不是从工具入参里抽。paths 必须非空：
// 空清单在 allMatch 里恒真，那是一次静默放行。
//
// detail 进对话框的 Detail 栏，用来说明"这些路径是从哪儿来的"（传输面传的是 cp 的两端原文）。
func (g *LocalToolGate) CheckLocalWrites(ctx context.Context, paths []string, detail string) error {
	return g.decide(ctx, paths, LocalToolApprovalRequest{
		ToolName: LocalWriteToolName,
		// 多条路径时 Command 逐行列出：前端把它当成审批项的正文渲染，而"始终允许"回来的
		// 编辑框同样按行拆（patternsFromResponse），两侧的形态因此一致。
		Command:         strings.Join(paths, "\n"),
		Detail:          detail,
		SubCommands:     paths,
		DefaultPatterns: defaultPatterns(LocalWriteToolName, paths),
	})
}

// decide 是门禁的判定本体：命中会话白名单直接放行，否则弹一次框。返回 nil 表示放行，
// 非 nil 是拒绝理由。middleware 与 CheckLocalWrites 共用它 —— 两条入口若各写一份，
// "本次会话允许"的记法迟早分叉。
func (g *LocalToolGate) decide(ctx context.Context, subjects []string, req LocalToolApprovalRequest) error {
	convID := aictx.GetConversationID(ctx)
	toolName := req.ToolName

	if g.allMatch(convID, toolName, subjects) {
		return nil
	}
	if g.confirm == nil {
		return fmt.Errorf("no approval mechanism configured for local tool %s", toolName)
	}

	resp := g.confirm(ctx, req)
	expected := []permission.ApprovalItem{{Type: toolName, Command: req.Command}}
	parsed, err := permission.ParseApprovalResponse(permission.ApprovalKindLocalTool, resp, expected)
	if err != nil {
		return fmt.Errorf("invalid approval response for local tool %s: %w", toolName, err)
	}

	switch parsed.Decision {
	case permission.ApprovalDeny:
		// 措辞是给模型看的指令（"立刻停下"），句号必须留着；用 %s 绕开 ST1005 是仓内既有
		// 写法（tool_handlers_cp.go 的 USER DENIED 同形）。
		return fmt.Errorf("%s",
			fmt.Sprintf("USER DENIED: user rejected local tool %s. Stop the current task.", toolName))
	case permission.ApprovalAllowAll:
		for _, p := range patternsFromResponse(toolName, subjects, parsed.EditedItems) {
			g.remember(convID, toolName, p)
		}
		return nil
	case permission.ApprovalAllow:
		return nil
	default:
		return fmt.Errorf("invalid approval decision for local tool %s", toolName)
	}
}

// Reset 清空指定 convID 的白名单。在删除会话或重置 provider 时调用。
func (g *LocalToolGate) Reset(convID int64) {
	g.allowed.Delete(convID)
}

func (g *LocalToolGate) remember(convID int64, tool, pattern string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return
	}
	cur, _ := g.allowed.LoadOrStore(convID, []allowEntry{})
	entries := cur.([]allowEntry)
	for _, e := range entries {
		if e.Tool == tool && e.Pattern == pattern {
			return
		}
	}
	newEntries := make([]allowEntry, len(entries), len(entries)+1)
	copy(newEntries, entries)
	newEntries = append(newEntries, allowEntry{Tool: tool, Pattern: pattern})
	g.allowed.Store(convID, newEntries)
}

func (g *LocalToolGate) allMatch(convID int64, tool string, subjects []string) bool {
	cur, ok := g.allowed.Load(convID)
	if !ok {
		return false
	}
	entries := cur.([]allowEntry)
	if len(entries) == 0 {
		return false
	}
	for _, sub := range subjects {
		matched := false
		for _, e := range entries {
			if e.Tool != tool {
				continue
			}
			if matchLocalPattern(tool, e.Pattern, sub) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// matchLocalPattern：local_bash 用 policy.MatchCommandRule（与远程 exec 一致，支持 *），
// local_write/local_edit 用 policy.MatchPathRule（POSIX glob，* 不跨 /，与远程 cp 同一套语义）。
func matchLocalPattern(tool, pattern, subject string) bool {
	if pattern == "*" || pattern == subject {
		return true
	}
	switch tool {
	case "local_bash":
		return policy.MatchCommandRule(pattern, subject)
	default:
		return policy.MatchPathRule(pattern, subject)
	}
}

// extractSubjects 解析工具输入得到需要审批的主体列表。
func extractSubjects(tool string, in map[string]any) []string {
	switch tool {
	case "local_bash":
		cmd, _ := in["command"].(string)
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return nil
		}
		subs, err := policy.ExtractSubCommands(cmd)
		if err != nil || len(subs) == 0 {
			return []string{cmd}
		}
		return subs
	case "local_write", "local_edit":
		p, _ := in["path"].(string)
		p = strings.TrimSpace(p)
		if p == "" {
			return nil
		}
		return []string{p}
	}
	return nil
}

func primaryCommand(tool string, in map[string]any) string {
	switch tool {
	case "local_bash":
		cmd, _ := in["command"].(string)
		return cmd
	case "local_write", "local_edit":
		p, _ := in["path"].(string)
		return p
	}
	return ""
}

// detailOf 给前端展示用的补充内容：local_write 显示前若干内容，local_edit 显示 diff 摘要。
func detailOf(tool string, in map[string]any) string {
	switch tool {
	case "local_write":
		c, _ := in["content"].(string)
		return truncatePreview(c, 800)
	case "local_edit":
		edits, ok := in["edits"].([]any)
		if !ok {
			return ""
		}
		var b strings.Builder
		for i, e := range edits {
			m, _ := e.(map[string]any)
			oldT, _ := m["oldText"].(string)
			newT, _ := m["newText"].(string)
			fmt.Fprintf(&b, "--- edit %d ---\n- %s\n+ %s\n", i+1, truncatePreview(oldT, 200), truncatePreview(newT, 200))
			if b.Len() > 800 {
				b.WriteString("...(truncated)")
				break
			}
		}
		return b.String()
	}
	return ""
}

func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...(truncated)"
}

// defaultPatterns 为每条 subject 生成默认 pattern。
// local_bash: 取第一个 token + " *"；local_write/local_edit: 原 path（用户再编辑加 * 或 **）。
func defaultPatterns(tool string, subjects []string) []string {
	out := make([]string, 0, len(subjects))
	for _, s := range subjects {
		if tool == "local_bash" {
			out = append(out, defaultBashPattern(s))
		} else {
			out = append(out, s)
		}
	}
	return out
}

func defaultBashPattern(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return cmd
	}
	return fields[0] + " *"
}

// patternsFromResponse 取用户最终用作白名单的 pattern 列表。
//   - 优先 EditedItems（用户在 dialog 里手工编辑）；多行按 \n 拆分。
//   - 否则回退到 defaultPatterns。
func patternsFromResponse(tool string, subjects []string, edited []permission.ApprovalItem) []string {
	var out []string
	for _, item := range edited {
		for line := range strings.SplitSeq(item.Command, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				out = append(out, line)
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	return defaultPatterns(tool, subjects)
}
