package import_svc

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/group_repo"
	"github.com/opskat/opskat/internal/service/asset_svc"
)

// rdpExcelColumns RDP Excel 导入的列别名表（表头单元格 normalize 后精确匹配）。
// 中文列名与常见英文列名均接受；未识别的列忽略。
var rdpExcelColumns = map[string]string{
	// 名称
	"名称": "name", "昵称": "name", "name": "name", "nickname": "name",
	// 地址
	"地址": "host", "主机": "host", "服务器": "host", "ip": "host", "host": "host", "address": "host", "server": "host",
	// 端口
	"端口": "port", "port": "port",
	// 用户名
	"用户名": "username", "账号": "username", "用户": "username", "username": "username", "user": "username",
	// 密码
	"密码": "password", "password": "password", "pwd": "password",
	// 域
	"域": "domain", "domain": "domain",
	// 分组
	"分组": "group", "组": "group", "group": "group",
	// 宽度
	"宽度": "width", "宽": "width", "width": "width",
	// 高度
	"高度": "height", "高": "height", "height": "height",
	// 剪贴板
	"剪贴板": "clipboard", "clipboard": "clipboard",
}

// rdpExcelRow 一行待导入的 RDP 数据（Excel 原始单元格文本 + 行级校验结果）
type rdpExcelRow struct {
	Name        string
	Host        string
	Port        int
	Username    string
	Password    string
	Domain      string
	Group       string
	Width       int
	Height      int
	Clipboard   bool
	HasPassword bool
	Reason      string // 非空表示该行字段非法或缺失，预览/导入均按失败处理
}

// normalizeExcelCell 表头匹配用：去空白、转小写
func normalizeExcelCell(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// parseExcelStrictInt 单元格严格整数解析：非纯整数（如 3390.9、abc）视为非法
func parseExcelStrictInt(s string) (int, bool) {
	i, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return i, true
}

// parseExcelBoolStrict 单元格布尔解析：是/否、true/false、1/0、yes/no、开/关；
// 无法识别返回 ok=false（不静默取默认值）
func parseExcelBoolStrict(s string) (value, ok bool) {
	switch normalizeExcelCell(s) {
	case "是", "true", "1", "yes", "y", "开":
		return true, true
	case "否", "false", "0", "no", "n", "关":
		return false, true
	default:
		return false, false
	}
}

// parseRDPExcel 解析 xlsx 数据：首个工作表，首行为表头，其余每行一个 RDP 连接。
// 全空行跳过；返回行序与数据行顺序一致的列表。
func parseRDPExcel(data []byte) ([]rdpExcelRow, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("没有工作表：Excel 文件为空")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("没有数据行：首行应为表头（名称/分组/地址/端口/用户名/密码/域/宽度/高度/剪贴板），从第二行开始填写连接")
	}

	// 表头 → 列号；同一逻辑字段出现多列时确定性报错（避免取值依赖 map 遍历顺序）
	colFields := make(map[int]string, len(rows[0]))
	seenFields := make(map[string]bool, len(rdpExcelColumns))
	for col, cell := range rows[0] {
		field, ok := rdpExcelColumns[normalizeExcelCell(cell)]
		if !ok {
			continue
		}
		if seenFields[field] {
			return nil, fmt.Errorf("表头字段重复：「%s」列出现多次，同一字段只能填写一列", strings.TrimSpace(cell))
		}
		seenFields[field] = true
		colFields[col] = field
	}
	if _, ok := hasColumn(colFields, "host"); !ok {
		return nil, fmt.Errorf("未识别到「地址」列：请使用模板表头（名称/分组/地址/端口/用户名/密码/域/宽度/高度/剪贴板）")
	}

	var parsed []rdpExcelRow
	for _, row := range rows[1:] {
		cells := make(map[string]string, len(colFields))
		for col, field := range colFields {
			if col < len(row) {
				cells[field] = strings.TrimSpace(row[col])
			} else {
				cells[field] = ""
			}
		}
		// 全空行跳过
		empty := true
		for _, v := range cells {
			if v != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		parsed = append(parsed, parseRDPExcelRow(cells))
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("没有有效数据行：全部数据行为空")
	}
	return parsed, nil
}

// parseRDPExcelRow 把一行单元格文本规范化为 rdpExcelRow：
// 地址列支持 host:port / [IPv6]:port（独立端口列非空时优先）；用户名支持 DOMAIN\user；
// 非法字段记入 Reason，不静默取默认值或截断。
func parseRDPExcelRow(cells map[string]string) rdpExcelRow {
	row := rdpExcelRow{
		Name:        cells["name"],
		Username:    cells["username"],
		Password:    cells["password"],
		Domain:      cells["domain"],
		Group:       cells["group"],
		HasPassword: cells["password"] != "",
		Clipboard:   true, // 剪贴板列留空时默认开启
	}
	invalid := func(reason string) {
		if row.Reason == "" {
			row.Reason = reason
		}
	}

	// 地址：复用 .rdp 的拆分规则（裸 IPv6 保持主机地址）
	host, embeddedPort, err := splitRDPAddress(cells["host"])
	if err != nil {
		invalid(err.Error())
		host = ""
	}
	row.Host = host
	row.Username, row.Domain = normalizeRDPUsername(cells["username"], cells["domain"])

	// 端口：独立端口列非空时优先，且必须是 1..65535 的严格整数
	if cells["port"] != "" {
		if p, ok := parseExcelStrictInt(cells["port"]); ok && p >= 1 && p <= 65535 {
			row.Port = p
		} else {
			invalid(fmt.Sprintf("端口无效: %s", cells["port"]))
		}
	} else {
		row.Port = embeddedPort
	}

	// 宽度/高度：非空时必须是 1..65535 的严格整数
	if cells["width"] != "" {
		if v, ok := parseExcelStrictInt(cells["width"]); ok && v >= 1 && v <= 65535 {
			row.Width = v
		} else {
			invalid(fmt.Sprintf("宽度无效: %s", cells["width"]))
		}
	}
	if cells["height"] != "" {
		if v, ok := parseExcelStrictInt(cells["height"]); ok && v >= 1 && v <= 65535 {
			row.Height = v
		} else {
			invalid(fmt.Sprintf("高度无效: %s", cells["height"]))
		}
	}

	// 剪贴板：非空时必须是受支持的布尔词
	if cells["clipboard"] != "" {
		if v, ok := parseExcelBoolStrict(cells["clipboard"]); ok {
			row.Clipboard = v
		} else {
			invalid(fmt.Sprintf("剪贴板无效: %s", cells["clipboard"]))
		}
	}

	if row.Username == "" {
		invalid("缺少用户名")
	}
	return row
}

func hasColumn(colFields map[int]string, field string) (int, bool) {
	for col, f := range colFields {
		if f == field {
			return col, true
		}
	}
	return 0, false
}

// RDPExcelPreviewResult RDP Excel 预览结果（带导入会话）
type RDPExcelPreviewResult struct {
	Preview  *PreviewResult `json:"preview"`
	SourceID string         `json:"sourceId"`
}

var rdpExcelImportSession = &singleSlotSession[[]byte]{}

// RDPExcelImportSessionData 按 sourceID 取回预览阶段缓存的文件内容
func RDPExcelImportSessionData(id string) ([]byte, bool) {
	return rdpExcelImportSession.Get(id)
}

// DeleteRDPExcelImportSession 释放导入会话缓存
func DeleteRDPExcelImportSession(id string) {
	rdpExcelImportSession.Delete(id)
}

// rdpRowName 行名称：优先「名称」列，缺省用地址
func rdpRowName(row rdpExcelRow) string {
	if row.Name != "" {
		return row.Name
	}
	return row.Host
}

// rdpRowPort 行端口：缺省 3389
func rdpRowPort(row rdpExcelRow) int {
	if row.Port > 0 {
		return row.Port
	}
	return 3389
}

// PreviewRDPExcel 解析 xlsx 数据，返回预览（不写数据库）并缓存导入会话。
// 密码不进入预览条目，仅在导入阶段从会话缓存的原文件中重新解析。
func PreviewRDPExcel(ctx context.Context, data []byte) (*RDPExcelPreviewResult, error) {
	rows, err := parseRDPExcel(data)
	if err != nil {
		return nil, err
	}
	existingMap, err := existingRDPAssetMap(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询已有资产失败: %w", err)
	}

	var items []PreviewItem
	groupSeen := make(map[string]bool)
	var groups []PreviewGroup
	for i, row := range rows {
		port := rdpRowPort(row)
		item := PreviewItem{
			Index:       i,
			Name:        rdpRowName(row),
			Host:        row.Host,
			Port:        port,
			Username:    row.Username,
			AuthType:    "password",
			GroupID:     row.Group,
			HasPassword: row.HasPassword,
			Exists:      existingMap[rdpAssetKey(row.Host, port, row.Domain, row.Username)] != nil,
			Reason:      row.Reason,
		}
		if row.Group != "" && !groupSeen[row.Group] {
			groupSeen[row.Group] = true
			groups = append(groups, PreviewGroup{ID: row.Group, Name: row.Group})
		}
		items = append(items, item)
	}

	sourceID, err := rdpExcelImportSession.Put(data)
	if err != nil {
		return nil, err
	}
	return &RDPExcelPreviewResult{Preview: &PreviewResult{Groups: groups, Items: items}, SourceID: sourceID}, nil
}

// ImportRDPExcelSelected 导入用户选中的 Excel 行为 RDP 资产。
// 密码列有值则加密写入；为空则不覆盖已有认证（覆盖模式下保留旧密码/统一凭证）。
// 覆盖时保留导入源不拥有的 Proxy/ProxyChain；重复项在创建分组之前跳过。
func ImportRDPExcelSelected(ctx context.Context, data []byte, selectedIndexes []int, opts ImportOptions) (*ImportResult, error) {
	rows, err := parseRDPExcel(data)
	if err != nil {
		return nil, err
	}

	selectedSet := make(map[int]bool, len(selectedIndexes))
	for _, i := range selectedIndexes {
		selectedSet[i] = true
	}

	var toImport []rdpExcelRow
	for i, row := range rows {
		if selectedSet[i] {
			toImport = append(toImport, row)
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

	// 分组缓存懒加载：只有真正要写资产时才查询/创建分组，
	// 避免全部跳过的导入留下孤立分组
	var groupCache map[string]int64
	groupCacheLoaded := false
	ensureGroupCache := func() error {
		if groupCacheLoaded {
			return nil
		}
		existingGroups, err := group_repo.Group().List(ctx)
		if err != nil {
			return fmt.Errorf("查询已有分组失败: %w", err)
		}
		groupCache = buildGroupCache(existingGroups)
		groupCacheLoaded = true
		return nil
	}

	for _, row := range toImport {
		name := rdpRowName(row)
		// 行级校验失败（缺地址/用户名、非法端口等）：不创建资产、不触碰分组
		if row.Reason != "" {
			result.addFailed(name, row.Reason)
			continue
		}

		port := rdpRowPort(row)
		width, height := row.Width, row.Height
		if width == 0 {
			width = 1280
		}
		if height == 0 {
			height = 720
		}

		rdpCfg := &asset_entity.RDPConfig{
			Host: row.Host, Port: port, Username: row.Username,
			Domain: row.Domain, Width: width, Height: height,
			Clipboard: row.Clipboard,
		}
		if row.Password != "" {
			encrypted, err := encryptPassword(row.Password)
			if err != nil {
				result.addFailed(name, fmt.Sprintf("加密密码失败: %v", err))
				continue
			}
			rdpCfg.Password = encrypted
		}

		dupKey := rdpAssetKey(row.Host, port, row.Domain, row.Username)
		existingAsset := existingMap[dupKey]
		if existingAsset != nil && !opts.Overwrite {
			result.addSkipped(name)
			continue
		}

		// 分组：按名称复用或创建（仅在确定要写资产时）
		groupID := int64(0)
		if row.Group != "" {
			if err := ensureGroupCache(); err != nil {
				return nil, err
			}
			var err error
			groupID, err = ensureGroupByName(ctx, row.Group, groupCache)
			if err != nil {
				result.addFailed(name, fmt.Sprintf("创建分组失败: %v", err))
				continue
			}
		}

		if existingAsset != nil {
			// 覆盖：密码列留空时保留旧认证；Excel 不拥有 Proxy/ProxyChain，始终保留已有值
			if oldCfg, err := existingAsset.GetRDPConfig(); err == nil && oldCfg != nil {
				preserveRDPConfigOnOverwrite(oldCfg, rdpCfg, rdpCfg.Password == "")
			}
			existingAsset.Name = name
			if groupID != 0 {
				existingAsset.GroupID = groupID
			}
			if err := existingAsset.SetRDPConfig(rdpCfg); err != nil {
				result.addFailed(name, fmt.Sprintf("序列化配置失败: %v", err))
				continue
			}
			if err := asset_svc.Asset().Update(ctx, existingAsset); err != nil {
				result.addFailed(name, fmt.Sprintf("更新资产失败: %v", err))
				continue
			}
			result.Success++
		} else {
			asset := &asset_entity.Asset{
				Name: name, Type: asset_entity.AssetTypeRDP, GroupID: groupID,
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

// BuildRDPExcelTemplate 生成 RDP 批量导入模板 xlsx（含示例行与填写说明）
func BuildRDPExcelTemplate() ([]byte, error) {
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	const sheet = "RDP"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}

	headers := []string{"名称", "分组", "地址", "端口", "用户名", "密码", "域", "宽度", "高度", "剪贴板"}
	for col, h := range headers {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			return nil, err
		}
		if err := f.SetCellValue(sheet, cell, h); err != nil {
			return nil, err
		}
	}
	demos := [][]interface{}{
		{"财务服务器", "生产环境", "192.168.1.60", 3389, "administrator", "Demo@123", "CORP", 1920, 1080, "是"},
		{"跳板机", "办公网", "10.0.0.15", nil, "ops", nil, nil, nil, nil, nil},
	}
	for r, demo := range demos {
		for col, v := range demo {
			if v == nil {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(col+1, r+2)
			if err != nil {
				return nil, err
			}
			if err := f.SetCellValue(sheet, cell, v); err != nil {
				return nil, err
			}
		}
	}

	// 说明 sheet
	const helpSheet = "说明"
	if _, err := f.NewSheet(helpSheet); err != nil {
		return nil, err
	}
	notes := []string{
		"RDP 批量导入模板",
		"在 RDP 工作表按表头填写，一行一个连接；导入时在预览中勾选要导入的行。",
		"",
		"列说明：",
		"名称：资产显示名，可留空（缺省用地址）",
		"分组：可留空；不存在时会自动创建",
		"地址：必填，主机名或 IP，可带端口（如 10.0.0.1:3390）",
		"端口：可留空，默认 3389",
		"用户名：必填，支持 DOMAIN\\user 或配合「域」列",
		"密码：可留空；留空时保留已有资产的原密码",
		"域：Windows 域，可留空",
		"宽度/高度：远程桌面分辨率，可留空，默认 1280x720",
		"剪贴板：是/否，可留空，默认开启",
	}
	for i, note := range notes {
		cell, err := excelize.CoordinatesToCellName(1, i+1)
		if err != nil {
			return nil, err
		}
		if err := f.SetCellValue(helpSheet, cell, note); err != nil {
			return nil, err
		}
	}

	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
