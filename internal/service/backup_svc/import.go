package backup_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/sshagent"
	"github.com/opskat/opskat/pkg/extension"
)

// Import 导入备份数据
func Import(ctx context.Context, data *BackupData, opts *ImportOptions, crypto CredentialCrypto) (*ImportResult, error) {
	result := &ImportResult{}
	isReplace := opts.Mode != "merge"

	// Agent 预检在写入前执行：重复来源 ID、缺失来源引用和畸形 Agent 字段
	// 都在任何写入（含 replace 模式的清空）之前拒绝。
	if opts.ImportAssets {
		if err := precheckAgentSources(data); err != nil {
			return nil, err
		}
	}

	err := db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 策略组
		pgIDMap := make(map[int64]int64)
		if opts.ImportPolicyGroups && len(data.PolicyGroups) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM policy_groups").Error; err != nil {
					return fmt.Errorf("清除策略组失败: %w", err)
				}
			}
			for _, pg := range data.PolicyGroups {
				oldID := pg.ID
				pg.ID = 0
				if err := tx.Create(pg).Error; err != nil {
					return fmt.Errorf("创建策略组 %s 失败: %w", pg.Name, err)
				}
				pgIDMap[oldID] = pg.ID
				result.PolicyGroupsImported++
			}
		}

		// 2. SSH Agent 来源（先建来源，再建 Agent 认证资产）
		agentSourceIDMap := make(map[int64]int64)
		if opts.ImportAssets && len(data.AgentSources) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM ssh_agent_sources").Error; err != nil {
					return fmt.Errorf("清除 SSH Agent 来源失败: %w", err)
				}
			}
			for _, src := range data.AgentSources {
				oldID := src.ID
				src.ID = 0
				if err := tx.Create(src).Error; err != nil {
					return fmt.Errorf("创建 SSH Agent 来源 %s 失败: %w", src.Name, err)
				}
				agentSourceIDMap[oldID] = src.ID
				result.AgentSourcesImported++
			}
		}

		// 3. 凭据
		credIDMap := make(map[int64]int64)
		if opts.ImportCredentials && len(data.Credentials) > 0 && crypto != nil {
			if isReplace {
				if err := tx.Exec("DELETE FROM credentials").Error; err != nil {
					return fmt.Errorf("清除凭据失败: %w", err)
				}
			}
			for _, bc := range data.Credentials {
				oldID := bc.ID
				cred := bc.Credential
				cred.ID = 0
				// 重新加密
				if bc.PlainPassword != "" {
					encrypted, err := crypto.Encrypt(bc.PlainPassword)
					if err != nil {
						return fmt.Errorf("加密密码失败: %w", err)
					}
					cred.Password = encrypted
				}
				if bc.PlainPrivateKey != "" {
					encrypted, err := crypto.Encrypt(bc.PlainPrivateKey)
					if err != nil {
						return fmt.Errorf("加密私钥失败: %w", err)
					}
					cred.PrivateKey = encrypted
				}
				if bc.PlainPassphrase != "" {
					encrypted, err := crypto.Encrypt(bc.PlainPassphrase)
					if err != nil {
						return fmt.Errorf("加密 passphrase 失败: %w", err)
					}
					cred.Passphrase = encrypted
				}
				if err := tx.Create(&cred).Error; err != nil {
					return fmt.Errorf("创建凭据 %s 失败: %w", cred.Name, err)
				}
				credIDMap[oldID] = cred.ID
				result.CredentialsImported++
			}
		}

		// 4. 分组
		groupIDMap := make(map[int64]int64)
		if opts.ImportAssets && len(data.Groups) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM groups").Error; err != nil {
					return fmt.Errorf("清除分组失败: %w", err)
				}
			}
			sortedGroups := sortGroups(data.Groups)
			for _, g := range sortedGroups {
				oldID := g.ID
				g.ID = 0
				if g.ParentID > 0 {
					if newID, ok := groupIDMap[g.ParentID]; ok {
						g.ParentID = newID
					}
				}
				// 回填策略组引用
				remapGroupPolicyGroupIDs(g, pgIDMap)
				if err := tx.Create(g).Error; err != nil {
					return fmt.Errorf("创建分组 %s 失败: %w", g.Name, err)
				}
				groupIDMap[oldID] = g.ID
				result.GroupsImported++
			}
		}

		// 5. 资产
		assetIDMap := make(map[int64]int64)
		if opts.ImportAssets && len(data.Assets) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM assets").Error; err != nil {
					return fmt.Errorf("清除资产失败: %w", err)
				}
			}

			for _, a := range data.Assets {
				oldID := a.ID
				a.ID = 0
				if a.GroupID > 0 {
					if newID, ok := groupIDMap[a.GroupID]; ok {
						a.GroupID = newID
					}
				}
				// 回填策略组引用
				remapAssetPolicyGroupIDs(a, pgIDMap)
				// 处理 Config 中的凭据、Agent 来源引用与密码重加密
				switch {
				case a.IsSSH() && a.Config != "":
					cfg, err := a.GetSSHConfig()
					if err == nil {
						// 回填 CredentialID
						if cfg.CredentialID > 0 {
							if newID, ok := credIDMap[cfg.CredentialID]; ok {
								cfg.CredentialID = newID
							} else if !opts.ImportCredentials {
								cfg.CredentialID = 0
							}
						}
						// Agent 来源引用重映射：旧来源 ID → 新来源 ID（预检保证可映射）
						if cfg.AuthType == asset_entity.AuthTypeAgent && cfg.AgentSourceID > 0 {
							if newID, ok := agentSourceIDMap[cfg.AgentSourceID]; ok {
								cfg.AgentSourceID = newID
							}
						}
						if cfg.AgentForwarding && cfg.AgentForwardSourceID > 0 {
							if newID, ok := agentSourceIDMap[cfg.AgentForwardSourceID]; ok {
								cfg.AgentForwardSourceID = newID
							}
						}
						// 重新加密内联密码
						if data.IncludesCredentials && cfg.Password != "" && crypto != nil {
							encrypted, encErr := crypto.Encrypt(cfg.Password)
							if encErr != nil {
								logger.Default().Warn("re-encrypt ssh password", zap.Error(encErr))
							} else {
								cfg.Password = encrypted
							}
						}
						// 代理密码
						if data.IncludesCredentials && cfg.Proxy != nil && cfg.Proxy.Password != "" && crypto != nil {
							encrypted, encErr := crypto.Encrypt(cfg.Proxy.Password)
							if encErr != nil {
								logger.Default().Warn("re-encrypt proxy password", zap.Error(encErr))
							} else {
								cfg.Proxy.Password = encrypted
							}
						}
						if err := a.SetSSHConfig(cfg); err != nil {
							logger.Default().Warn("set ssh config in import", zap.Error(err))
						}
					}
				case a.IsDatabase() && a.Config != "":
					cfg, err := a.GetDatabaseConfig()
					if err == nil {
						if data.IncludesCredentials && cfg.Password != "" && crypto != nil {
							encrypted, encErr := crypto.Encrypt(cfg.Password)
							if encErr != nil {
								logger.Default().Warn("re-encrypt db password", zap.Error(encErr))
							} else {
								cfg.Password = encrypted
							}
						}
						if err := a.SetDatabaseConfig(cfg); err != nil {
							logger.Default().Warn("set db config in import", zap.Error(err))
						}
					}
				case a.IsRedis() && a.Config != "":
					cfg, err := a.GetRedisConfig()
					if err == nil {
						if data.IncludesCredentials && cfg.Password != "" && crypto != nil {
							encrypted, encErr := crypto.Encrypt(cfg.Password)
							if encErr != nil {
								logger.Default().Warn("re-encrypt redis password", zap.Error(encErr))
							} else {
								cfg.Password = encrypted
							}
						}
						if data.IncludesCredentials && cfg.SentinelPassword != "" && crypto != nil {
							encrypted, encErr := crypto.Encrypt(cfg.SentinelPassword)
							if encErr != nil {
								logger.Default().Warn("re-encrypt redis sentinel password", zap.Error(encErr))
							} else {
								cfg.SentinelPassword = encrypted
							}
						}
						if err := a.SetRedisConfig(cfg); err != nil {
							logger.Default().Warn("set redis config in import", zap.Error(err))
						}
					}
				}

				if err := tx.Create(a).Error; err != nil {
					return fmt.Errorf("创建资产 %s 失败: %w", a.Name, err)
				}
				assetIDMap[oldID] = a.ID
				result.AssetsImported++
			}

			// 回填所有资产的跳板机、SSH 隧道与代理链 SSH 引用（映射不到时显式清零）
			for _, a := range data.Assets {
				if remapAssetSSHRefs(ctx, a, assetIDMap) {
					if err := tx.Save(a).Error; err != nil {
						return fmt.Errorf("更新资产引用失败: %w", err)
					}
				}
			}
		}

		// 6. 端口转发
		if opts.ImportForwards && len(data.Forwards) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM forward_rules").Error; err != nil {
					return fmt.Errorf("清除转发规则失败: %w", err)
				}
				if err := tx.Exec("DELETE FROM forward_configs").Error; err != nil {
					return fmt.Errorf("清除转发配置失败: %w", err)
				}
			}
			for _, bf := range data.Forwards {
				newAssetID, ok := assetIDMap[bf.AssetID]
				if !ok {
					// 合并模式下资产可能已存在，尝试按名字匹配
					continue
				}
				config := bf.ForwardConfig
				config.ID = 0
				config.AssetID = newAssetID
				if err := tx.Create(&config).Error; err != nil {
					return fmt.Errorf("创建转发配置 %s 失败: %w", config.Name, err)
				}
				for _, rule := range bf.Rules {
					rule.ID = 0
					rule.ConfigID = config.ID
					if err := tx.Create(rule).Error; err != nil {
						return fmt.Errorf("创建转发规则失败: %w", err)
					}
				}
				result.ForwardsImported++
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// 6. 客户端设置透传
	if opts.ImportShortcuts && len(data.Shortcuts) > 0 {
		result.Shortcuts = string(data.Shortcuts)
	}
	if opts.ImportThemes && len(data.CustomThemes) > 0 {
		result.CustomThemes = string(data.CustomThemes)
	}

	return result, nil
}

// precheckAgentSources 在写入前拒绝来源/资产中的 Agent 引用问题：
//   - 重复来源 ID；
//   - 结构非法的来源端点（畸形 Agent 字段；平台不兼容但结构合法的来源保留为 unsupported）；
//   - Agent 认证 SSH 资产引用了备份中不存在的来源；
//   - Agent 认证 SSH 资产的 Agent 契约畸形（指纹非法、非 Agent 认证携带来源字段等）。
//
// 只校验备份数据本身，不触碰数据库；任一项失败即整体拒绝导入。
func precheckAgentSources(data *BackupData) error {
	seen := make(map[int64]bool, len(data.AgentSources))
	for _, src := range data.AgentSources {
		if seen[src.ID] {
			return fmt.Errorf("备份包含重复的 Agent 来源 ID: %d", src.ID)
		}
		seen[src.ID] = true
		s := sshagent.Source{Type: sshagent.EndpointType(src.EndpointType), Value: src.Endpoint}
		if err := s.Validate(); err != nil {
			return fmt.Errorf("来源 %s 字段畸形: %w", src.Name, err)
		}
	}
	for _, a := range data.Assets {
		if !a.IsSSH() || a.Config == "" {
			continue
		}
		cfg, err := a.GetSSHConfig()
		if err != nil {
			// 无法解析的配置保持既有宽松导入行为，不做 Agent 预检
			continue
		}
		if cfg.AuthType != asset_entity.AuthTypeAgent && cfg.AgentSourceID == 0 && cfg.AgentKeyFingerprint == "" &&
			!cfg.AgentForwarding && cfg.AgentForwardSourceID == 0 {
			continue
		}
		if cfg.AuthType == asset_entity.AuthTypeAgent && cfg.AgentSourceID > 0 && !seen[cfg.AgentSourceID] {
			return fmt.Errorf("资产 %s 引用的 Agent 来源 %d 不在备份中", a.Name, cfg.AgentSourceID)
		}
		if cfg.AgentForwarding && cfg.AgentForwardSourceID > 0 && !seen[cfg.AgentForwardSourceID] {
			return fmt.Errorf("资产 %s 引用的 Agent 转发来源 %d 不在备份中", a.Name, cfg.AgentForwardSourceID)
		}
		if err := a.Validate(); err != nil {
			return fmt.Errorf("资产 %s 的 Agent 字段畸形: %w", a.Name, err)
		}
	}
	return nil
}

// --- 导入辅助函数 ---

// remapGroupPolicyGroupIDs 回填分组中策略的 Groups 引用
func remapGroupPolicyGroupIDs(g *group_entity.Group, pgIDMap map[int64]int64) {
	g.CmdPolicy = remapPolicyGroupRefs(g.CmdPolicy, pgIDMap)
	g.QryPolicy = remapPolicyGroupRefs(g.QryPolicy, pgIDMap)
	g.RdsPolicy = remapPolicyGroupRefs(g.RdsPolicy, pgIDMap)
}

// remapAssetPolicyGroupIDs 回填资产中策略的 Groups 引用
func remapAssetPolicyGroupIDs(a *asset_entity.Asset, pgIDMap map[int64]int64) {
	a.CmdPolicy = remapPolicyGroupRefs(a.CmdPolicy, pgIDMap)
}

// configSSHRefKeys 是资产 Config JSON 顶层存放单条 SSH 资产 ID 引用的键。
var configSSHRefKeys = []string{
	"jump_host_id",
	"ssh_asset_id",
	"file_ssh_asset_id",
}

// collectAssetSSHRefIDs 收集单个资产在 SSHTunnelID 列、Config 顶层单跳字段、
// proxy_chain 与扩展宿主连接配置（__opskat_connection.proxyChain）中引用的全部 SSH 资产 ID。
func collectAssetSSHRefIDs(a *asset_entity.Asset) []int64 {
	if a == nil {
		return nil
	}
	var ids []int64
	if a.SSHTunnelID > 0 {
		ids = append(ids, a.SSHTunnelID)
	}
	if a.Config == "" {
		return ids
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(a.Config), &raw); err != nil {
		return ids
	}
	for _, key := range configSSHRefKeys {
		if valRaw, ok := raw[key]; ok {
			var refID int64
			if err := json.Unmarshal(valRaw, &refID); err == nil && refID > 0 {
				ids = append(ids, refID)
			}
		}
	}
	if chainRaw, ok := raw["proxy_chain"]; ok {
		ids = append(ids, collectProxyChainSSHRefIDs(chainRaw)...)
	}
	if extRaw, ok := raw[extension.HostConnectionConfigKey]; ok {
		var extConn map[string]json.RawMessage
		if err := json.Unmarshal(extRaw, &extConn); err == nil {
			if chainRaw, hasChain := extConn["proxyChain"]; hasChain {
				ids = append(ids, collectProxyChainSSHRefIDs(chainRaw)...)
			}
		}
	}
	return ids
}

func collectProxyChainSSHRefIDs(chainRaw json.RawMessage) []int64 {
	var chain asset_entity.ProxyChainConfig
	if err := json.Unmarshal(chainRaw, &chain); err != nil {
		return nil
	}
	var ids []int64
	for _, layer := range chain.Layers {
		if layer.Type == asset_entity.ProxyChainLayerSSH && layer.SSHAssetID > 0 {
			ids = append(ids, layer.SSHAssetID)
		}
	}
	return ids
}

// remapAssetSSHRefs 将资产的 SSHTunnelID 列及 Config 中的 SSH 资产引用重映射到新 ID；
// 无法映射的引用显式清零，避免悬空或串号到目标库中的其它资产。
func remapAssetSSHRefs(ctx context.Context, a *asset_entity.Asset, assetIDMap map[int64]int64) bool {
	changed := false
	if a.SSHTunnelID > 0 {
		oldRefID := a.SSHTunnelID
		newRefID, ok := assetIDMap[oldRefID]
		if !ok {
			logger.Ctx(ctx).Warn("unmapped ssh_tunnel_id cleared on import",
				zap.String("asset", a.Name), zap.Int64("oldTunnelID", oldRefID))
			newRefID = 0
		}
		if a.SSHTunnelID != newRefID {
			a.SSHTunnelID = newRefID
			changed = true
		}
	}
	if a.Config != "" {
		if newConfig, cfgChanged := remapConfigSSHRefs(ctx, a.Name, a.Config, assetIDMap); cfgChanged {
			a.Config = newConfig
			changed = true
		}
	}
	return changed
}

func remapConfigSSHRefs(ctx context.Context, assetName, configJSON string, assetIDMap map[int64]int64) (string, bool) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(configJSON), &raw); err != nil {
		return configJSON, false
	}
	changed := false
	for _, key := range configSSHRefKeys {
		valRaw, ok := raw[key]
		if !ok {
			continue
		}
		var oldRefID int64
		if err := json.Unmarshal(valRaw, &oldRefID); err != nil || oldRefID <= 0 {
			continue
		}
		if newRefID, mapped := assetIDMap[oldRefID]; mapped {
			if newRefID != oldRefID {
				if encoded, err := json.Marshal(newRefID); err == nil {
					raw[key] = encoded
					changed = true
				}
			}
		} else {
			logger.Ctx(ctx).Warn("unmapped ssh ref cleared on import",
				zap.String("asset", assetName), zap.String("field", key), zap.Int64("oldRefID", oldRefID))
			delete(raw, key)
			changed = true
		}
	}
	if chainRaw, ok := raw["proxy_chain"]; ok {
		if newChainRaw, chainChanged := remapProxyChainSSHRefs(ctx, assetName, chainRaw, assetIDMap); chainChanged {
			raw["proxy_chain"] = newChainRaw
			changed = true
		}
	}
	if extRaw, ok := raw[extension.HostConnectionConfigKey]; ok {
		var extConn map[string]json.RawMessage
		if err := json.Unmarshal(extRaw, &extConn); err == nil {
			if chainRaw, hasChain := extConn["proxyChain"]; hasChain {
				if newChainRaw, chainChanged := remapProxyChainSSHRefs(ctx, assetName, chainRaw, assetIDMap); chainChanged {
					extConn["proxyChain"] = newChainRaw
					if newExtRaw, err := json.Marshal(extConn); err == nil {
						raw[extension.HostConnectionConfigKey] = newExtRaw
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return configJSON, false
	}
	out, err := json.Marshal(raw)
	if err != nil {
		return configJSON, false
	}
	return string(out), true
}

func remapProxyChainSSHRefs(ctx context.Context, assetName string, chainRaw json.RawMessage, assetIDMap map[int64]int64) (json.RawMessage, bool) {
	var chainObj map[string]json.RawMessage
	if err := json.Unmarshal(chainRaw, &chainObj); err != nil {
		return chainRaw, false
	}
	layersRaw, ok := chainObj["layers"]
	if !ok {
		return chainRaw, false
	}
	var layers []map[string]json.RawMessage
	if err := json.Unmarshal(layersRaw, &layers); err != nil {
		return chainRaw, false
	}
	changed := false
	for _, layer := range layers {
		var layerType string
		if typeRaw, ok := layer["type"]; !ok || json.Unmarshal(typeRaw, &layerType) != nil || layerType != asset_entity.ProxyChainLayerSSH {
			continue
		}
		sshRaw, ok := layer["ssh_asset_id"]
		if !ok {
			continue
		}
		var oldRefID int64
		if err := json.Unmarshal(sshRaw, &oldRefID); err != nil || oldRefID <= 0 {
			continue
		}
		if newRefID, mapped := assetIDMap[oldRefID]; mapped {
			if newRefID != oldRefID {
				if encoded, err := json.Marshal(newRefID); err == nil {
					layer["ssh_asset_id"] = encoded
					changed = true
				}
				var layerID string
				if idRaw, hasID := layer["id"]; hasID && json.Unmarshal(idRaw, &layerID) == nil && layerID == fmt.Sprintf("legacy-ssh-%d", oldRefID) {
					if encodedID, err := json.Marshal(fmt.Sprintf("legacy-ssh-%d", newRefID)); err == nil {
						layer["id"] = encodedID
					}
				}
			}
		} else {
			logger.Ctx(ctx).Warn("unmapped proxy_chain ssh_asset_id cleared on import",
				zap.String("asset", assetName), zap.Int64("oldRefID", oldRefID))
			delete(layer, "ssh_asset_id")
			changed = true
		}
	}
	if !changed {
		return chainRaw, false
	}
	newLayersRaw, err := json.Marshal(layers)
	if err != nil {
		return chainRaw, false
	}
	chainObj["layers"] = newLayersRaw
	newChainRaw, err := json.Marshal(chainObj)
	if err != nil {
		return chainRaw, false
	}
	return newChainRaw, true
}

// remapPolicyGroupRefs 替换策略 JSON 中的 groups ID 引用
func remapPolicyGroupRefs(policyJSON string, pgIDMap map[int64]int64) string {
	if policyJSON == "" || len(pgIDMap) == 0 {
		return policyJSON
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(policyJSON), &raw); err != nil {
		return policyJSON
	}
	groupsRaw, ok := raw["groups"]
	if !ok {
		return policyJSON
	}
	var groups []string
	if err := json.Unmarshal(groupsRaw, &groups); err != nil {
		return policyJSON
	}
	changed := false
	for i, id := range groups {
		if policy_group_entity.IsBuiltinID(id) {
			continue
		}
		oldID, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			continue
		}
		if newID, ok := pgIDMap[oldID]; ok {
			groups[i] = strconv.FormatInt(newID, 10)
			changed = true
		}
	}
	if !changed {
		return policyJSON
	}
	newGroupsRaw, err := json.Marshal(groups)
	if err != nil {
		return policyJSON
	}
	raw["groups"] = newGroupsRaw
	result, err := json.Marshal(raw)
	if err != nil {
		return policyJSON
	}
	return string(result)
}

// sortGroups 拓扑排序分组，确保父分组在子分组之前
func sortGroups(groups []*group_entity.Group) []*group_entity.Group {
	sorted := make([]*group_entity.Group, 0, len(groups))
	added := make(map[int64]bool)

	for len(sorted) < len(groups) {
		progress := false
		for _, g := range groups {
			if added[g.ID] {
				continue
			}
			if g.ParentID == 0 || added[g.ParentID] {
				sorted = append(sorted, g)
				added[g.ID] = true
				progress = true
			}
		}
		if !progress {
			for _, g := range groups {
				if !added[g.ID] {
					sorted = append(sorted, g)
				}
			}
			break
		}
	}
	return sorted
}
