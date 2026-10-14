package backup_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/model/entity/ssh_agent_source_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/credential_repo"
	"github.com/opskat/opskat/internal/repository/custom_type_repo"
	"github.com/opskat/opskat/internal/repository/forward_repo"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/repository/policy_group_repo"
	"github.com/opskat/opskat/internal/repository/ssh_agent_source_repo"
)

// Export 导出数据
func Export(ctx context.Context, opts *ExportOptions, crypto CredentialCrypto) (*BackupData, error) {
	allAssets, err := asset_repo.Asset().List(ctx, asset_repo.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("导出资产失败: %w", err)
	}
	allGroups, err := group_repo.Group().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("导出分组失败: %w", err)
	}

	// 构建查找映射
	assetMap := make(map[int64]*asset_entity.Asset, len(allAssets))
	for _, a := range allAssets {
		assetMap[a.ID] = a
	}
	groupMap := make(map[int64]*group_entity.Group, len(allGroups))
	for _, g := range allGroups {
		groupMap[g.ID] = g
	}

	// 确定要导出的资产
	var selectedAssets []*asset_entity.Asset
	if len(opts.AssetIDs) > 0 {
		selectedIDs := make(map[int64]bool)
		for _, id := range opts.AssetIDs {
			selectedIDs[id] = true
		}
		// 自动解析依赖
		resolveDependentAssets(selectedIDs, assetMap)
		for _, a := range allAssets {
			if selectedIDs[a.ID] {
				selectedAssets = append(selectedAssets, a)
			}
		}
	} else {
		selectedAssets = allAssets
	}

	// 收集所需的分组（含祖先链）
	neededGroupIDs := make(map[int64]bool)
	for _, a := range selectedAssets {
		if a.GroupID > 0 {
			collectAncestorGroups(a.GroupID, groupMap, neededGroupIDs)
		}
	}
	var selectedGroups []*group_entity.Group
	for _, g := range allGroups {
		if neededGroupIDs[g.ID] {
			selectedGroups = append(selectedGroups, g)
		}
	}
	// 如果导出全部资产，导出全部分组
	if len(opts.AssetIDs) == 0 {
		selectedGroups = allGroups
	}

	data := &BackupData{
		Version:    "1.0",
		ExportedAt: time.Now().Format(time.RFC3339),
		Assets:     selectedAssets,
		Groups:     selectedGroups,
	}
	if opts.Shortcuts != "" {
		data.Shortcuts = json.RawMessage(opts.Shortcuts)
	}
	if opts.CustomThemes != "" {
		data.CustomThemes = json.RawMessage(opts.CustomThemes)
	}

	// 收集选中资产 ID 集合（后续模块使用）
	selectedAssetIDs := make(map[int64]bool, len(selectedAssets))
	for _, a := range selectedAssets {
		selectedAssetIDs[a.ID] = true
	}

	// 收集策略组
	if opts.IncludePolicyGroups {
		pgIDs := collectPolicyGroupIDs(selectedAssets, selectedGroups)
		if len(pgIDs) > 0 {
			ids := make([]int64, 0, len(pgIDs))
			for id := range pgIDs {
				ids = append(ids, id)
			}
			pgs, err := policy_group_repo.PolicyGroup().ListByIDs(ctx, ids)
			if err != nil {
				return nil, fmt.Errorf("导出策略组失败: %w", err)
			}
			data.PolicyGroups = pgs
		}
	}

	// 凭据处理
	if opts.IncludeCredentials && crypto != nil {
		data.IncludesCredentials = true
		creds, err := exportCredentials(ctx, selectedAssets, crypto)
		if err != nil {
			return nil, fmt.Errorf("导出凭据失败: %w", err)
		}
		data.Credentials = creds
		// 解密资产内联密码
		if err := decryptAssetPasswords(ctx, data.Assets, crypto); err != nil {
			return nil, fmt.Errorf("解密资产密码失败: %w", err)
		}
	} else {
		// 不含凭据：清除敏感字段
		if err := stripAssetSecrets(ctx, data.Assets); err != nil {
			return nil, fmt.Errorf("清除资产密钥字段失败: %w", err)
		}
	}

	// 端口转发
	if opts.IncludeForwards {
		forwards, err := exportForwards(ctx, selectedAssetIDs)
		if err != nil {
			return nil, fmt.Errorf("导出端口转发失败: %w", err)
		}
		data.Forwards = forwards
	}

	// SSH Agent 来源定义：只含端点定义，不受凭据秘密包含选项控制。
	// 部分导出（AssetIDs 非空）只含被导出 Agent 认证资产引用的来源，
	// 全量导出包含全部来源（含未使用）。
	agentSources, err := exportAgentSources(ctx, selectedAssets, len(opts.AssetIDs) > 0)
	if err != nil {
		return nil, fmt.Errorf("导出 SSH Agent 来源失败: %w", err)
	}
	data.AgentSources = agentSources

	// 通用资产用到的自定义类型：与 Agent 来源同一约定，部分导出只含被选中资产
	// 引用的类型，全量导出含全部类型（含未使用）。
	customTypes, err := exportCustomTypes(ctx, selectedAssets, len(opts.AssetIDs) > 0)
	if err != nil {
		return nil, fmt.Errorf("导出自定义类型失败: %w", err)
	}
	data.CustomTypes = customTypes

	return data, nil
}

// --- 导出辅助函数 ---

// resolveDependentAssets 递归补全跳板机和 SSH 隧道资产
func resolveDependentAssets(selectedIDs map[int64]bool, assetMap map[int64]*asset_entity.Asset) {
	changed := true
	for changed {
		changed = false
		for id := range selectedIDs {
			a, ok := assetMap[id]
			if !ok {
				continue
			}
			switch {
			case a.IsSSH() && a.Config != "":
				cfg, err := a.GetSSHConfig()
				if err == nil && cfg.JumpHostID > 0 && !selectedIDs[cfg.JumpHostID] {
					selectedIDs[cfg.JumpHostID] = true
					changed = true
				}
			case a.IsDatabase() && a.Config != "":
				cfg, err := a.GetDatabaseConfig()
				if err == nil && cfg.SSHAssetID > 0 && !selectedIDs[cfg.SSHAssetID] {
					selectedIDs[cfg.SSHAssetID] = true
					changed = true
				}
			case a.IsRedis() && a.Config != "":
				cfg, err := a.GetRedisConfig()
				if err == nil && cfg.SSHAssetID > 0 && !selectedIDs[cfg.SSHAssetID] {
					selectedIDs[cfg.SSHAssetID] = true
					changed = true
				}
			}
		}
	}
}

// collectAncestorGroups 收集分组及其所有祖先
func collectAncestorGroups(groupID int64, groupMap map[int64]*group_entity.Group, result map[int64]bool) {
	for groupID > 0 && !result[groupID] {
		result[groupID] = true
		g, ok := groupMap[groupID]
		if !ok {
			break
		}
		groupID = g.ParentID
	}
}

// collectPolicyGroupIDs 从资产和分组中收集用户自定义策略组 ID（ID>0）
func collectPolicyGroupIDs(assets []*asset_entity.Asset, groups []*group_entity.Group) map[int64]bool {
	ids := make(map[int64]bool)
	for _, a := range assets {
		collectPolicyIDs(a.CmdPolicy, ids)
	}
	for _, g := range groups {
		collectPolicyIDs(g.CmdPolicy, ids)
		collectPolicyIDs(g.QryPolicy, ids)
		collectPolicyIDs(g.RdsPolicy, ids)
	}
	return ids
}

// collectPolicyIDs 从策略 JSON 中提取 Groups 字段的用户自定义组 ID
func collectPolicyIDs(policyJSON string, ids map[int64]bool) {
	if policyJSON == "" {
		return
	}
	var p struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal([]byte(policyJSON), &p); err != nil {
		return
	}
	for _, id := range p.Groups {
		if !policy_group_entity.IsBuiltinID(id) {
			if dbID, err := strconv.ParseInt(id, 10, 64); err == nil {
				ids[dbID] = true
			}
		}
	}
}

// exportCredentials 导出关联凭据（解密为明文）
func exportCredentials(ctx context.Context, assets []*asset_entity.Asset, crypto CredentialCrypto) ([]*BackupCredential, error) {
	credIDs := make(map[int64]bool)
	for _, a := range assets {
		if a.IsSSH() && a.Config != "" {
			cfg, err := a.GetSSHConfig()
			if err == nil && cfg.CredentialID > 0 {
				credIDs[cfg.CredentialID] = true
			}
		}
		if a.IsGeneric() && a.Config != "" {
			cfg, err := a.GetGenericConfig()
			if err == nil {
				for _, v := range cfg.Values {
					if v.CredentialID > 0 {
						credIDs[v.CredentialID] = true
					}
				}
			}
		}
	}
	if len(credIDs) == 0 {
		return nil, nil
	}

	var result []*BackupCredential
	for credID := range credIDs {
		cred, err := credential_repo.Credential().Find(ctx, credID)
		if err != nil {
			logger.Default().Warn("credential not found during export", zap.Int64("id", credID), zap.Error(err))
			continue
		}
		bc := &BackupCredential{Credential: *cred}
		if cred.Password != "" {
			plain, err := crypto.Decrypt(cred.Password)
			if err != nil {
				return nil, fmt.Errorf("解密凭据 %s 密码失败: %w", cred.Name, err)
			}
			bc.PlainPassword = plain
			bc.Password = "" // 清除密文
		}
		if cred.PrivateKey != "" {
			plain, err := crypto.Decrypt(cred.PrivateKey)
			if err != nil {
				return nil, fmt.Errorf("解密凭据 %s 私钥失败: %w", cred.Name, err)
			}
			bc.PlainPrivateKey = plain
			bc.PrivateKey = "" // 清除密文
		}
		if cred.Passphrase != "" {
			plain, err := crypto.Decrypt(cred.Passphrase)
			if err != nil {
				return nil, fmt.Errorf("解密凭据 %s passphrase 失败: %w", cred.Name, err)
			}
			bc.PlainPassphrase = plain
			bc.Passphrase = "" // 清除密文
		}
		result = append(result, bc)
	}
	return result, nil
}

// decryptAssetPasswords 解密资产 Config 中的内联密码为明文
func decryptAssetPasswords(ctx context.Context, assets []*asset_entity.Asset, crypto CredentialCrypto) error {
	types := make(map[string]*custom_type_entity.CustomType)
	for _, a := range assets {
		switch {
		case a.IsSSH() && a.Config != "":
			cfg, err := a.GetSSHConfig()
			if err != nil {
				continue
			}
			changed := false
			if cfg.Password != "" {
				plain, err := crypto.Decrypt(cfg.Password)
				if err != nil {
					return fmt.Errorf("解密资产 %s SSH 密码失败: %w", a.Name, err)
				}
				cfg.Password = plain
				changed = true
			}
			if cfg.Proxy != nil && cfg.Proxy.Password != "" {
				plain, err := crypto.Decrypt(cfg.Proxy.Password)
				if err != nil {
					return fmt.Errorf("解密资产 %s 代理密码失败: %w", a.Name, err)
				}
				cfg.Proxy.Password = plain
				changed = true
			}
			if changed {
				if err := a.SetSSHConfig(cfg); err != nil {
					return err
				}
			}
		case a.IsDatabase() && a.Config != "":
			cfg, err := a.GetDatabaseConfig()
			if err != nil {
				continue
			}
			if cfg.Password != "" {
				plain, err := crypto.Decrypt(cfg.Password)
				if err != nil {
					return fmt.Errorf("解密资产 %s 数据库密码失败: %w", a.Name, err)
				}
				cfg.Password = plain
				if err := a.SetDatabaseConfig(cfg); err != nil {
					return err
				}
			}
		case a.IsRedis() && a.Config != "":
			cfg, err := a.GetRedisConfig()
			if err != nil {
				continue
			}
			changed := false
			if cfg.Password != "" {
				plain, err := crypto.Decrypt(cfg.Password)
				if err != nil {
					return fmt.Errorf("解密资产 %s Redis 密码失败: %w", a.Name, err)
				}
				cfg.Password = plain
				changed = true
			}
			if cfg.SentinelPassword != "" {
				plain, err := crypto.Decrypt(cfg.SentinelPassword)
				if err != nil {
					return fmt.Errorf("解密资产 %s Redis 哨兵密码失败: %w", a.Name, err)
				}
				cfg.SentinelPassword = plain
				changed = true
			}
			if changed {
				if err := a.SetRedisConfig(cfg); err != nil {
					return err
				}
			}
		case a.IsGeneric() && a.Config != "":
			if err := decryptGenericSecrets(ctx, a, crypto, types); err != nil {
				return err
			}
		}
	}
	return nil
}

// lookupCustomType 按标识查找自定义类型，在一次导出内缓存结果（同类型的多个资产
// 只查一次）。类型不存在（已被删除但资产未清理等异常情况）时返回 nil, nil，调用方
// 按“无法确定字段是否为密钥”处理，不阻塞整体导出。
func lookupCustomType(ctx context.Context, cache map[string]*custom_type_entity.CustomType, slug string) (*custom_type_entity.CustomType, error) {
	if slug == "" {
		return nil, nil
	}
	if ct, ok := cache[slug]; ok {
		return ct, nil
	}
	ct, err := custom_type_repo.CustomType().FindBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cache[slug] = nil
			return nil, nil
		}
		return nil, err
	}
	cache[slug] = ct
	return ct, nil
}

// decryptGenericSecrets 解密通用资产密钥字段的内联值为明文（导出含凭据时使用）。
// 托管凭据引用（CredentialID > 0）不在这里处理：它们和 SSH 的 cfg.CredentialID
// 走同一套凭据收集 / 重映射机制（exportCredentials / import.go）。
func decryptGenericSecrets(ctx context.Context, a *asset_entity.Asset, crypto CredentialCrypto, cache map[string]*custom_type_entity.CustomType) error {
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return nil //nolint:nilerr // 无法解析的配置沿用既有宽松导出行为，不阻塞整体导出
	}
	ct, err := lookupCustomType(ctx, cache, cfg.CustomType)
	if err != nil {
		return fmt.Errorf("资产 %s 引用的自定义类型 %q: %w", a.Name, cfg.CustomType, err)
	}
	if ct == nil {
		return nil
	}
	changed := false
	for name, v := range cfg.Values {
		f, ok := ct.FieldByName(name)
		if !ok || !f.Secret || v.CredentialID > 0 || v.Value == "" {
			continue
		}
		plain, err := crypto.Decrypt(v.Value)
		if err != nil {
			return fmt.Errorf("解密资产 %s 字段 %s 失败: %w", a.Name, name, err)
		}
		cfg.Values[name] = asset_entity.GenericValue{Value: plain}
		changed = true
	}
	if !changed {
		return nil
	}
	return a.SetGenericConfig(cfg)
}

// stripAssetSecrets 清除资产配置中的敏感字段
func stripAssetSecrets(ctx context.Context, assets []*asset_entity.Asset) error {
	types := make(map[string]*custom_type_entity.CustomType)
	for _, a := range assets {
		switch {
		case a.IsSSH() && a.Config != "":
			cfg, err := a.GetSSHConfig()
			if err != nil {
				continue
			}
			cfg.Password = ""
			cfg.CredentialID = 0
			cfg.PrivateKeys = nil
			if cfg.Proxy != nil {
				cfg.Proxy.Password = ""
			}
			if err := a.SetSSHConfig(cfg); err != nil {
				logger.Default().Warn("strip ssh secrets", zap.Error(err))
			}
		case a.IsDatabase() && a.Config != "":
			cfg, err := a.GetDatabaseConfig()
			if err != nil {
				continue
			}
			cfg.Password = ""
			if err := a.SetDatabaseConfig(cfg); err != nil {
				logger.Default().Warn("strip db secrets", zap.Error(err))
			}
		case a.IsRedis() && a.Config != "":
			cfg, err := a.GetRedisConfig()
			if err != nil {
				continue
			}
			cfg.Password = ""
			cfg.SentinelPassword = ""
			if err := a.SetRedisConfig(cfg); err != nil {
				logger.Default().Warn("strip redis secrets", zap.Error(err))
			}
		case a.IsGeneric() && a.Config != "":
			if err := stripGenericSecrets(ctx, a, types); err != nil {
				return err
			}
		}
	}
	return nil
}

// stripGenericSecrets 清除通用资产密钥字段的值（导出不含凭据时使用）：托管凭据引用
// 与内联密文一并清空，非密钥字段的值原样保留（它们不是敏感数据，且导出没有它们
// 就没有意义，如 Base URL 的 host 字段）。
func stripGenericSecrets(ctx context.Context, a *asset_entity.Asset, cache map[string]*custom_type_entity.CustomType) error {
	cfg, err := a.GetGenericConfig()
	if err != nil {
		return nil //nolint:nilerr // 无法解析的配置沿用既有宽松导出行为，不阻塞整体导出
	}
	ct, err := lookupCustomType(ctx, cache, cfg.CustomType)
	if err != nil {
		return fmt.Errorf("资产 %s 引用的自定义类型 %q: %w", a.Name, cfg.CustomType, err)
	}
	if ct == nil {
		return nil
	}
	changed := false
	for _, f := range ct.Fields {
		if !f.Secret {
			continue
		}
		if v, ok := cfg.Values[f.Name]; ok && (v.Value != "" || v.CredentialID > 0) {
			cfg.Values[f.Name] = asset_entity.GenericValue{}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if err := a.SetGenericConfig(cfg); err != nil {
		logger.Default().Warn("strip generic secrets", zap.Error(err))
	}
	return nil
}

// exportCustomTypes 导出通用资产引用的自定义类型定义。
//
// partial=true 时只导出被选中通用资产引用的类型；partial=false（全量）时导出
// 全部类型，包括当前未被任何资产引用的（与 exportAgentSources 同一约定）。
func exportCustomTypes(ctx context.Context, assets []*asset_entity.Asset, partial bool) ([]*custom_type_entity.CustomType, error) {
	if !partial {
		return custom_type_repo.CustomType().List(ctx)
	}

	// 与密钥解密 / 清除共用 lookupCustomType：同一个类型只查一次，只有"类型不存在"被
	// 容忍（资产引用了已不存在的类型时没有定义可导出），其余查询错误原样返回。
	cache := make(map[string]*custom_type_entity.CustomType)
	var result []*custom_type_entity.CustomType
	for _, a := range assets {
		if !a.IsGeneric() || a.Config == "" {
			continue
		}
		cfg, err := a.GetGenericConfig()
		if err != nil {
			continue
		}
		if _, seen := cache[cfg.CustomType]; seen {
			continue
		}
		ct, err := lookupCustomType(ctx, cache, cfg.CustomType)
		if err != nil {
			return nil, fmt.Errorf("资产 %s 引用的自定义类型 %q: %w", a.Name, cfg.CustomType, err)
		}
		if ct != nil {
			result = append(result, ct)
		}
	}
	return result, nil
}

// exportAgentSources 导出 SSH Agent 来源定义。
//
// partial=true 时只导出被选中 Agent 认证 SSH 资产引用的来源；
// partial=false（全量）时导出全部来源，包括当前未使用的。
// 来源实体只含端点定义，任何情况下都不会导出身份、公钥、签名或运行时 payload。
func exportAgentSources(ctx context.Context, assets []*asset_entity.Asset, partial bool) ([]*ssh_agent_source_entity.SSHAgentSource, error) {
	if !partial {
		return ssh_agent_source_repo.SSHAgentSource().List(ctx)
	}

	// 收集被导出 Agent 认证或 Agent 转发 SSH 资产引用的来源 ID。
	sourceIDs := make(map[int64]bool)
	for _, a := range assets {
		if !a.IsSSH() || a.Config == "" {
			continue
		}
		cfg, err := a.GetSSHConfig()
		if err != nil {
			continue
		}
		if cfg.AuthType == asset_entity.AuthTypeAgent && cfg.AgentSourceID > 0 {
			sourceIDs[cfg.AgentSourceID] = true
		}
		if cfg.AgentForwarding && cfg.AgentForwardSourceID > 0 {
			sourceIDs[cfg.AgentForwardSourceID] = true
		}
	}
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	var result []*ssh_agent_source_entity.SSHAgentSource
	for id := range sourceIDs {
		src, err := ssh_agent_source_repo.SSHAgentSource().Find(ctx, id)
		if err != nil {
			logger.Default().Warn("ssh agent source not found during export", zap.Int64("id", id), zap.Error(err))
			continue
		}
		result = append(result, src)
	}
	return result, nil
}

// exportForwards 导出关联的端口转发配置
func exportForwards(ctx context.Context, assetIDs map[int64]bool) ([]*BackupForward, error) {
	configs, err := forward_repo.Forward().ListConfigs(ctx)
	if err != nil {
		return nil, err
	}
	var result []*BackupForward
	for _, config := range configs {
		if !assetIDs[config.AssetID] {
			continue
		}
		rules, err := forward_repo.Forward().ListRulesByConfigID(ctx, config.ID)
		if err != nil {
			return nil, fmt.Errorf("导出转发规则失败: %w", err)
		}
		result = append(result, &BackupForward{
			ForwardConfig: *config,
			Rules:         rules,
		})
	}
	return result, nil
}
