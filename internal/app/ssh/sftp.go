package ssh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/opskat/opskat/internal/service/sftp_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
	cssh "golang.org/x/crypto/ssh"
)

// --- SFTP 文件传输 ---

// sftpTransferEvent 是传输终态事件：在通用进度之上带本地路径，让前端能「打开所在目录」。
type sftpTransferEvent struct {
	transfer.Progress
	LocalPath string `json:"localPath,omitempty"`
}

// finalTransferEvent 把传输结果映射为终态事件：被取消（context.Canceled）单独成取消状态（transfer.StatusCancelled），
// 前端不必再从错误文案里推断。
func finalTransferEvent(transferID, localPath string, err error) sftpTransferEvent {
	ev := sftpTransferEvent{Progress: transfer.Progress{TransferID: transferID, Status: "done"}, LocalPath: localPath}
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		ev.Status = transfer.StatusCancelled
	default:
		ev.Status = "error"
		ev.Error = err.Error()
	}
	return ev
}

// SFTPGetwd 获取远程工作目录（用户 home）
func (s *SSH) SFTPGetwd(sessionID string) (string, error) {
	return s.sftp.Getwd(sessionID)
}

// SFTPListDir 列出远程目录内容
func (s *SSH) SFTPListDir(sessionID, dirPath string) ([]sftp_svc.FileEntry, error) {
	return s.sftp.ListDir(sessionID, dirPath)
}

// SFTPSelectUploadFile 弹出本地文件选择框，返回所选路径（取消时为空）。
// 上传本身走 SFTPUploadFile：前端要先检查目标是否同名，再决定传到哪里。
func (s *SSH) SFTPSelectUploadFile() (string, error) {
	localPath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件",
	})
	if err != nil {
		return "", fmt.Errorf("打开文件对话框失败: %w", err)
	}
	return localPath, nil
}

// SFTPSelectUploadDir 弹出本地目录选择框，返回所选路径（取消时为空）。
func (s *SSH) SFTPSelectUploadDir() (string, error) {
	localDir, err := wailsRuntime.OpenDirectoryDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件夹",
	})
	if err != nil {
		return "", fmt.Errorf("打开目录对话框失败: %w", err)
	}
	return localDir, nil
}

// SFTPStatTargets 检查一组远端目标路径是否已被占用，供上传/移动/粘贴前做同名冲突判断。
func (s *SSH) SFTPStatTargets(sessionID string, paths []string) ([]sftp_svc.TargetStat, error) {
	return s.sftp.StatTargets(sessionID, paths)
}

// SFTPDownload 下载文件
func (s *SSH) SFTPDownload(sessionID, remotePath string) (string, error) {
	defaultName := filepath.Base(remotePath)
	localPath, err := wailsRuntime.SaveFileDialog(s.ctx, wailsRuntime.SaveDialogOptions{
		DefaultFilename: defaultName,
		Title:           "保存到本地",
	})
	if err != nil {
		return "", fmt.Errorf("保存文件对话框失败: %w", err)
	}
	if localPath == "" {
		return "", nil
	}

	transferID := s.sftp.GenerateTransferID()
	go func() {
		err := s.sftp.Download(s.ctx, transferID, sessionID, remotePath, localPath, func(p transfer.Progress) {
			wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, p)
		})
		wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, finalTransferEvent(transferID, localPath, err))
	}()
	return transferID, nil
}

// SFTPDownloadDir 下载目录
func (s *SSH) SFTPDownloadDir(sessionID, remotePath string) (string, error) {
	localDir, err := wailsRuntime.OpenDirectoryDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择保存目录",
	})
	if err != nil {
		return "", fmt.Errorf("打开目录对话框失败: %w", err)
	}
	if localDir == "" {
		return "", nil
	}

	localDir = filepath.Join(localDir, filepath.Base(remotePath))

	transferID := s.sftp.GenerateTransferID()
	go func() {
		err := s.sftp.DownloadDir(s.ctx, transferID, sessionID, remotePath, localDir, func(p transfer.Progress) {
			wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, p)
		})
		wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, finalTransferEvent(transferID, localDir, err))
	}()
	return transferID, nil
}

// SFTPUploadFile 上传本地文件或目录到 remotePath（目标的完整路径，同名冲突已由调用方决定）。
func (s *SSH) SFTPUploadFile(sessionID, localPath, remotePath string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", localPath, err)
	}
	if remotePath == "" || strings.HasSuffix(remotePath, "/") {
		return "", fmt.Errorf("remote path must name the destination: %q", remotePath)
	}

	transferID := s.sftp.GenerateTransferID()
	emitProgress := func(p transfer.Progress) {
		wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, p)
	}
	emitDone := func(err error) {
		wailsRuntime.EventsEmit(s.ctx, "transfer:progress:"+transferID, finalTransferEvent(transferID, "", err))
	}

	go func() {
		if info.IsDir() {
			emitDone(s.sftp.UploadDir(s.ctx, transferID, sessionID, localPath, remotePath, emitProgress))
			return
		}
		emitDone(s.sftp.Upload(s.ctx, transferID, sessionID, localPath, remotePath, emitProgress))
	}()

	return transferID, nil
}

// SFTPCancelTransfer 取消传输
func (s *SSH) SFTPCancelTransfer(transferID string) {
	s.sftp.Cancel(transferID)
}

// SFTPDelete 删除远程文件或目录
func (s *SSH) SFTPDelete(sessionID, remotePath string, isDir bool) error {
	if isDir {
		return s.sftp.RemoveDir(sessionID, remotePath)
	}
	return s.sftp.Remove(sessionID, remotePath)
}

// SFTPMkdir 新建远程目录。
func (s *SSH) SFTPMkdir(sessionID, remotePath string) error {
	return s.sftp.Mkdir(sessionID, remotePath)
}

// SFTPCreateFile 新建空远程文件。
func (s *SSH) SFTPCreateFile(sessionID, remotePath string) error {
	return s.sftp.CreateFile(sessionID, remotePath)
}

// SFTPRename 重命名或移动远程路径。
func (s *SSH) SFTPRename(sessionID, oldPath, newPath string) error {
	return s.sftp.Rename(sessionID, oldPath, newPath)
}

// SFTPPaste 粘贴远程文件/目录剪贴板内容。
func (s *SSH) SFTPPaste(req sftp_svc.PasteRequest) error {
	ctx := s.ctx
	if ctx == nil {
		ctx = s.appCtx
	}
	return s.sftp.Paste(ctx, req)
}

// SFTPProperties 获取远程文件/目录属性。
// 目录递归统计有 5s 超时保护，超时时返回近似值并置 truncated=true。
func (s *SSH) SFTPProperties(sessionID, remotePath string) (sftp_svc.FileProperties, error) {
	base := s.ctx
	if base == nil {
		base = s.appCtx
	}
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	defer cancel()
	return s.sftp.Properties(ctx, sessionID, remotePath)
}

// SFTPApplyPermissions 修改远程权限与属主。
func (s *SSH) SFTPApplyPermissions(sessionID string, req sftp_svc.PermissionApplyRequest) error {
	return s.sftp.ApplyPermissions(sessionID, req)
}

// --- 本地 SSH 密钥发现 ---

// LocalSSHKeyInfo 本地 SSH 密钥信息
type LocalSSHKeyInfo struct {
	Path        string `json:"path"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"`
	IsEncrypted bool   `json:"isEncrypted"`
}

// ListLocalSSHKeys 扫描 ~/.ssh 目录，返回有效的私钥列表
func (s *SSH) ListLocalSSHKeys() ([]LocalSSHKeyInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户目录失败: %w", err)
	}
	sshDir := filepath.Join(homeDir, ".ssh")

	entries, err := os.ReadDir(sshDir)
	if err != nil {
		// ~/.ssh 不存在时返回空列表
		if os.IsNotExist(err) {
			return []LocalSSHKeyInfo{}, nil
		}
		return nil, fmt.Errorf("读取 .ssh 目录失败: %w", err)
	}

	skipFiles := map[string]bool{
		"known_hosts":     true,
		"known_hosts.old": true,
		"config":          true,
		"authorized_keys": true,
		"environment":     true,
	}

	var keys []LocalSSHKeyInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".pub") || skipFiles[name] || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".sock") {
			continue
		}

		fullPath := filepath.Join(sshDir, name)
		info, err := parseLocalSSHKey(fullPath)
		if err != nil {
			continue // 不是有效私钥，跳过
		}
		keys = append(keys, *info)
	}

	if keys == nil {
		keys = []LocalSSHKeyInfo{}
	}
	return keys, nil
}

// SelectSSHKeyFile 打开文件选择框选择密钥文件，默认定位到 ~/.ssh
func (s *SSH) SelectSSHKeyFile() (*LocalSSHKeyInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Default().Warn("get user home dir", zap.Error(err))
	}
	defaultDir := filepath.Join(homeDir, ".ssh")

	filePath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title:            "选择 SSH 私钥文件",
		DefaultDirectory: defaultDir,
	})
	if err != nil {
		return nil, fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if filePath == "" {
		return nil, nil
	}

	info, err := parseLocalSSHKey(filePath)
	if err != nil {
		return nil, fmt.Errorf("所选文件不是有效的 SSH 私钥: %w", err)
	}
	return info, nil
}

// parseLocalSSHKey 解析本地私钥文件，返回密钥信息
func parseLocalSSHKey(path string) (*LocalSSHKeyInfo, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is from user file dialog
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	signer, err := cssh.ParsePrivateKey(data)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "password protected") ||
			strings.Contains(errStr, "encrypted") ||
			strings.Contains(errStr, "passphrase") {
			keyType := "unknown"
			if strings.Contains(string(data), "OPENSSH PRIVATE KEY") {
				keyType = "ssh-ed25519"
			} else if strings.Contains(string(data), "RSA PRIVATE KEY") {
				keyType = "ssh-rsa"
			} else if strings.Contains(string(data), "EC PRIVATE KEY") {
				keyType = "ecdsa-sha2-nistp256"
			} else if strings.Contains(string(data), "DSA PRIVATE KEY") {
				keyType = "ssh-dss"
			}
			return &LocalSSHKeyInfo{
				Path:        path,
				KeyType:     keyType,
				Fingerprint: "",
				IsEncrypted: true,
			}, nil
		}
		return nil, err
	}

	pubKey := signer.PublicKey()
	fingerprint := cssh.FingerprintSHA256(pubKey)
	keyType := pubKey.Type()

	return &LocalSSHKeyInfo{
		Path:        path,
		KeyType:     keyType,
		Fingerprint: fingerprint,
		IsEncrypted: false,
	}, nil
}
