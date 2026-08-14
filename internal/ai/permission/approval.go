package permission

import (
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/pkg/auditredact"
)

// ApprovalItem 统一审批项，AI 和 opsctl 共用
type ApprovalItem struct {
	Type      string `json:"type"` // "exec", "k8s", "sql", "redis", "mongo", "kafka", "cp", "grant", "delete"
	AssetID   int64  `json:"asset_id"`
	AssetName string `json:"asset_name"`
	GroupID   int64  `json:"group_id,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	Command   string `json:"command"`
	Detail    string `json:"detail,omitempty"`
}

// ApprovalResponse 统一审批响应
type ApprovalResponse struct {
	Decision    string         `json:"decision"`               // "allow", "deny", "allowAll"
	EditedItems []ApprovalItem `json:"edited_items,omitempty"` // grant: 用户可能编辑了 items
}

const (
	// ApprovalKindSingle 是可选择本次允许或保存 grant pattern 的普通命令审批。
	ApprovalKindSingle = "single"
	// ApprovalKindOnce 是没有可复用 grant 契约的普通一次性审批。
	ApprovalKindOnce = "once"
	// ApprovalKindBatch 是聚合命令审批，只允许整批本次放行或拒绝。
	ApprovalKindBatch = "batch"
	// ApprovalKindGrant 是 request_permission 的显式 grant 审批。
	ApprovalKindGrant = "grant"
	// ApprovalKindLocalTool 是可保存会话内 pattern 的本地工具审批。
	ApprovalKindLocalTool = "local_tool"
	// ApprovalKindDelete 删除类审批不可 grant，前端不渲染 rememberMode。
	ApprovalKindDelete = "delete"
	// ApprovalKindExtension 扩展参数没有定义安全、可复用的 grant pattern，只能本次允许。
	ApprovalKindExtension = "extension"
)

// ApprovalDecision 是经过 ParseApprovalResponse 白名单解析后的 typed 决策。
// Deny 是零值，使任何遗漏的 switch/default 都天然 fail-closed。
type ApprovalDecision uint8

const (
	ApprovalDeny ApprovalDecision = iota
	ApprovalAllow
	ApprovalAllowAll
)

// ParsedApprovalResponse 是唯一允许消费者使用的审批响应形态。
type ParsedApprovalResponse struct {
	Decision    ApprovalDecision
	EditedItems []ApprovalItem
}

// ParseApprovalResponse 在 permission seam 一次性校验所有 AI/opsctl 审批响应。
// 字符串必须精确命中白名单；不做 Trim/EqualFold，避免损坏/大小写漂移被解释成授权。
// allowAll 只属于真正支持 pattern grant 的 single/local_tool；delete/batch/extension
// 等普通审批只有 allow。edited_items 仅在 grant 或 allowAll 时有意义，并且每项必须
// 同时带 type 与非空 command，防止半解析/零值条目落成意外的宽授权。
func ParseApprovalResponse(kind string, resp ApprovalResponse, expectedItems ...[]ApprovalItem) (ParsedApprovalResponse, error) {
	parsed := ParsedApprovalResponse{Decision: ApprovalDeny}

	switch resp.Decision {
	case "deny":
		return parsed, nil
	case "allow":
		parsed.Decision = ApprovalAllow
	case "allowAll":
		if kind != ApprovalKindSingle && kind != ApprovalKindLocalTool {
			return ParsedApprovalResponse{Decision: ApprovalDeny},
				fmt.Errorf("approval kind %q does not support allowAll", kind)
		}
		parsed.Decision = ApprovalAllowAll
	default:
		return parsed, fmt.Errorf("invalid approval decision %q", resp.Decision)
	}

	switch kind {
	case ApprovalKindSingle, ApprovalKindOnce, ApprovalKindBatch, ApprovalKindGrant, ApprovalKindLocalTool,
		ApprovalKindDelete, ApprovalKindExtension:
	default:
		return ParsedApprovalResponse{Decision: ApprovalDeny}, fmt.Errorf("invalid approval kind %q", kind)
	}

	mayEdit := parsed.Decision == ApprovalAllowAll ||
		(kind == ApprovalKindGrant && parsed.Decision == ApprovalAllow)
	if len(resp.EditedItems) > 0 && !mayEdit {
		return ParsedApprovalResponse{Decision: ApprovalDeny},
			fmt.Errorf("approval kind %q decision %q does not accept edited_items", kind, resp.Decision)
	}
	if mayEdit {
		for i, item := range resp.EditedItems {
			if strings.TrimSpace(item.Type) == "" {
				return ParsedApprovalResponse{Decision: ApprovalDeny},
					fmt.Errorf("approval edited_items[%d].type is required", i)
			}
			if strings.TrimSpace(item.Command) == "" {
				return ParsedApprovalResponse{Decision: ApprovalDeny},
					fmt.Errorf("approval edited_items[%d].command is required", i)
			}
		}
		if len(resp.EditedItems) > 0 && len(expectedItems) > 0 {
			expected := expectedItems[0]
			if len(resp.EditedItems) != len(expected) {
				return ParsedApprovalResponse{Decision: ApprovalDeny},
					fmt.Errorf("approval edited_items count %d does not match requested item count %d", len(resp.EditedItems), len(expected))
			}
			for i, item := range resp.EditedItems {
				want := expected[i]
				if item.Type != want.Type || item.AssetID != want.AssetID || item.GroupID != want.GroupID {
					return ParsedApprovalResponse{Decision: ApprovalDeny},
						fmt.Errorf("approval edited_items[%d] changes the requested scope", i)
				}
			}
		}
	}
	parsed.EditedItems = resp.EditedItems
	return parsed, nil
}

// ApprovalTypeDelete 删除审批项的类型标签，前端 TypeBadge 按它取图标。
const ApprovalTypeDelete = "delete"

// SafeApprovalItems 在发往 Wails 前投影安全 command/detail 副本：command 按 Result 语义
// （JSON 递归脱敏 / 普通文本），detail 按文本语义脱敏。返回安全副本与是否发生脱敏。
// 后端 pending approval 必须继续持有原始 items（调用方用 safe 只做展示、用 redacted 做
// 响应门禁）；原始秘密永远不会进入安全副本，原始 items 也不会被就地改写。
func SafeApprovalItems(items []ApprovalItem) ([]ApprovalItem, bool) {
	safe := make([]ApprovalItem, len(items))
	redacted := false
	for i, it := range items {
		sc := it
		sc.Command = auditredact.Result(it.Command)
		sc.Detail = auditredact.Text(it.Detail)
		if sc.Command != it.Command || sc.Detail != it.Detail {
			redacted = true
		}
		safe[i] = sc
	}
	return safe, redacted
}

// SafeApprovalDescription projects the free-form reason/description rendered beside
// approval items. It is display/persistence text rather than an editable grant subject,
// so it is redacted without changing the command/detail persistence gate.
func SafeApprovalDescription(description string) string {
	return auditredact.Text(description)
}

// ContainsRedaction 报告这批审批主体是否发生了任何 command/detail 脱敏。
func ContainsRedaction(items []ApprovalItem) bool {
	_, redacted := SafeApprovalItems(items)
	return redacted
}

// CanPersistGrant 决定是否允许把这次响应落成/批准为持久授权（allowAll 或 grant
// edited_items）。redacted 主体在 UI 上没有 remember/allow-all/edit 入口；后端据此拒绝
// 伪造的 allowAll 与 grant/edited_items——<redacted> 不能成为授权 pattern，原始秘密也
// 不能经编辑响应回传或持久化（spec Approval safety）。deny 与 allow-once 仍有效并执行
// 原始主体；grant 没有 allow-once（它总是持久化），redacted 时整单拒绝。
func CanPersistGrant(redacted bool, kind string, parsed ParsedApprovalResponse) bool {
	if !redacted {
		return true
	}
	switch {
	case parsed.Decision == ApprovalAllowAll:
		return false
	case kind == ApprovalKindGrant:
		return false
	case len(parsed.EditedItems) > 0:
		return false
	default:
		return true
	}
}
