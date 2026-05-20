package ssh

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opskat/opskat/internal/service/sftp_svc"

	"github.com/cago-frame/cago/pkg/logger"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
	cssh "golang.org/x/crypto/ssh"
)

// --- SFTP 文件传输 ---

// SFTPGetwd 获取远程工作目录（用户 home）
func (s *SSH) SFTPGetwd(sessionID string) (string, error) {
	return s.sftp.Getwd(sessionID)
}

// SFTPListDir 列出远程目录内容
func (s *SSH) SFTPListDir(sessionID, dirPath string) ([]sftp_svc.FileEntry, error) {
	return s.sftp.ListDir(sessionID, dirPath)
}

// SFTPUpload 上传文件：弹出本地文件选择 → 上传到 remotePath
func (s *SSH) SFTPUpload(sessionID, remotePath string) (string, error) {
	localPath, err := wailsRuntime.OpenFileDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件",
	})
	if err != nil {
		return "", fmt.Errorf("打开文件对话框失败: %w", err)
	}
	if localPath == "" {
		return "", nil // 用户取消
	}

	if strings.HasSuffix(remotePath, "/") {
		remotePath += filepath.Base(localPath)
	}

	transferID := s.sftp.GenerateTransferID()
	go func() {
		err := s.sftp.Upload(s.ctx, transferID, sessionID, localPath, remotePath, func(p sftp_svc.TransferProgress) {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, p)
		})
		if err != nil {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
				TransferID: transferID,
				Status:     "error",
				Error:      err.Error(),
			})
			return
		}
		wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
			TransferID: transferID,
			Status:     "done",
		})
	}()
	return transferID, nil
}

// SFTPUploadDir 上传目录
func (s *SSH) SFTPUploadDir(sessionID, remotePath string) (string, error) {
	localDir, err := wailsRuntime.OpenDirectoryDialog(s.ctx, wailsRuntime.OpenDialogOptions{
		Title: "选择上传文件夹",
	})
	if err != nil {
		return "", fmt.Errorf("打开目录对话框失败: %w", err)
	}
	if localDir == "" {
		return "", nil
	}

	if strings.HasSuffix(remotePath, "/") {
		remotePath += filepath.Base(localDir)
	} else {
		remotePath += "/" + filepath.Base(localDir)
	}

	transferID := s.sftp.GenerateTransferID()
	go func() {
		err := s.sftp.UploadDir(s.ctx, transferID, sessionID, localDir, remotePath, func(p sftp_svc.TransferProgress) {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, p)
		})
		if err != nil {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
				TransferID: transferID,
				Status:     "error",
				Error:      err.Error(),
			})
			return
		}
		wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
			TransferID: transferID,
			Status:     "done",
		})
	}()
	return transferID, nil
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
		err := s.sftp.Download(s.ctx, transferID, sessionID, remotePath, localPath, func(p sftp_svc.TransferProgress) {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, p)
		})
		if err != nil {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
				TransferID: transferID,
				Status:     "error",
				Error:      err.Error(),
			})
			return
		}
		wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
			TransferID: transferID,
			Status:     "done",
		})
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
		err := s.sftp.DownloadDir(s.ctx, transferID, sessionID, remotePath, localDir, func(p sftp_svc.TransferProgress) {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, p)
		})
		if err != nil {
			wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
				TransferID: transferID,
				Status:     "error",
				Error:      err.Error(),
			})
			return
		}
		wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, sftp_svc.TransferProgress{
			TransferID: transferID,
			Status:     "done",
		})
	}()
	return transferID, nil
}

// SFTPUploadFile 直接上传本地文件或目录（不弹对话框，用于拖拽上传）
func (s *SSH) SFTPUploadFile(sessionID, localPath, remotePath string) (string, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", localPath, err)
	}

	transferID := s.sftp.GenerateTransferID()
	emitProgress := func(p sftp_svc.TransferProgress) {
		wailsRuntime.EventsEmit(s.ctx, "sftp:progress:"+transferID, p)
	}
	emitDone := func(err error) {
		if err != nil {
			emitProgress(sftp_svc.TransferProgress{TransferID: transferID, Status: "error", Error: err.Error()})
			return
		}
		emitProgress(sftp_svc.TransferProgress{TransferID: transferID, Status: "done"})
	}

	if info.IsDir() {
		dirRemotePath := remotePath
		if strings.HasSuffix(dirRemotePath, "/") {
			dirRemotePath += filepath.Base(localPath)
		} else {
			dirRemotePath += "/" + filepath.Base(localPath)
		}
		go func() {
			emitDone(s.sftp.UploadDir(s.ctx, transferID, sessionID, localPath, dirRemotePath, emitProgress))
		}()
	} else {
		fileRemotePath := remotePath
		if strings.HasSuffix(fileRemotePath, "/") {
			fileRemotePath += filepath.Base(localPath)
		}
		go func() {
			emitDone(s.sftp.Upload(s.ctx, transferID, sessionID, localPath, fileRemotePath, emitProgress))
		}()
	}

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
