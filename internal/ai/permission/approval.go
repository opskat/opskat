package permission

import (
	"fmt"
	"strings"
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
			normalized := make([]ApprovalItem, len(resp.EditedItems))
			changed := false
			for i, item := range resp.EditedItems {
				want := expected[i]
				if item.Type != want.Type || item.AssetID != want.AssetID || item.GroupID != want.GroupID {
					return ParsedApprovalResponse{Decision: ApprovalDeny},
						fmt.Errorf("approval edited_items[%d] changes the requested scope", i)
				}
				normalized[i] = want
				normalized[i].Command = item.Command
				changed = changed || item.Command != want.Command
			}
			// EditedItems is also the origin signal for grant normalization. Old or
			// forged frontends may echo every unchanged item; treat that as no edit so
			// system-generated subjects still receive the stricter system projection.
			if changed {
				parsed.EditedItems = normalized
			}
			return parsed, nil
		}
	}
	parsed.EditedItems = resp.EditedItems
	return parsed, nil
}

// ApprovalTypeDelete 删除审批项的类型标签，前端 TypeBadge 按它取图标。
const ApprovalTypeDelete = "delete"
