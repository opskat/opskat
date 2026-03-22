package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"ops-cat/internal/ai"
	"ops-cat/internal/model/entity/asset_entity"
	"ops-cat/internal/model/entity/group_entity"
	"ops-cat/internal/repository/group_repo"
	"ops-cat/internal/service/asset_svc"
	"ops-cat/internal/service/credential_svc"
	"ops-cat/internal/service/ssh_svc"

	"github.com/cago-frame/cago/pkg/i18n"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App Wails应用主结构体，替代controller层
type App struct {
	ctx        context.Context
	lang       string
	sshManager *ssh_svc.Manager
	aiAgent    *ai.Agent
}

// NewApp 创建App实例
func NewApp() *App {
	return &App{
		lang:       "zh-cn",
		sshManager: ssh_svc.NewManager(),
	}
}

// SetAIProvider 设置 AI provider 并创建 agent
func (a *App) SetAIProvider(providerType, apiBase, apiKey, model string) {
	var provider ai.Provider
	switch providerType {
	case "openai":
		provider = ai.NewOpenAIProvider("OpenAI Compatible", apiBase, apiKey, model)
	case "local_cli":
		// apiBase 作为 CLI 路径，model 作为 CLI 类型
		provider = ai.NewLocalCLIProvider("Local CLI", apiBase, model)
	default:
		provider = ai.NewOpenAIProvider(providerType, apiBase, apiKey, model)
	}
	a.aiAgent = ai.NewAgent(provider, ai.NewDefaultToolExecutor())
}

// startup Wails启动回调
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// SetLanguage 前端调用，同步语言设置到后端
func (a *App) SetLanguage(lang string) {
	a.lang = lang
}

// GetLanguage 返回当前语言
func (a *App) GetLanguage() string {
	return a.lang
}

// langCtx 返回带语言设置的context，每个绑定方法内部调用
func (a *App) langCtx() context.Context {
	return i18n.WithLanguage(a.ctx, a.lang)
}

// --- 资产操作 ---

// GetAsset 获取资产详情
func (a *App) GetAsset(id int64) (*asset_entity.Asset, error) {
	return asset_svc.Asset().Get(a.langCtx(), id)
}

// ListAssets 列出资产
func (a *App) ListAssets(assetType string, groupID int64) ([]*asset_entity.Asset, error) {
	return asset_svc.Asset().List(a.langCtx(), assetType, groupID)
}

// CreateAsset 创建资产
func (a *App) CreateAsset(asset *asset_entity.Asset) error {
	return asset_svc.Asset().Create(a.langCtx(), asset)
}

// UpdateAsset 更新资产
func (a *App) UpdateAsset(asset *asset_entity.Asset) error {
	return asset_svc.Asset().Update(a.langCtx(), asset)
}

// DeleteAsset 删除资产
func (a *App) DeleteAsset(id int64) error {
	return asset_svc.Asset().Delete(a.langCtx(), id)
}

// --- 分组操作 ---

// ListGroups 列出所有分组
func (a *App) ListGroups() ([]*group_entity.Group, error) {
	return group_repo.Group().List(a.langCtx())
}

// CreateGroup 创建分组
func (a *App) CreateGroup(group *group_entity.Group) error {
	if err := group.Validate(); err != nil {
		return err
	}
	return group_repo.Group().Create(a.langCtx(), group)
}

// --- SSH 操作 ---

// SSHConnectRequest 前端 SSH 连接请求
type SSHConnectRequest struct {
	AssetID  int64  `json:"assetId"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

// ConnectSSH 连接 SSH 服务器，返回会话 ID
func (a *App) ConnectSSH(req SSHConnectRequest) (string, error) {
	// 获取资产信息
	asset, err := asset_svc.Asset().Get(a.langCtx(), req.AssetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsSSH() {
		return "", fmt.Errorf("资产不是SSH类型")
	}
	sshCfg, err := asset.GetSSHConfig()
	if err != nil {
		return "", err
	}

	sessionID, err := a.sshManager.Connect(ssh_svc.ConnectConfig{
		Host:     sshCfg.Host,
		Port:     sshCfg.Port,
		Username: sshCfg.Username,
		AuthType: sshCfg.AuthType,
		Password: req.Password,
		Key:      req.Key,
		AssetID:  req.AssetID,
		Cols:     req.Cols,
		Rows:     req.Rows,
		OnData: func(sid string, data []byte) {
			// 通过 Wails Events 发送终端输出到前端（base64 编码二进制数据）
			wailsRuntime.EventsEmit(a.ctx, "ssh:data:"+sid, base64.StdEncoding.EncodeToString(data))
		},
		OnClosed: func(sid string) {
			wailsRuntime.EventsEmit(a.ctx, "ssh:closed:"+sid, nil)
		},
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// WriteSSH 向 SSH 终端写入数据（base64 编码）
func (a *App) WriteSSH(sessionID string, dataB64 string) error {
	sess, ok := a.sshManager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("解码数据失败: %w", err)
	}
	return sess.Write(data)
}

// ResizeSSH 调整终端尺寸
func (a *App) ResizeSSH(sessionID string, cols int, rows int) error {
	sess, ok := a.sshManager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	return sess.Resize(cols, rows)
}

// DisconnectSSH 断开 SSH 连接
func (a *App) DisconnectSSH(sessionID string) {
	a.sshManager.Disconnect(sessionID)
}

// --- AI 操作 ---

// SendAIMessage 发送 AI 消息，通过 Wails Events 流式返回
func (a *App) SendAIMessage(conversationID string, messages []ai.Message) error {
	if a.aiAgent == nil {
		return fmt.Errorf("请先配置 AI Provider")
	}

	// 添加系统提示
	fullMessages := []ai.Message{
		{
			Role:    ai.RoleSystem,
			Content: "你是 Ops Cat 的 AI 助手，帮助用户管理IT资产。你可以列出资产、查看详情、添加资产、在SSH服务器上执行命令。请用中文回复。",
		},
	}
	fullMessages = append(fullMessages, messages...)

	go func() {
		err := a.aiAgent.Chat(a.ctx, fullMessages, func(event ai.StreamEvent) {
			wailsRuntime.EventsEmit(a.ctx, "ai:event:"+conversationID, event)
		})
		if err != nil {
			wailsRuntime.EventsEmit(a.ctx, "ai:event:"+conversationID, ai.StreamEvent{
				Type:  "error",
				Error: err.Error(),
			})
		}
	}()

	return nil
}

// DetectLocalCLIs 检测本地 AI CLI 工具
func (a *App) DetectLocalCLIs() []ai.CLIInfo {
	return ai.DetectLocalCLIs()
}

// --- 凭证操作 ---

// SaveCredential 加密保存凭证（密码或密钥），返回加密后的字符串
func (a *App) SaveCredential(plaintext string) (string, error) {
	return credential_svc.Default().Encrypt(plaintext)
}

// LoadCredential 解密凭证
func (a *App) LoadCredential(ciphertext string) (string, error) {
	return credential_svc.Default().Decrypt(ciphertext)
}

// --- 导入导出 ---

// ExportData 导出所有资产和分组为 JSON
func (a *App) ExportData() (string, error) {
	assets, err := asset_svc.Asset().List(a.langCtx(), "", 0)
	if err != nil {
		return "", err
	}
	groups, err := group_repo.Group().List(a.langCtx())
	if err != nil {
		return "", err
	}
	data := map[string]interface{}{
		"version": "1.0",
		"assets":  assets,
		"groups":  groups,
	}
	result, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(result), nil
}
