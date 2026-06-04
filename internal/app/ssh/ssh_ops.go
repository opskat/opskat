package ssh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/dirsync"
	"github.com/opskat/opskat/internal/service/asset_svc"
	"github.com/opskat/opskat/internal/service/credential_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/service/testreg"
	"github.com/opskat/opskat/internal/sshpool"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// SSHConnectRequest 前端 SSH 连接请求
type SSHConnectRequest struct {
	AssetID  int64  `json:"assetId"`
	Password string `json:"password"`
	Key      string `json:"key"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
}

// ConnectSSH 连接 SSH 服务器，返回会话 ID
func (s *SSH) ConnectSSH(req SSHConnectRequest) (string, error) {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(s.ctx, s.lang.Lang()), req.AssetID)
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

	storedPassword, storedKey, storedPassphrase := s.resolveSSHCredentialsFull(sshCfg)
	password := req.Password
	key := req.Key
	if password == "" {
		password = storedPassword
	}
	if key == "" {
		key = storedKey
	}

	connectCfg := ssh_svc.ConnectConfig{
		Host:              sshCfg.Host,
		Port:              sshCfg.Port,
		Username:          sshCfg.Username,
		AuthType:          sshCfg.AuthType,
		Password:          password,
		Key:               key,
		KeyPassphrase:     storedPassphrase,
		PrivateKeys:       sshCfg.PrivateKeys,
		AssetID:           req.AssetID,
		Cols:              req.Cols,
		Rows:              req.Rows,
		Proxy:             s.decryptProxyPassword(sshCfg.Proxy),
		HostKeyVerifyFunc: ssh_svc.AutoTrustFirstRejectChangeVerifyFunc(),
		OnData: func(sid string, data []byte) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:data:"+sid, base64.StdEncoding.EncodeToString(data))
		},
		OnClosed: func(sid string) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:closed:"+sid, nil)
		},
		OnSync: func(sid string, state ssh_svc.DirectorySyncState) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:sync:"+sid, state)
		},
	}

	// 解析跳板机链（递归，最大深度 5）
	jumpHostID := asset.SSHTunnelID
	if jumpHostID == 0 {
		jumpHostID = sshCfg.JumpHostID // backward compat
	}
	if jumpHostID > 0 {
		jumpHosts, err := s.resolveJumpHosts(jumpHostID, 5)
		if err != nil {
			return "", fmt.Errorf("解析跳板机失败: %w", err)
		}
		connectCfg.JumpHosts = jumpHosts
	}

	sessionID, err := s.manager.Connect(connectCfg)
	if err != nil {
		if isSSHAuthError(err) {
			return "", fmt.Errorf("AUTH_FAILED:%s", err.Error())
		}
		return "", err
	}
	return sessionID, nil
}

// isSSHAuthError 判断是否为 SSH 认证失败错误
func isSSHAuthError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods remain")
}

// ConnectSSHAsync 异步连接 SSH 服务器，立即返回 connectionId，通过事件推送进度
func (s *SSH) ConnectSSHAsync(req SSHConnectRequest) (string, error) {
	// 前置校验（同步）
	asset, err := asset_svc.Asset().Get(i18n.Ctx(s.ctx, s.lang.Lang()), req.AssetID)
	if err != nil {
		return "", fmt.Errorf("资产不存在: %w", err)
	}
	if !asset.IsSSH() {
		return "", fmt.Errorf("资产不是SSH类型")
	}

	connectionID := s.nextConnectionID()

	// 创建可取消的 context
	connCtx, cancel := context.WithCancel(s.ctx)
	s.pendingConnections.Store(connectionID, cancel)

	eventName := "ssh:connect:" + connectionID

	emitEvent := func(event SSHConnectEvent) {
		wailsRuntime.EventsEmit(s.ctx, eventName, event)
	}

	go func() {
		defer func() {
			s.pendingConnections.Delete(connectionID)
		}()

		emitEvent(SSHConnectEvent{Type: "progress", Step: "resolve", Message: "正在解析凭证..."})

		sshCfg, err := asset.GetSSHConfig()
		if err != nil {
			emitEvent(SSHConnectEvent{Type: "error", Error: err.Error()})
			return
		}

		if connCtx.Err() != nil {
			return
		}

		storedPassword, storedKey, storedPassphrase := s.resolveSSHCredentialsFull(sshCfg)
		password := req.Password
		key := req.Key
		if password == "" {
			password = storedPassword
		}
		if key == "" {
			key = storedKey
		}

		connectCfg := ssh_svc.ConnectConfig{
			Host:          sshCfg.Host,
			Port:          sshCfg.Port,
			Username:      sshCfg.Username,
			AuthType:      sshCfg.AuthType,
			Password:      password,
			Key:           key,
			KeyPassphrase: storedPassphrase,
			PrivateKeys:   sshCfg.PrivateKeys,
			AssetID:       req.AssetID,
			Cols:          req.Cols,
			Rows:          req.Rows,
			Proxy:         s.decryptProxyPassword(sshCfg.Proxy),
			OnData: func(sid string, data []byte) {
				wailsRuntime.EventsEmit(s.ctx, "ssh:data:"+sid, base64.StdEncoding.EncodeToString(data))
			},
			OnClosed: func(sid string) {
				wailsRuntime.EventsEmit(s.ctx, "ssh:closed:"+sid, nil)
			},
			OnSync: func(sid string, state ssh_svc.DirectorySyncState) {
				wailsRuntime.EventsEmit(s.ctx, "ssh:sync:"+sid, state)
			},
			OnProgress: func(step, message string) {
				emitEvent(SSHConnectEvent{Type: "progress", Step: step, Message: message})
			},
			OnAuthChallenge: func(prompts []string, echo []bool) ([]string, error) {
				challengeID := fmt.Sprintf("auth_%s_%d", connectionID, time.Now().UnixNano())
				emitEvent(SSHConnectEvent{
					Type:        "auth_challenge",
					ChallengeID: challengeID,
					Prompts:     prompts,
					Echo:        echo,
				})

				ch := make(chan []string, 1)
				s.pendingAuthResponses.Store(challengeID, ch)
				defer s.pendingAuthResponses.Delete(challengeID)

				select {
				case answers := <-ch:
					return answers, nil
				case <-connCtx.Done():
					return nil, fmt.Errorf("连接已取消")
				}
			},
			HostKeyVerifyFunc: func(event ssh_svc.HostKeyEvent) ssh_svc.HostKeyAction {
				verifyID := fmt.Sprintf("hk_%s_%d", connectionID, time.Now().UnixNano())
				emitEvent(SSHConnectEvent{
					Type:            "host_key_verify",
					HostKeyVerifyID: verifyID,
					HostKeyEvent:    &event,
				})

				ch := make(chan ssh_svc.HostKeyAction, 1)
				s.pendingHostKeyResponses.Store(verifyID, ch)
				defer s.pendingHostKeyResponses.Delete(verifyID)

				select {
				case action := <-ch:
					return action
				case <-connCtx.Done():
					return ssh_svc.HostKeyReject
				case <-s.appCtx.Done():
					return ssh_svc.HostKeyReject
				}
			},
		}

		// 解析跳板机链
		jumpHostID := asset.SSHTunnelID
		if jumpHostID == 0 {
			jumpHostID = sshCfg.JumpHostID // backward compat
		}
		if jumpHostID > 0 {
			emitEvent(SSHConnectEvent{Type: "progress", Step: "resolve", Message: "正在解析跳板机链..."})
			jumpHosts, err := s.resolveJumpHosts(jumpHostID, 5)
			if err != nil {
				emitEvent(SSHConnectEvent{Type: "error", Error: fmt.Sprintf("解析跳板机失败: %s", err.Error())})
				return
			}
			connectCfg.JumpHosts = jumpHosts
		}

		if connCtx.Err() != nil {
			return
		}

		sessionID, err := s.manager.Connect(connectCfg)
		if err != nil {
			emitEvent(SSHConnectEvent{
				Type:       "error",
				Error:      err.Error(),
				AuthFailed: isSSHAuthError(err),
			})
			return
		}

		emitEvent(SSHConnectEvent{Type: "connected", SessionID: sessionID})
	}()

	return connectionID, nil
}

// RespondAuthChallenge 前端响应 keyboard-interactive 认证质询
func (s *SSH) RespondAuthChallenge(challengeID string, answers []string) {
	if v, ok := s.pendingAuthResponses.Load(challengeID); ok {
		ch := v.(chan []string)
		select {
		case ch <- answers:
		default:
		}
	}
}

// RespondHostKeyVerify 前端响应主机密钥校验
// action: 0=AcceptAndSave, 1=AcceptOnce, 2=Reject
func (s *SSH) RespondHostKeyVerify(verifyID string, action int) {
	if v, ok := s.pendingHostKeyResponses.Load(verifyID); ok {
		ch := v.(chan ssh_svc.HostKeyAction)
		select {
		case ch <- ssh_svc.HostKeyAction(action):
		default:
		}
	}
}

// CancelSSHConnect 取消异步 SSH 连接
func (s *SSH) CancelSSHConnect(connectionID string) {
	if v, ok := s.pendingConnections.Load(connectionID); ok {
		cancel := v.(context.CancelFunc)
		cancel()
	}
}

// UpdateAssetPassword 更新资产的保存密码
func (s *SSH) UpdateAssetPassword(assetID int64, password string) error {
	asset, err := asset_svc.Asset().Get(i18n.Ctx(s.ctx, s.lang.Lang()), assetID)
	if err != nil {
		return err
	}
	sshCfg, err := asset.GetSSHConfig()
	if err != nil {
		return err
	}
	encrypted, err := credential_svc.Default().Encrypt(password)
	if err != nil {
		return err
	}
	sshCfg.Password = encrypted
	if err := asset.SetSSHConfig(sshCfg); err != nil {
		return err
	}
	return asset_svc.Asset().Update(i18n.Ctx(s.ctx, s.lang.Lang()), asset)
}

// TestSSHConnection 测试 SSH 连接（不创建终端会话）
func (s *SSH) TestSSHConnection(testID string, configJSON string, plainPassword string) error {
	var sshCfg asset_entity.SSHConfig
	if err := json.Unmarshal([]byte(configJSON), &sshCfg); err != nil {
		return fmt.Errorf("配置解析失败: %w", err)
	}

	parent, parentCancel := context.WithTimeout(i18n.Ctx(s.ctx, s.lang.Lang()), 10*time.Second)
	defer parentCancel()
	ctx, release := testreg.Begin(parent, testID)
	defer release()

	storedPassword, key, passphrase := s.resolveSSHCredentialsFull(&sshCfg)
	password := plainPassword
	if password == "" {
		password = storedPassword
	}

	// 处理 passphrase
	var keyPassphrase string
	if sshCfg.PrivateKeyPassphrase != "" {
		decrypted, err := credential_svc.Default().Decrypt(sshCfg.PrivateKeyPassphrase)
		if err == nil {
			keyPassphrase = decrypted
		} else {
			keyPassphrase = sshCfg.PrivateKeyPassphrase
		}
	} else {
		keyPassphrase = passphrase
	}

	connectCfg := ssh_svc.ConnectConfig{
		Host:              sshCfg.Host,
		Port:              sshCfg.Port,
		Username:          sshCfg.Username,
		AuthType:          sshCfg.AuthType,
		Password:          password,
		Key:               key,
		KeyPassphrase:     keyPassphrase,
		PrivateKeys:       sshCfg.PrivateKeys,
		Proxy:             sshCfg.Proxy,
		HostKeyVerifyFunc: ssh_svc.AutoTrustFirstRejectChangeVerifyFunc(),
	}

	// 解析跳板机
	if sshCfg.JumpHostID > 0 {
		jumpHosts, err := s.resolveJumpHosts(sshCfg.JumpHostID, 5)
		if err != nil {
			return fmt.Errorf("解析跳板机失败: %w", err)
		}
		connectCfg.JumpHosts = jumpHosts
	}

	return s.manager.TestConnection(ctx, connectCfg)
}

// WriteSSH 向 SSH 终端写入数据（base64 编码）
func (s *SSH) WriteSSH(sessionID string, dataB64 string) error {
	sess, ok := s.manager.GetSession(sessionID)
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
func (s *SSH) ResizeSSH(sessionID string, cols int, rows int) error {
	sess, ok := s.manager.GetSession(sessionID)
	if !ok {
		return fmt.Errorf("会话不存在: %s", sessionID)
	}
	return sess.Resize(cols, rows)
}

// GetSSHSyncState 返回会话当前的目录同步状态。
func (s *SSH) GetSSHSyncState(sessionID string) (ssh_svc.DirectorySyncState, error) {
	return s.manager.GetSessionSyncState(sessionID)
}

// ChangeSSHDirectory 请求当前终端切换到指定目录。
func (s *SSH) ChangeSSHDirectory(sessionID, targetPath string) error {
	sess, ok := s.manager.GetSession(sessionID)
	if !ok {
		return dirsync.Error(dirsync.CodeSessionNotFound)
	}

	state := sess.GetSyncState()
	resolvedPath := targetPath
	if strings.HasPrefix(resolvedPath, "/") {
		resolvedPath = path.Clean(resolvedPath)
		expectedPath, err := s.sftp.ResolveDirectory(sessionID, resolvedPath)
		if err != nil {
			return err
		}
		if !state.Supported || !state.CwdKnown || !state.PromptReady || !state.PromptClean {
			return sess.ChangeDirectoryDirect(resolvedPath)
		}
		return sess.ChangeDirectoryTo(resolvedPath, expectedPath)
	}

	if !state.Supported {
		if err := sess.EnableSync(); err != nil {
			return err
		}
		state = sess.GetSyncState()
	}
	if !state.CwdKnown {
		return dirsync.Error(dirsync.CodeCwdUnknown)
	}
	if !state.PromptReady || !state.PromptClean {
		return dirsync.Error(dirsync.CodeBusy)
	}

	if !strings.HasPrefix(resolvedPath, "/") {
		resolvedPath = path.Join(state.Cwd, resolvedPath)
	}
	resolvedPath = path.Clean(resolvedPath)

	expectedPath, err := s.sftp.ResolveDirectory(sessionID, resolvedPath)
	if err != nil {
		return err
	}

	return sess.ChangeDirectoryTo(resolvedPath, expectedPath)
}

// EnableSSHSync 显式启用目录同步。
func (s *SSH) EnableSSHSync(sessionID string) error {
	sess, ok := s.manager.GetSession(sessionID)
	if !ok {
		return dirsync.Error(dirsync.CodeSessionNotFound)
	}
	return sess.EnableSync()
}

// SplitSSH 在已有会话的连接上创建新会话（分割窗格复用连接）
func (s *SSH) SplitSSH(existingSessionID string, cols, rows int) (string, error) {
	return s.manager.NewSessionFrom(existingSessionID, cols, rows,
		func(sid string, data []byte) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:data:"+sid, base64.StdEncoding.EncodeToString(data))
		},
		func(sid string) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:closed:"+sid, nil)
		},
		func(sid string, state ssh_svc.DirectorySyncState) {
			wailsRuntime.EventsEmit(s.ctx, "ssh:sync:"+sid, state)
		},
	)
}

// DisconnectSSH 断开 SSH 连接
func (s *SSH) DisconnectSSH(sessionID string) {
	s.manager.Disconnect(sessionID)
}

// GetSSHPoolConnections 返回连接池中的活跃连接信息（供前端展示）
func (s *SSH) GetSSHPoolConnections() []sshpool.PoolEntryInfo {
	if s.pool == nil {
		return nil
	}
	return s.pool.List()
}
