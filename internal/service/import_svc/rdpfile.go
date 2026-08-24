package import_svc

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// RDPFileData 一个待导入的 .rdp 文件
type RDPFileData struct {
	Filename string `json:"filename"`
	Content  []byte `json:"content"`
}

// RDPFilePreviewResult RDP 文件预览结果（带导入会话）
type RDPFilePreviewResult struct {
	Preview  *PreviewResult `json:"preview"`
	SourceID string         `json:"sourceId"`
}

var rdpImportSession = &singleSlotSession[[]RDPFileData]{}

// parseRDPFile 解析单个 .rdp 文件内容为待导入条目。
//
// .rdp 是 Microsoft 的 key:type:value 文本格式（type: s=字符串, i=整数），
// 每行一个属性；旧版 mstsc 可能存为 UTF-16LE。仅提取 OpsKat RDP 资产支持的
// 字段，其余（网关、驱动器重定向等）忽略。
func parseRDPFile(content []byte) (parsedRDPEntry, error) {
	text := decodeRDPText(content)
	props := make(map[string]string)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 值本身可能含冒号（IPv6 地址、URL），最多切 3 段
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		key, typ, value := strings.ToLower(strings.TrimSpace(parts[0])), parts[1], parts[2]
		if typ != "s" && typ != "i" {
			continue
		}
		props[key] = value
	}

	entry := parsedRDPEntry{}
	address := props["full address"]
	if address == "" {
		return entry, fmt.Errorf("缺少 full address")
	}
	var err error
	entry.Host, entry.Port, err = splitRDPAddress(address)
	if err != nil {
		return entry, err
	}
	entry.Username, entry.Domain = normalizeRDPUsername(props["username"], props["domain"])
	// server port 显式存在时必须是 1..65535 的整数；缺省则沿用 full address 解析出的端口
	if v, ok := props["server port"]; ok {
		port, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || port < 1 || port > 65535 {
			return entry, fmt.Errorf("server port 无效: %s", v)
		}
		entry.Port = port
	}
	entry.Width = parseIntProp(props, "desktopwidth")
	entry.Height = parseIntProp(props, "desktopheight")
	// redirectclipboard 缺省视为开启（与 mstsc 默认一致）；仅在显式为 0 时关闭
	entry.Clipboard = true
	if v, ok := props["redirectclipboard"]; ok && v == "0" {
		entry.Clipboard = false
	}
	return entry, nil
}

type parsedRDPEntry struct {
	Host      string
	Port      int
	Username  string
	Domain    string
	Width     int
	Height    int
	Clipboard bool
}

// decodeRDPText 按 BOM 判定编码：UTF-16LE（旧版 mstsc）或 UTF-8。
func decodeRDPText(content []byte) string {
	if len(content) >= 2 && content[0] == 0xFF && content[1] == 0xFE {
		u16 := make([]uint16, 0, len(content)/2)
		for i := 2; i+1 < len(content); i += 2 {
			u16 = append(u16, uint16(content[i])|uint16(content[i+1])<<8)
		}
		return string(utf16.Decode(u16))
	}
	if len(content) >= 3 && content[0] == 0xEF && content[1] == 0xBB && content[2] == 0xBF {
		content = content[3:]
	}
	return string(content)
}

// splitRDPAddress 拆分可选的 host:port；裸 IPv6 保持为主机地址。
func splitRDPAddress(address string) (string, int, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, fmt.Errorf("RDP 地址为空")
	}
	if strings.Count(address, ":") == 0 {
		return address, 0, nil
	}
	if strings.Count(address, ":") > 1 && !strings.HasPrefix(address, "[") {
		if net.ParseIP(address) == nil {
			return "", 0, fmt.Errorf("RDP 地址格式无效: %s", address)
		}
		return address, 0, nil
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", 0, fmt.Errorf("RDP 地址端口格式无效: %s", address)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("RDP 地址端口无效: %s", portText)
	}
	return strings.TrimSpace(host), port, nil
}

// normalizeRDPUsername 统一处理 DOMAIN\user；显式 domain 列/属性优先。
func normalizeRDPUsername(username, domain string) (string, string) {
	username = strings.TrimSpace(username)
	domain = strings.TrimSpace(domain)
	if i := strings.Index(username, `\`); i > 0 {
		if domain == "" {
			domain = strings.TrimSpace(username[:i])
		}
		username = strings.TrimSpace(username[i+1:])
	}
	return username, domain
}

func parseIntProp(props map[string]string, key string) int {
	port, err := strconv.Atoi(props[key])
	if err != nil {
		return 0
	}
	return port
}

// rdpAssetKey RDP 资产去重键：host、port、domain、username。
func rdpAssetKey(host string, port int, domain, username string) string {
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s", host, port, domain, username)
}

func preserveRDPConfigOnOverwrite(oldCfg, newCfg *asset_entity.RDPConfig, preserveAuth bool) {
	newCfg.Proxy = oldCfg.Proxy
	newCfg.ProxyChain = oldCfg.ProxyChain
	if preserveAuth {
		newCfg.Password = oldCfg.Password
		newCfg.CredentialID = oldCfg.CredentialID
	}
}

func listRDPAssets(ctx context.Context) ([]*asset_entity.Asset, error) {
	return asset_svc.Asset().List(ctx, asset_entity.AssetTypeRDP, 0)
}

func existingRDPAssetMap(ctx context.Context) (map[string]*asset_entity.Asset, error) {
	assets, err := listRDPAssets(ctx)
	if err != nil {
		return nil, err
	}
	existingMap := make(map[string]*asset_entity.Asset, len(assets))
	for _, asset := range assets {
		cfg, err := asset.GetRDPConfig()
		if err != nil || cfg == nil {
			continue
		}
		username, domain := normalizeRDPUsername(cfg.Username, cfg.Domain)
		// 手工创建的资产可能把 DOMAIN\user 整体存在 Username、Domain 留空；
		// 与导入侧同一归一化，保证去重键形态一致。
		existingMap[rdpAssetKey(cfg.Host, cfg.Port, domain, username)] = asset
	}
	return existingMap, nil
}

// rdpEntryName 由文件名生成资产名称（去掉 .rdp 扩展名）
func rdpEntryName(filename string) string {
	name := filepath.Base(filename)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return strings.TrimSpace(name)
}

// PreviewRDPFiles 解析选中的 .rdp 文件列表，返回预览数据并缓存导入会话（不写数据库）
func PreviewRDPFiles(ctx context.Context, files []RDPFileData) (*RDPFilePreviewResult, error) {
	existingMap, err := existingRDPAssetMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询已有资产失败: %w", err)
	}

	var items []PreviewItem
	for i, file := range files {
		name := rdpEntryName(file.Filename)
		if name == "" {
			name = fmt.Sprintf("rdp-%d", i+1)
		}
		entry, err := parseRDPFile(file.Content)
		if err != nil {
			items = append(items, PreviewItem{Index: i, Name: name, Reason: err.Error()})
			continue
		}
		port := entry.Port
		if port == 0 {
			port = 3389
		}
		item := PreviewItem{
			Index:    i,
			Name:     name,
			Host:     entry.Host,
			Port:     port,
			Username: entry.Username,
			AuthType: "password",
			Exists:   existingMap[rdpAssetKey(entry.Host, port, entry.Domain, entry.Username)] != nil,
		}
		// 与导入端校验对称：缺用户名的条目预览即失败，不能勾选导入
		if entry.Username == "" {
			item.Reason = "缺少用户名"
		}
		items = append(items, item)
	}

	sourceID, err := rdpImportSession.Put(files)
	if err != nil {
		return nil, err
	}
	return &RDPFilePreviewResult{Preview: &PreviewResult{Items: items}, SourceID: sourceID}, nil
}

// ImportRDPSelected 导入用户选中的 .rdp 条目为 RDP 资产
func ImportRDPSelected(ctx context.Context, files []RDPFileData, selectedIndexes []int, opts ImportOptions) (*ImportResult, error) {
	selectedSet := make(map[int]bool, len(selectedIndexes))
	for _, i := range selectedIndexes {
		selectedSet[i] = true
	}

	var toImport []RDPFileData
	for i, file := range files {
		if selectedSet[i] {
			toImport = append(toImport, file)
		}
	}

	result := &ImportResult{Total: len(toImport)}
	if len(toImport) == 0 {
		return result, nil
	}

	existingMap, err := existingRDPAssetMap(ctx)
	if err != nil {
		return nil, err
	}

	for i, file := range toImport {
		name := rdpEntryName(file.Filename)
		if name == "" {
			name = fmt.Sprintf("rdp-%d", i+1)
		}
		entry, err := parseRDPFile(file.Content)
		if err != nil {
			result.addFailed(name, err.Error())
			continue
		}
		if entry.Username == "" {
			result.addFailed(name, "缺少用户名")
			continue
		}

		port := entry.Port
		if port == 0 {
			port = 3389
		}
		width, height := entry.Width, entry.Height
		if width == 0 {
			width = 1280
		}
		if height == 0 {
			height = 720
		}

		rdpCfg := &asset_entity.RDPConfig{
			Host: entry.Host, Port: port, Username: entry.Username,
			Domain: entry.Domain, Width: width, Height: height,
			Clipboard: entry.Clipboard,
		}

		dupKey := rdpAssetKey(entry.Host, port, entry.Domain, entry.Username)
		existingAsset := existingMap[dupKey]
		switch {
		case existingAsset != nil && opts.Overwrite:
			// .rdp 不拥有认证或代理配置，覆盖时保留已有值。
			if oldCfg, err := existingAsset.GetRDPConfig(); err == nil && oldCfg != nil {
				preserveRDPConfigOnOverwrite(oldCfg, rdpCfg, true)
			}
			existingAsset.Name = name
			if err := existingAsset.SetRDPConfig(rdpCfg); err != nil {
				result.addFailed(name, fmt.Sprintf("序列化配置失败: %v", err))
				continue
			}
			if err := asset_svc.Asset().Update(ctx, existingAsset); err != nil {
				result.addFailed(name, fmt.Sprintf("更新资产失败: %v", err))
				continue
			}
			result.Success++
		case existingAsset != nil:
			result.addSkipped(name)
		default:
			asset := &asset_entity.Asset{
				Name: name, Type: asset_entity.AssetTypeRDP,
				Icon: "monitor-up",
			}
			if err := asset.SetRDPConfig(rdpCfg); err != nil {
				result.addFailed(name, fmt.Sprintf("序列化配置失败: %v", err))
				continue
			}
			if err := asset_svc.Asset().Create(ctx, asset); err != nil {
				result.addFailed(name, fmt.Sprintf("创建资产失败: %v", err))
				continue
			}
			existingMap[dupKey] = asset
			result.Success++
		}
	}

	return result, nil
}

// RDPImportSessionData 按 sourceID 取回预览阶段缓存的文件列表
func RDPImportSessionData(id string) ([]RDPFileData, bool) {
	return rdpImportSession.Get(id)
}

// DeleteRDPImportSession 释放导入会话缓存
func DeleteRDPImportSession(id string) {
	rdpImportSession.Delete(id)
}
