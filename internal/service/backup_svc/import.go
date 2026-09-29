package backup_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/group_entity"
	"github.com/opskat/opskat/internal/model/entity/policy_group_entity"
	"github.com/opskat/opskat/internal/sshagent"
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

		// 4. 自定义类型。以标识（slug）而非自增 ID 跨库引用（Design decision 13），
		// 不需要像其他实体那样维护 ID 重映射表：replace 模式用备份替换本地全部类型；
		// merge 模式标识已存在时保留本地版本，备份中的资产按标识绑定到本地版本
		// （因此出现的缺值按“新增必填字段”的规则展示，见 custom_type_svc.ResolveAsset）。
		if opts.ImportAssets && len(data.CustomTypes) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM custom_types").Error; err != nil {
					return fmt.Errorf("清除自定义类型失败: %w", err)
				}
			}
			for _, ct := range data.CustomTypes {
				if !isReplace {
					var existing custom_type_entity.CustomType
					err := tx.Where("slug = ?", ct.Slug).First(&existing).Error
					switch {
					case err == nil:
						continue // 合并模式：标识已存在，保留本地版本
					case !errors.Is(err, gorm.ErrRecordNotFound):
						return fmt.Errorf("检查自定义类型 %s 是否存在失败: %w", ct.Slug, err)
					}
				}
				newCT := *ct
				newCT.ID = 0
				if err := tx.Create(&newCT).Error; err != nil {
					return fmt.Errorf("创建自定义类型 %s 失败: %w", ct.Slug, err)
				}
				result.CustomTypesImported++
			}
		}
		// 通用资产字段值的密钥重加密要按备份导出时的字段结构判定哪些字段是密钥
		// （见 export.go 的 decryptGenericSecrets/stripGenericSecrets 同一约定）：
		// 必须用 data.CustomTypes 里随备份带的定义，而不是本地（可能已被用户改过
		// 结构，或合并模式下刻意保留的）版本。
		customTypesBySlug := make(map[string]*custom_type_entity.CustomType, len(data.CustomTypes))
		for _, ct := range data.CustomTypes {
			customTypesBySlug[ct.Slug] = ct
		}

		// 5. 分组
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

		// 6. 资产
		assetIDMap := make(map[int64]int64)
		if opts.ImportAssets && len(data.Assets) > 0 {
			if isReplace {
				if err := tx.Exec("DELETE FROM assets").Error; err != nil {
					return fmt.Errorf("清除资产失败: %w", err)
				}
			}

			type deferredRef struct {
				newAssetID int64
				oldRefID   int64
				refType    string // "jump_host" | "ssh_tunnel"
			}
			var deferredRefs []deferredRef

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
				// 处理 Config 中的引用
				var oldJumpHostID, oldSSHAssetID int64
				switch {
				case a.IsSSH() && a.Config != "":
					cfg, err := a.GetSSHConfig()
					if err == nil {
						if cfg.JumpHostID > 0 {
							oldJumpHostID = cfg.JumpHostID
							cfg.JumpHostID = 0
						}
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
						if cfg.SSHAssetID > 0 {
							oldSSHAssetID = cfg.SSHAssetID
							cfg.SSHAssetID = 0
						}
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
						if cfg.SSHAssetID > 0 {
							oldSSHAssetID = cfg.SSHAssetID
							cfg.SSHAssetID = 0
						}
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
				case a.IsGeneric() && a.Config != "":
					cfg, err := a.GetGenericConfig()
					if err == nil {
						ct := customTypesBySlug[cfg.CustomType]
						for name, v := range cfg.Values {
							if v.CredentialID > 0 {
								// 托管凭据引用重映射：与 SSH 的 cfg.CredentialID 同一套机制。
								if newID, ok := credIDMap[v.CredentialID]; ok {
									v.CredentialID = newID
								} else if !opts.ImportCredentials {
									v.CredentialID = 0
								}
								cfg.Values[name] = v
								continue
							}
							if ct == nil {
								continue
							}
							f, ok := ct.FieldByName(name)
							if !ok || !f.Secret {
								continue
							}
							// 重新加密内联密钥字段值（备份里已解密为明文，见 export.go 的
							// decryptGenericSecrets）。
							if data.IncludesCredentials && v.Value != "" && crypto != nil {
								encrypted, encErr := crypto.Encrypt(v.Value)
								if encErr != nil {
									logger.Default().Warn("re-encrypt generic secret", zap.String("field", name), zap.Error(encErr))
								} else {
									cfg.Values[name] = asset_entity.GenericValue{Value: encrypted}
								}
							}
						}
						if err := a.SetGenericConfig(cfg); err != nil {
							logger.Default().Warn("set generic config in import", zap.Error(err))
						}
					}
				}

				if err := tx.Create(a).Error; err != nil {
					return fmt.Errorf("创建资产 %s 失败: %w", a.Name, err)
				}
				assetIDMap[oldID] = a.ID
				result.AssetsImported++

				if oldJumpHostID > 0 {
					deferredRefs = append(deferredRefs, deferredRef{a.ID, oldJumpHostID, "jump_host"})
				}
				if oldSSHAssetID > 0 {
					deferredRefs = append(deferredRefs, deferredRef{a.ID, oldSSHAssetID, "ssh_tunnel"})
				}
			}

			// 回填跳板机和 SSH 隧道引用
			for _, ref := range deferredRefs {
				newRefID, ok := assetIDMap[ref.oldRefID]
				if !ok {
					continue
				}
				var asset asset_entity.Asset
				if err := tx.Where("id = ?", ref.newAssetID).First(&asset).Error; err != nil {
					continue
				}
				switch ref.refType {
				case "jump_host":
					cfg, err := asset.GetSSHConfig()
					if err != nil {
						continue
					}
					cfg.JumpHostID = newRefID
					if err := asset.SetSSHConfig(cfg); err != nil {
						continue
					}
				case "ssh_tunnel":
					if asset.IsDatabase() {
						cfg, err := asset.GetDatabaseConfig()
						if err != nil {
							continue
						}
						cfg.SSHAssetID = newRefID
						if err := asset.SetDatabaseConfig(cfg); err != nil {
							continue
						}
					} else if asset.IsRedis() {
						cfg, err := asset.GetRedisConfig()
						if err != nil {
							continue
						}
						cfg.SSHAssetID = newRefID
						if err := asset.SetRedisConfig(cfg); err != nil {
							continue
						}
					}
				}
				if err := tx.Save(&asset).Error; err != nil {
					return fmt.Errorf("更新资产引用失败: %w", err)
				}
			}
		}

		// 7. 端口转发
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

	// 8. 客户端设置透传
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
