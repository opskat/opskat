package import_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

const windTermDefaultUsername = "root"

type WindTermPreviewResult struct {
	Preview  *PreviewResult `json:"preview"`
	SourceID string         `json:"sourceId"`
}

type windTermSession struct {
	AutoLogin           string `json:"-"`
	Group               string `json:"session.group"`
	Label               string `json:"session.label"`
	Port                int    `json:"session.port"`
	Protocol            string `json:"session.protocol"`
	Target              string `json:"session.target"`
	UUID                string `json:"session.uuid"`
	IdentityFileWindows string `json:"ssh.identityFilePath.windows"`
}

func PreviewWindTermConfig(ctx context.Context, data []byte) (*PreviewResult, error) {
	sessions, err := parseWindTermSessions(data)
	if err != nil {
		return nil, err
	}

	existingSet, err := existingSSHAssetSet(ctx)
	if err != nil {
		return nil, err
	}

	groupSet := make(map[string]bool)
	var groups []PreviewGroup
	var items []PreviewItem
	for idx, session := range sessions {
		entry := normalizeWindTermSession(session)
		if entry.Host == "" {
			continue
		}
		if entry.GroupID != "" {
			for _, group := range windTermGroupPaths(entry.GroupID) {
				if !groupSet[group.ID] {
					groups = append(groups, group)
					groupSet[group.ID] = true
				}
			}
		}
		items = append(items, PreviewItem{
			Index:    idx,
			Name:     entry.Name,
			Host:     entry.Host,
			Port:     entry.Port,
			Username: entry.Username,
			AuthType: entry.AuthType,
			GroupID:  entry.GroupID,
			Exists:   existingSet[sshAssetKey(entry.Host, entry.Port, entry.Username)],
		})
	}

	return &PreviewResult{Groups: groups, Items: items}, nil
}

func ImportWindTermSelected(ctx context.Context, data []byte, selectedIndexes []int, opts ImportOptions) (*ImportResult, error) {
	sessions, err := parseWindTermSessions(data)
	if err != nil {
		return nil, err
	}

	selectedSet := make(map[int]bool, len(selectedIndexes))
	for _, index := range selectedIndexes {
		selectedSet[index] = true
	}

	var toImport []windTermSession
	for index, session := range sessions {
		if selectedSet[index] {
			toImport = append(toImport, session)
		}
	}

	result := &ImportResult{Total: len(toImport)}
	if len(toImport) == 0 {
		return result, nil
	}

	existingMap, err := existingSSHAssetMap(ctx)
	if err != nil {
		return nil, err
	}
	existingGroups, err := group_repo.Group().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询已有分组失败: %w", err)
	}
	groupCache := buildGroupCache(existingGroups)

	for _, session := range toImport {
		entry := normalizeWindTermSession(session)
		if entry.Host == "" {
			result.Failed++
			result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: "host 为空"})
			continue
		}

		dupKey := sshAssetKey(entry.Host, entry.Port, entry.Username)
		existingAsset := existingMap[dupKey]
		if existingAsset != nil && !opts.Overwrite {
			result.Skipped++
			continue
		}

		groupID := int64(0)
		if entry.GroupID != "" {
			var err error
			groupID, err = ensureGroupPath(ctx, entry.GroupID, groupCache)
			if err != nil {
				result.Failed++
				result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: fmt.Sprintf("创建分组失败: %v", err)})
				continue
			}
		}

		sshCfg := &asset_entity.SSHConfig{
			Host:        entry.Host,
			Port:        entry.Port,
			Username:    entry.Username,
			AuthType:    entry.AuthType,
			PrivateKeys: entry.PrivateKeys,
		}

		if existingAsset != nil && opts.Overwrite {
			preserveWindTermSensitiveFields(existingAsset, sshCfg)
			existingAsset.Name = entry.Name
			if groupID != 0 {
				existingAsset.GroupID = groupID
			}
			if err := existingAsset.SetSSHConfig(sshCfg); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: fmt.Sprintf("序列化配置失败: %v", err)})
				continue
			}
			if err := asset_svc.Asset().Update(ctx, existingAsset); err != nil {
				result.Failed++
				result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: fmt.Sprintf("更新资产失败: %v", err)})
				continue
			}
			result.Success++
			continue
		}

		asset := &asset_entity.Asset{Name: entry.Name, Type: asset_entity.AssetTypeSSH, GroupID: groupID, Icon: "server"}
		if err := asset.SetSSHConfig(sshCfg); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: fmt.Sprintf("序列化配置失败: %v", err)})
			continue
		}
		if err := asset_svc.Asset().Create(ctx, asset); err != nil {
			result.Failed++
			result.Errors = append(result.Errors, ImportError{Name: entry.Name, Reason: fmt.Sprintf("创建资产失败: %v", err)})
			continue
		}
		existingMap[dupKey] = asset
		result.Success++
	}

	return result, nil
}

type normalizedWindTermSession struct {
	Name        string
	Host        string
	Port        int
	Username    string
	AuthType    string
	GroupID     string
	PrivateKeys []string
}

func parseWindTermSessions(data []byte) ([]windTermSession, error) {
	var rawSessions []windTermSession
	if err := json.Unmarshal(data, &rawSessions); err != nil {
		return nil, fmt.Errorf("解析 WindTerm 配置失败: %w", err)
	}

	sessions := make([]windTermSession, 0, len(rawSessions))
	for _, session := range rawSessions {
		if strings.EqualFold(session.Protocol, "SSH") {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func normalizeWindTermSession(session windTermSession) normalizedWindTermSession {
	host := strings.TrimSpace(session.Target)
	port := session.Port
	if port <= 0 {
		port = 22
	}
	username := windTermDefaultUsername
	name := strings.TrimSpace(session.Label)
	if name == "" {
		name = fmt.Sprintf("%s@%s:%d", username, host, port)
	}

	var privateKeys []string
	identityFile := strings.TrimSpace(session.IdentityFileWindows)
	if identityFile != "" {
		privateKeys = append(privateKeys, expandPath(identityFile))
	}

	authType := asset_entity.AuthTypePassword
	if len(privateKeys) > 0 {
		authType = asset_entity.AuthTypeKey
	}

	return normalizedWindTermSession{
		Name:        name,
		Host:        host,
		Port:        port,
		Username:    username,
		AuthType:    authType,
		GroupID:     strings.TrimSpace(session.Group),
		PrivateKeys: privateKeys,
	}
}

func windTermGroupPaths(path string) []PreviewGroup {
	var groups []PreviewGroup
	var current []string
	for _, part := range strings.Split(path, ">") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		current = append(current, name)
		id := strings.Join(current, ">")
		groups = append(groups, PreviewGroup{ID: id, Name: name})
	}
	return groups
}

func preserveWindTermSensitiveFields(asset *asset_entity.Asset, sshCfg *asset_entity.SSHConfig) {
	oldCfg, err := asset.GetSSHConfig()
	if err != nil {
		return
	}
	sshCfg.Password = oldCfg.Password
	sshCfg.CredentialID = oldCfg.CredentialID
	sshCfg.PrivateKeyPassphrase = oldCfg.PrivateKeyPassphrase
}
