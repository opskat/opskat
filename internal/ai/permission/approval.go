package permission

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

// ApprovalKindDelete 删除类审批的 kind。与 "single" 的区别是前端**不渲染** rememberMode
// （"全部允许"→写 grant）：删除不可 grant，UI 上出现那个按钮就等于把后端的约束架空。
const ApprovalKindDelete = "delete"

// ApprovalTypeDelete 删除审批项的类型标签，前端 TypeBadge 按它取图标。
const ApprovalTypeDelete = "delete"
