package system

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/opskat/opskat/internal/ai/aictx"
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	policyent "github.com/opskat/opskat/internal/model/entity/policy"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/conntest"
	"github.com/opskat/opskat/internal/service/group_svc"
	"github.com/opskat/opskat/internal/service/policy_group_svc"
	"github.com/opskat/opskat/internal/service/testreg"
)

// --- 策略测试 ---

// PolicyTestRequest 策略测试请求
type PolicyTestRequest struct {
	PolicyType string `json:"policyType"` // 前端资产 policyType(ssh/database/redis/k8s/etcd/...);经 ResolvePolicyKind 映射到 policyKind
	PolicyJSON string `json:"policyJSON"` // JSON 编码的策略结构体（当前编辑状态）
	Command    string `json:"command"`    // 待测试的命令/SQL/Redis命令
	AssetID    int64  `json:"assetID"`    // 资产ID（用于解析资产组链）
	GroupID    int64  `json:"groupID"`    // 资产组ID（用于解析父组链）
}

// PolicyTestResult 策略测试结果
type PolicyTestResult struct {
	Decision       string `json:"decision"`       // "allow" | "deny" | "need_confirm"
	MatchedPattern string `json:"matchedPattern"` // 匹配到的规则
	MatchedSource  string `json:"matchedSource"`  // 匹配来源: "" 当前策略, "default" 默认规则, 或组名
	Message        string `json:"message"`        // 可读说明
}

// TestPolicyRule 测试命令/SQL/Redis/K8S/etcd 命令是否匹配当前策略（含资产组继承）
func (s *System) TestPolicyRule(req PolicyTestRequest) (*PolicyTestResult, error) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil, fmt.Errorf("command is empty")
	}

	kind, ok := policy.ResolvePolicyKind(req.PolicyType)
	if !ok {
		return nil, fmt.Errorf("unsupported policy type: %s", req.PolicyType)
	}

	input := policy.PolicyTestInput{
		PolicyKind: kind,
		AssetID:    req.AssetID,
		GroupID:    req.GroupID,
	}
	if req.PolicyJSON != "" {
		current, err := policy.DecodeCurrentPolicy(kind, []byte(req.PolicyJSON))
		if err != nil {
			return nil, fmt.Errorf("invalid %s policy JSON: %w", req.PolicyType, err)
		}
		input.Current = current
	}

	result := policy.TestPolicy(i18n.Ctx(s.ctx, s.Lang()), input, command)

	decision := "need_confirm"
	switch result.Decision {
	case aictx.Allow:
		decision = "allow"
	case aictx.Deny:
		decision = "deny"
	}

	return &PolicyTestResult{
		Decision:       decision,
		MatchedPattern: result.MatchedPattern,
		MatchedSource:  result.MatchedSource,
		Message:        result.Message,
	}, nil
}

// TestAssetConnection 测试一份未保存的资产配置(资产表单「测试连接」)。
// testID 配合 CancelTest 中断;assetType 经 conntest 注册表分发到对应 binder 的 tester。
// 共享信封(i18n ctx + 10s 超时 + testreg 取消)在此统一施加,各 tester 只做解析/解析凭据/拨号。
func (s *System) TestAssetConnection(testID, assetType, configJSON, plainPassword string) error {
	fn, ok := conntest.Lookup(assetType)
	if !ok {
		return fmt.Errorf("unsupported asset type: %s", assetType)
	}
	parent, cancel := context.WithTimeout(i18n.Ctx(s.ctx, s.Lang()), 10*time.Second)
	defer cancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()
	return fn(ctx, configJSON, plainPassword)
}

// GetDefaultPolicy 获取指定资产类型的默认策略 JSON
func (s *System) GetDefaultPolicy(assetType string) (string, error) {
	p, ok := policyent.GetDefaultPolicyOf(assetType)
	if !ok {
		return "", fmt.Errorf("unsupported asset type: %s", assetType)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// --- 权限组管理 ---

// ListPolicyGroups 列出权限组（内置 + 自定义）
func (s *System) ListPolicyGroups(policyType string) ([]*policy_group_entity.PolicyGroupItem, error) {
	return policy_group_svc.PolicyGroup().List(i18n.Ctx(s.ctx, s.Lang()), policyType)
}

// CreatePolicyGroup 创建自定义权限组
func (s *System) CreatePolicyGroup(pg policy_group_entity.PolicyGroup) (*policy_group_entity.PolicyGroup, error) {
	if err := policy_group_svc.PolicyGroup().Create(i18n.Ctx(s.ctx, s.Lang()), &pg); err != nil {
		return nil, err
	}
	return &pg, nil
}

// UpdatePolicyGroup 更新自定义权限组
func (s *System) UpdatePolicyGroup(pg policy_group_entity.PolicyGroup) error {
	return policy_group_svc.PolicyGroup().Update(i18n.Ctx(s.ctx, s.Lang()), &pg)
}

// DeletePolicyGroup 删除自定义权限组
func (s *System) DeletePolicyGroup(id string) error {
	return policy_group_svc.PolicyGroup().Delete(i18n.Ctx(s.ctx, s.Lang()), id)
}

// CopyPolicyGroup 复制权限组（内置或自定义）
func (s *System) CopyPolicyGroup(id string, name string) (*policy_group_entity.PolicyGroup, error) {
	return policy_group_svc.PolicyGroup().Copy(i18n.Ctx(s.ctx, s.Lang()), id, name)
}

// --- 资产操作 ---

// GetAsset 获取资产详情
func (s *System) GetAsset(id int64) (*asset_entity.Asset, error) {
	return asset_svc.Asset().Get(i18n.Ctx(s.ctx, s.Lang()), id)
}

// ListAssets 列出资产
func (s *System) ListAssets(assetType string, groupID int64) ([]*asset_entity.Asset, error) {
	return asset_svc.Asset().List(i18n.Ctx(s.ctx, s.Lang()), assetType, groupID)
}

// CreateAsset 创建资产
func (s *System) CreateAsset(asset *asset_entity.Asset) error {
	return asset_svc.Asset().Create(i18n.Ctx(s.ctx, s.Lang()), asset)
}

// UpdateAsset 更新资产
func (s *System) UpdateAsset(asset *asset_entity.Asset) error {
	return asset_svc.Asset().Update(i18n.Ctx(s.ctx, s.Lang()), asset)
}

// DeleteAsset 删除资产
func (s *System) DeleteAsset(id int64) error {
	return asset_svc.Asset().Delete(i18n.Ctx(s.ctx, s.Lang()), id)
}

// MoveAsset 移动资产排序（up/down/top）
func (s *System) MoveAsset(id int64, direction string) error {
	return asset_svc.Asset().Move(i18n.Ctx(s.ctx, s.Lang()), id, direction)
}

// MoveGroup 移动分组排序（up/down/top）
func (s *System) MoveGroup(id int64, direction string) error {
	return group_svc.Group().Move(i18n.Ctx(s.ctx, s.Lang()), id, direction)
}

// ReorderAsset 把资产拖到 targetGroupID 内 beforeID 之前；beforeID==0 表示追加末尾。
// 跨分组时同步 GroupID。
func (s *System) ReorderAsset(id, targetGroupID, beforeID int64) error {
	return asset_svc.Asset().Reorder(i18n.Ctx(s.ctx, s.Lang()), id, targetGroupID, beforeID)
}

// ReorderGroup 把分组拖到 targetParentID 下 beforeID 之前；beforeID==0 表示追加末尾。
// 跨父级时同步 ParentID。禁止拖到自身或自己的子孙下。
func (s *System) ReorderGroup(id, targetParentID, beforeID int64) error {
	return group_svc.Group().Reorder(i18n.Ctx(s.ctx, s.Lang()), id, targetParentID, beforeID)
}

// --- 分组操作 ---

// ListGroups 列出所有分组
func (s *System) ListGroups() ([]*group_entity.Group, error) {
	return group_svc.Group().List(i18n.Ctx(s.ctx, s.Lang()))
}

// GetGroup 获取单个分组详情
func (s *System) GetGroup(id int64) (*group_entity.Group, error) {
	return group_svc.Group().Get(i18n.Ctx(s.ctx, s.Lang()), id)
}

// CreateGroup 创建分组
func (s *System) CreateGroup(group *group_entity.Group) error {
	return group_svc.Group().Create(i18n.Ctx(s.ctx, s.Lang()), group)
}

// RenameGroup 重命名分组
func (s *System) RenameGroup(id int64, name string) error {
	return group_svc.Group().Rename(i18n.Ctx(s.ctx, s.Lang()), id, name)
}

// UpdateGroup 更新分组
func (s *System) UpdateGroup(group *group_entity.Group) error {
	return group_svc.Group().Update(i18n.Ctx(s.ctx, s.Lang()), group)
}

// DeleteGroup 删除分组
// deleteAssets: true 删除分组下的资产，false 移动到未分组
func (s *System) DeleteGroup(id int64, deleteAssets bool) error {
	return group_svc.Group().Delete(i18n.Ctx(s.ctx, s.Lang()), id, deleteAssets)
}

// SelectSQLiteFile 打开原生文件对话框，返回选中的 SQLite 文件绝对路径。
// 取消选择返回空字符串（不算错误）。前端用于资产创建/编辑时的"浏览…"按钮。
func (s *System) SelectSQLiteFile() (string, error) {
	path, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择 SQLite 数据库文件",
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "SQLite (*.db, *.sqlite, *.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3"},
			{DisplayName: "All Files", Pattern: "*"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("打开文件对话框失败: %w", err)
	}
	return path, nil
}
