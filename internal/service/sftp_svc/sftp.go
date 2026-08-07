package sftp_svc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opskat/opskat/internal/pkg/dirsync"
	"github.com/opskat/opskat/internal/pkg/transfer"
	"github.com/opskat/opskat/internal/service/ssh_svc"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
)

// MaxReadFileSize limits full-file reads used by desktop features such as external edit.
const MaxReadFileSize int64 = 10 * 1024 * 1024

// Service SFTP 文件传输服务
type Service struct {
	sshManager              *ssh_svc.Manager
	clients                 sync.Map // sessionID -> *sftp.Client
	cancels                 sync.Map // transferID -> context.CancelFunc
	maxReadFileSizeProvider func() int64
}

// NewService 创建 SFTP 服务
func NewService(sshManager *ssh_svc.Manager) *Service {
	return &Service{
		sshManager: sshManager,
		maxReadFileSizeProvider: func() int64 {
			return MaxReadFileSize
		},
	}
}

func (s *Service) SetMaxReadFileSizeProvider(provider func() int64) {
	if provider == nil {
		s.maxReadFileSizeProvider = func() int64 {
			return MaxReadFileSize
		}
		return
	}
	s.maxReadFileSizeProvider = provider
}

func (s *Service) maxReadFileSize() int64 {
	if s == nil || s.maxReadFileSizeProvider == nil {
		return MaxReadFileSize
	}
	limit := s.maxReadFileSizeProvider()
	if limit <= 0 {
		return MaxReadFileSize
	}
	return limit
}

// GenerateTransferID 生成唯一传输 ID
func (s *Service) GenerateTransferID() string {
	return transfer.GenerateID("sftp")
}

// getSFTPClient 获取或创建 SFTP 客户端（懒加载，高性能配置）
func (s *Service) getSFTPClient(sessionID string) (*sftp.Client, error) {
	if v, ok := s.clients.Load(sessionID); ok {
		client := v.(*sftp.Client)
		// 检查是否仍然可用
		if _, err := client.Getwd(); err == nil {
			return client, nil
		}
		// 已失效，移除
		s.clients.Delete(sessionID)
		if err := client.Close(); err != nil {
			logger.Default().Warn("close stale client", zap.String("sessionID", sessionID), zap.Error(err))
		}
	}

	sess, ok := s.sshManager.GetSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("SSH 会话不存在: %s", sessionID)
	}
	if sess.IsClosed() {
		return nil, fmt.Errorf("SSH 会话已关闭: %s", sessionID)
	}

	//并发请求 + 并发写
	client, err := sftp.NewClient(sess.Client(), sftp.UseConcurrentReads(true), sftp.UseConcurrentWrites(true))
	if err != nil {
		return nil, fmt.Errorf("创建 SFTP 客户端失败: %w", err)
	}

	s.clients.Store(sessionID, client)
	return client, nil
}

// Getwd 获取远程工作目录（用户 home）
func (s *Service) Getwd(sessionID string) (string, error) {
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return "", err
	}
	return sftpClient.Getwd()
}

// ResolveDirectory validates that a remote directory exists and returns its canonical path.
func (s *Service) ResolveDirectory(sessionID, dirPath string) (string, error) {
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return "", err
	}

	info, err := sftpClient.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", dirsync.Error(dirsync.CodeNotFound)
		}
		return "", dirsync.Error(dirsync.CodeAccessDenied)
	}
	if !info.IsDir() {
		return "", dirsync.Error(dirsync.CodeNotDirectory)
	}

	if realPath, realPathErr := sftpClient.RealPath(dirPath); realPathErr == nil && realPath != "" {
		return realPath, nil
	}
	return dirPath, nil
}

// ValidateDirectory 校验远程目录存在且可访问。
func (s *Service) ValidateDirectory(sessionID, dirPath string) error {
	_, err := s.ResolveDirectory(sessionID, dirPath)
	return err
}

// FileEntry 远程文件/目录条目
type FileEntry struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"isDir"`
	ModTime int64  `json:"modTime"` // Unix timestamp
}

// RemoteFileInfo 是远程文件的基础元信息。
type RemoteFileInfo struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Mode     uint32 `json:"mode"`
	ModTime  int64  `json:"modTime"`
	IsDir    bool   `json:"isDir"`
	Regular  bool   `json:"regular"`
	SHA256   string `json:"sha256,omitempty"`
	RealPath string `json:"realPath,omitempty"`
}

type remoteAtomicWriter interface {
	io.Writer
	Close() error
}

type remoteAtomicClient interface {
	OpenFile(path string, f int) (remoteAtomicWriter, error)
	Stat(path string) (os.FileInfo, error)
	Chmod(path string, mode os.FileMode) error
	Remove(path string) error
	Rename(oldname, newname string) error
	PosixRename(oldname, newname string) error
}

type sftpAtomicClient struct {
	client *sftp.Client
}

func (c sftpAtomicClient) OpenFile(path string, f int) (remoteAtomicWriter, error) {
	return c.client.OpenFile(path, f)
}

func (c sftpAtomicClient) Stat(path string) (os.FileInfo, error) {
	return c.client.Stat(path)
}

func (c sftpAtomicClient) Chmod(path string, mode os.FileMode) error {
	return c.client.Chmod(path, mode)
}

func (c sftpAtomicClient) Remove(path string) error {
	return c.client.Remove(path)
}

func (c sftpAtomicClient) Rename(oldname, newname string) error {
	return c.client.Rename(oldname, newname)
}

func (c sftpAtomicClient) PosixRename(oldname, newname string) error {
	return c.client.PosixRename(oldname, newname)
}

// ListDir 列出远程目录内容
func (s *Service) ListDir(sessionID, dirPath string) ([]FileEntry, error) {
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return nil, err
	}

	infos, err := sftpClient.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("读取远程目录失败: %w", err)
	}

	// 排序：目录在前，文件在后，各自按名称排序
	var dirs, files []FileEntry
	for _, info := range infos {
		isDir := info.IsDir()
		if info.Mode()&os.ModeSymlink != 0 {
			if targetInfo, statErr := sftpClient.Stat(path.Join(dirPath, info.Name())); statErr == nil {
				isDir = targetInfo.IsDir()
			}
		}
		entry := FileEntry{
			Name:    info.Name(),
			Size:    info.Size(),
			IsDir:   isDir,
			ModTime: info.ModTime().Unix(),
		}
		if isDir {
			dirs = append(dirs, entry)
		} else {
			files = append(files, entry)
		}
	}

	result := make([]FileEntry, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)
	return result, nil
}

// Stat 返回远程路径元信息。
func (s *Service) Stat(sessionID, remotePath string) (*RemoteFileInfo, error) {
	// external edit 需要一份“可比较、可恢复”的远端基线，
	// 所以这里除了常规 stat，还尽量补齐 realPath，避免符号链接或相对路径把同一文件拆成多份会话。
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return nil, err
	}
	return statWithClient(sftpClient, remotePath)
}

func statWithClient(sftpClient *sftp.Client, remotePath string) (*RemoteFileInfo, error) {
	info, err := sftpClient.Stat(remotePath)
	if err != nil {
		return nil, fmt.Errorf("获取远程文件信息失败: %w", err)
	}

	realPath := remotePath
	if rp, realPathErr := sftpClient.RealPath(remotePath); realPathErr == nil && rp != "" {
		realPath = rp
	}

	return &RemoteFileInfo{
		Path:     remotePath,
		Size:     info.Size(),
		Mode:     uint32(info.Mode()),
		ModTime:  info.ModTime().Unix(),
		IsDir:    info.IsDir(),
		Regular:  info.Mode().IsRegular(),
		RealPath: realPath,
	}, nil
}

// ReadFile 读取远程文件全部字节。
func (s *Service) ReadFile(sessionID, remotePath string) ([]byte, *RemoteFileInfo, error) {
	// 读取阶段直接附带内容哈希，减少上层再次遍历字节流的机会，
	// 让 external edit 可以把“读取基线”和“冲突比较基线”绑定到同一次远端快照上。
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return nil, nil, err
	}

	limit := s.maxReadFileSize()
	info, err := statWithClient(sftpClient, remotePath)
	if err != nil {
		return nil, nil, err
	}
	if info.Size > limit {
		return nil, nil, fmt.Errorf("远程文件过大，无法完整读取: %s (%d bytes > %d bytes)", remotePath, info.Size, limit)
	}

	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return nil, nil, fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer func() {
		if err := remoteFile.Close(); err != nil {
			logger.Default().Warn("close remote file", zap.String("path", remotePath), zap.Error(err))
		}
	}()

	data, err := io.ReadAll(io.LimitReader(remoteFile, limit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取远程文件失败: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, nil, fmt.Errorf("远程文件读取过程中超过大小上限: %s (%d bytes > %d bytes)", remotePath, len(data), limit)
	}

	sum := sha256.Sum256(data)
	info.SHA256 = fmt.Sprintf("%x", sum[:])
	return data, info, nil
}

// WriteFile 原子替换远程文件内容。
func (s *Service) WriteFile(sessionID, remotePath string, data []byte) error {
	// 外部编辑回写不能复用普通上传语义。
	// 这里强制走原子替换，避免编辑器保存中途断开时把远端文本文件截成半份。
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	return writeFileAtomically(sftpAtomicClient{client: sftpClient}, remotePath, data)
}

// Upload 上传单个文件（并发写 + 远程原子替换）
func (s *Service) Upload(ctx context.Context, transferID, sessionID, localPath, remotePath string, onProgress func(transfer.Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancels.Store(transferID, cancel)
	defer func() {
		s.cancels.Delete(transferID)
		cancel()
	}()

	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}

	localFile, err := os.Open(localPath) //nolint:gosec // file path from user config
	if err != nil {
		return fmt.Errorf("打开本地文件失败: %w", err)
	}
	defer func() {
		if err := localFile.Close(); err != nil {
			logger.Default().Warn("close local file", zap.String("path", localPath), zap.Error(err))
		}
	}()

	stat, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("获取文件信息失败: %w", err)
	}

	return s.uploadFileAtomic(ctx, transferID, sftpClient, localFile, remotePath, stat.Size(), 1, filepath.Base(remotePath), 0, stat.Size(), 0, onProgress)
}

// Download 下载单个文件（并发读 + 本地原子替换）
func (s *Service) Download(ctx context.Context, transferID, sessionID, remotePath, localPath string, onProgress func(transfer.Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancels.Store(transferID, cancel)
	defer func() {
		s.cancels.Delete(transferID)
		cancel()
	}()

	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}

	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return fmt.Errorf("打开远程文件失败: %w", err)
	}
	defer func() {
		if err := remoteFile.Close(); err != nil {
			logger.Default().Warn("close remote file", zap.String("path", remotePath), zap.Error(err))
		}
	}()

	stat, err := remoteFile.Stat()
	if err != nil {
		return fmt.Errorf("获取远程文件信息失败: %w", err)
	}

	return s.downloadFileAtomic(ctx, transferID, remoteFile, localPath, stat.Size(), 1, filepath.Base(remotePath), 0, 0, onProgress)
}

// UploadDir 上传目录
func (s *Service) UploadDir(ctx context.Context, transferID, sessionID, localDir, remoteDir string, onProgress func(transfer.Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancels.Store(transferID, cancel)
	defer func() {
		s.cancels.Delete(transferID)
		cancel()
	}()

	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}

	// 扫描阶段：统计文件数和总大小
	var filesTotal int
	var bytesTotal int64
	if err := filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.IsDir() {
			filesTotal++
			info, err := d.Info()
			if err != nil {
				return err
			}
			bytesTotal += info.Size()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("扫描本地目录失败: %w", err)
	}

	// 传输阶段
	var filesCompleted int
	var bytesDone int64

	return filepath.WalkDir(localDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			logger.Default().Warn("compute relative path", zap.String("base", localDir), zap.String("path", path), zap.Error(err))
			return err
		}
		remoteFull := remoteDir + "/" + filepath.ToSlash(relPath)

		if d.IsDir() {
			return sftpClient.MkdirAll(remoteFull)
		}

		localFile, err := os.Open(path) //nolint:gosec
		if err != nil {
			return err
		}
		defer func() {
			if err := localFile.Close(); err != nil {
				logger.Default().Warn("close local file", zap.String("path", path), zap.Error(err))
			}
		}()

		stat, err := localFile.Stat()
		if err != nil {
			return err
		}

		if err := s.uploadFileAtomic(ctx, transferID, sftpClient, localFile, remoteFull, stat.Size(), filesTotal, relPath, bytesDone, stat.Size(), filesCompleted, onProgress); err != nil {
			return err
		}
		bytesDone += stat.Size()
		filesCompleted++
		return nil
	})
}

// DownloadDir 下载目录
func (s *Service) DownloadDir(ctx context.Context, transferID, sessionID, remoteDir, localDir string, onProgress func(transfer.Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	s.cancels.Store(transferID, cancel)
	defer func() {
		s.cancels.Delete(transferID)
		cancel()
	}()

	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}

	// 扫描阶段：递归统计远程目录
	type fileEntry struct {
		remotePath string
		size       int64
		isDir      bool
	}
	var entries []fileEntry
	var bytesTotal int64
	var filesTotal int

	var walk func(dir string) error
	walk = func(dir string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		infos, err := sftpClient.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, info := range infos {
			fullPath := dir + "/" + info.Name()
			if info.IsDir() {
				entries = append(entries, fileEntry{remotePath: fullPath, isDir: true})
				if err := walk(fullPath); err != nil {
					return err
				}
			} else {
				entries = append(entries, fileEntry{remotePath: fullPath, size: info.Size()})
				bytesTotal += info.Size()
				filesTotal++
			}
		}
		return nil
	}

	// 先创建根目录
	if err := os.MkdirAll(localDir, 0755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}
	if err := walk(remoteDir); err != nil {
		return fmt.Errorf("扫描远程目录失败: %w", err)
	}

	// 传输阶段
	var filesCompleted int
	var bytesDone int64

	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// 计算相对路径
		relPath := entry.remotePath[len(remoteDir):]
		localFull := filepath.Join(localDir, filepath.FromSlash(relPath))

		if entry.isDir {
			if err := os.MkdirAll(localFull, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(localFull), 0755); err != nil {
			return err
		}

		remoteFile, err := sftpClient.Open(entry.remotePath)
		if err != nil {
			return err
		}

		err = s.downloadFileAtomic(ctx, transferID, remoteFile, localFull, entry.size, filesTotal, relPath, bytesDone, filesCompleted, onProgress)
		_ = remoteFile.Close()
		if err != nil {
			return err
		}
		bytesDone += entry.size
		filesCompleted++
	}

	return nil
}

// Cancel 取消传输
func (s *Service) Cancel(transferID string) {
	if v, ok := s.cancels.Load(transferID); ok {
		v.(context.CancelFunc)()
	}
}

// CleanupSession 清理 SSH 会话关联的 SFTP 客户端
func (s *Service) CleanupSession(sessionID string) {
	if v, ok := s.clients.LoadAndDelete(sessionID); ok {
		if err := v.(*sftp.Client).Close(); err != nil {
			logger.Default().Warn("close client", zap.String("sessionID", sessionID), zap.Error(err))
		}
	}
}

// Remove 删除单个文件
func (s *Service) Remove(sessionID, path string) error {
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	return sftpClient.Remove(path)
}

// RemoveDir 递归删除目录
func (s *Service) RemoveDir(sessionID, path string) error {
	sftpClient, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	return s.removeDirRecursive(sftpClient, path)
}

func (s *Service) removeDirRecursive(client *sftp.Client, path string) error {
	entries, err := client.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fullPath := path + "/" + entry.Name()
		if entry.IsDir() {
			if err := s.removeDirRecursive(client, fullPath); err != nil {
				return err
			}
		} else {
			if err := client.Remove(fullPath); err != nil {
				return err
			}
		}
	}
	return client.RemoveDirectory(path)
}

// uploadFileAtomic：本地 → 远程临时文件（并发写）→ 原子 rename
func (s *Service) uploadFileAtomic(ctx context.Context, transferID string, client *sftp.Client, local io.Reader, remotePath string, fileSize int64, filesTotal int, currentFile string, baseBytesDone int64, bytesTotal int64, filesCompleted int, onProgress func(transfer.Progress)) error {
	targetMode, targetExists, err := statRemoteRegularFile(sftpAtomicClient{client: client}, remotePath)
	if err != nil {
		return err
	}

	tempPath := buildRemoteTempPath(remotePath, "part")
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			cleanupRemotePath(sftpAtomicClient{client: client}, tempPath)
		}
	}()

	// O_EXCL 避免并发冲突；不直接 TRUNCATE 目标文件
	remoteFile, err := client.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fmt.Errorf("创建远程临时文件失败: %w", err)
	}
	if bytesTotal <= 0 {
		bytesTotal = baseBytesDone + fileSize
	}

	pr := &progressReader{
		ctx:            ctx,
		r:              local,
		size:           fileSize,
		baseBytesDone:  baseBytesDone,
		bytesTotal:     bytesTotal, // ← 关键
		transferID:     transferID,
		currentFile:    currentFile,
		filesTotal:     filesTotal,
		filesCompleted: filesCompleted,
		onProgress:     onProgress,
		reporter:       transfer.NewReporter(onProgress),
	}

	written, err := remoteFile.ReadFrom(pr)
	closeErr := remoteFile.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("关闭远程临时文件失败: %w", closeErr)
	}

	// 强制最终进度（并发读盘太快时中间回调可能被节流掉）
	if onProgress != nil {
		onProgress(transfer.Progress{
			TransferID:     transferID,
			Status:         "progress",
			CurrentFile:    currentFile,
			FilesCompleted: filesCompleted,
			FilesTotal:     filesTotal,
			BytesDone:      baseBytesDone + written,
			BytesTotal:     bytesTotal,
		})
	}

	if targetExists {
		if err := client.Chmod(tempPath, targetMode); err != nil {
			return fmt.Errorf("同步远程文件权限失败: %w", err)
		}
		if err := client.PosixRename(tempPath, remotePath); err == nil {
			cleanupTemp = false
			return nil
		} else if !isSFTPOpUnsupported(err) {
			return fmt.Errorf("原子替换远程文件失败: %w", err)
		}

		// 不支持 PosixRename 时回退 backup 路径
		backupPath := buildRemoteTempPath(remotePath, "bak")
		if err := client.Rename(remotePath, backupPath); err != nil {
			return fmt.Errorf("创建远程备份文件失败: %w", err)
		}
		if err := client.Rename(tempPath, remotePath); err != nil {
			_ = client.Rename(backupPath, remotePath)
			return fmt.Errorf("替换远程文件失败: %w", err)
		}
		_ = client.Remove(backupPath)
		cleanupTemp = false
		return nil
	}

	if err := client.Rename(tempPath, remotePath); err != nil {
		return fmt.Errorf("提交远程临时文件失败: %w", err)
	}
	cleanupTemp = false
	return nil
}

// downloadFileAtomic：远程 → 本地临时文件（并发读）→ 原子 rename
func (s *Service) downloadFileAtomic(ctx context.Context, transferID string, remote *sftp.File, localPath string, fileSize int64, filesTotal int, currentFile string, baseBytesDone int64, filesCompleted int, onProgress func(transfer.Progress)) error {
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}

	base := filepath.Base(localPath)
	tempPath := filepath.Join(dir, fmt.Sprintf(".%s.opskat-part-%d", base, time.Now().UnixNano()))

	localFile, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec
	if err != nil {
		return fmt.Errorf("创建本地临时文件失败: %w", err)
	}
	committed := false
	defer func() {
		_ = localFile.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()

	pw := &progressWriter{
		ctx:            ctx,
		w:              localFile,
		transferID:     transferID,
		currentFile:    currentFile,
		filesTotal:     filesTotal,
		filesCompleted: filesCompleted,
		baseBytesDone:  baseBytesDone,
		totalBytes:     fileSize + baseBytesDone, // 目录场景下 total 由上层控制；单文件时 base=0
		fileTotal:      fileSize,
		onProgress:     onProgress,
		reporter:       transfer.NewReporter(onProgress),
	}
	// 单文件时 BytesTotal 应是 fileSize；目录时上层用 base 累加
	if baseBytesDone == 0 && filesTotal <= 1 {
		pw.totalBytes = fileSize
	}

	// WriteTo 在 UseConcurrentReads 时走并发读流水线
	_, err = remote.WriteTo(pw)
	if err != nil {
		return err
	}
	if err := localFile.Sync(); err != nil {
		return fmt.Errorf("同步本地文件失败: %w", err)
	}
	if err := localFile.Close(); err != nil {
		return fmt.Errorf("关闭本地临时文件失败: %w", err)
	}

	if err := os.Rename(tempPath, localPath); err != nil {
		return fmt.Errorf("提交本地文件失败: %w", err)
	}
	committed = true
	return nil
}

// progressReader：包装本地读，提供 Size() 让 ReadFrom 走并发写，并稳定上报进度
type progressReader struct {
	ctx            context.Context
	r              io.Reader
	size           int64 // 当前文件大小
	bytesDone      int64
	baseBytesDone  int64
	bytesTotal     int64 // 整次传输总字节
	transferID     string
	currentFile    string
	filesTotal     int
	filesCompleted int
	onProgress     func(transfer.Progress)
	reporter       *transfer.Reporter
	lastReport     time.Time
}

func (p *progressReader) Size() int64 { return p.size }

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	if n > 0 {
		p.bytesDone += int64(n)
		p.report(false)
	}
	if errors.Is(err, io.EOF) {
		p.report(true) // EOF 强制再报一次
	}
	return n, err
}

func (p *progressReader) report(force bool) {
	now := time.Now()
	if !force && now.Sub(p.lastReport) < 50*time.Millisecond {
		return
	}
	p.lastReport = now

	total := p.bytesTotal
	if total <= 0 {
		total = p.baseBytesDone + p.size
	}

	prog := transfer.Progress{
		TransferID:     p.transferID,
		Status:         "progress",
		CurrentFile:    p.currentFile,
		FilesCompleted: p.filesCompleted,
		FilesTotal:     p.filesTotal,
		BytesDone:      p.baseBytesDone + p.bytesDone,
		BytesTotal:     total,
	}
	if p.reporter != nil {
		p.reporter.Report(prog)
	} else if p.onProgress != nil {
		p.onProgress(prog)
	}
}

// progressWriter：包装本地写，上报进度
type progressWriter struct {
	ctx            context.Context
	w              io.Writer
	bytesDone      int64
	baseBytesDone  int64
	totalBytes     int64
	fileTotal      int64
	transferID     string
	currentFile    string
	filesTotal     int
	filesCompleted int
	onProgress     func(transfer.Progress)
	reporter       *transfer.Reporter
	lastReport     time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.w.Write(b)
	if n > 0 {
		p.bytesDone += int64(n)
		now := time.Now()
		if now.Sub(p.lastReport) >= 100*time.Millisecond || err != nil {
			total := p.totalBytes
			if total == 0 {
				total = p.baseBytesDone + p.fileTotal
			}
			p.reporter.Report(transfer.Progress{
				TransferID:     p.transferID,
				Status:         "progress",
				CurrentFile:    p.currentFile,
				FilesCompleted: p.filesCompleted,
				FilesTotal:     p.filesTotal,
				BytesDone:      p.baseBytesDone + p.bytesDone,
				BytesTotal:     total,
			})
			p.lastReport = now
		}
	}
	return n, err
}

func writeFileAtomically(client remoteAtomicClient, remotePath string, data []byte) error {
	// 优先写临时文件再切换目标文件名：
	// 成功时远端始终只会看到“旧版本”或“完整新版本”，不会暴露半写入状态。
	targetMode, targetExists, err := statRemoteRegularFile(client, remotePath)
	if err != nil {
		return err
	}

	tempPath := buildRemoteTempPath(remotePath, "tmp")
	backupPath := ""
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			cleanupRemotePath(client, tempPath)
		}
		if backupPath != "" {
			cleanupRemotePath(client, backupPath)
		}
	}()

	tempFile, err := client.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("创建远程临时文件失败: %w", err)
	}
	if _, err := io.Copy(tempFile, bytes.NewReader(data)); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("写入远程临时文件失败: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("关闭远程临时文件失败: %w", err)
	}

	if targetExists {
		if err := client.Chmod(tempPath, targetMode); err != nil {
			return fmt.Errorf("同步远程文件权限失败: %w", err)
		}
		if err := client.PosixRename(tempPath, remotePath); err == nil {
			cleanupTemp = false
			return nil
		} else if !isSFTPOpUnsupported(err) {
			return fmt.Errorf("原子替换远程文件失败: %w", err)
		}

		// 某些 SFTP 服务端不支持 PosixRename。
		// 这里回退到“先备份旧文件，再切换新文件，再尽力恢复”的兼容路径，把副作用控制在单个目标文件范围内。
		backupPath = buildRemoteTempPath(remotePath, "bak")
		if err := client.Rename(remotePath, backupPath); err != nil {
			return fmt.Errorf("创建远程备份文件失败: %w", err)
		}
		if err := client.Rename(tempPath, remotePath); err != nil {
			restoreErr := client.Rename(backupPath, remotePath)
			if restoreErr != nil {
				return fmt.Errorf("替换远程文件失败且恢复原文件失败: %w; restore: %v", err, restoreErr)
			}
			return fmt.Errorf("替换远程文件失败，已恢复原文件: %w", err)
		}
		if err := client.Remove(backupPath); err != nil && !os.IsNotExist(err) {
			logger.Default().Warn("cleanup remote backup file", zap.String("path", backupPath), zap.Error(err))
		}
		cleanupTemp = false
		backupPath = ""
		return nil
	}

	if err := client.Rename(tempPath, remotePath); err != nil {
		return fmt.Errorf("提交远程临时文件失败: %w", err)
	}
	cleanupTemp = false
	return nil
}

func statRemoteRegularFile(client remoteAtomicClient, remotePath string) (os.FileMode, bool, error) {
	// 这里只允许覆盖常规文件。
	// 目录、管道或其他特殊节点一旦进入原子替换流程，失败恢复和权限继承语义都会变得不可控。
	info, err := client.Stat(remotePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("获取远程文件信息失败: %w", err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return 0, false, fmt.Errorf("远程路径不是常规文件: %s (mode=%s, perm=%#o, isDir=%t)", remotePath, info.Mode(), info.Mode().Perm(), info.IsDir())
	}
	return info.Mode().Perm(), true, nil
}

func buildRemoteTempPath(remotePath, suffix string) string {
	dir := path.Dir(remotePath)
	base := path.Base(remotePath)
	token := fmt.Sprintf(".%s.opskat-%s-%d", base, suffix, time.Now().UnixNano())
	return path.Join(dir, token)
}

func cleanupRemotePath(client remoteAtomicClient, remotePath string) {
	if strings.TrimSpace(remotePath) == "" {
		return
	}
	if err := client.Remove(remotePath); err != nil && !os.IsNotExist(err) {
		logger.Default().Warn("cleanup remote temp file", zap.String("path", remotePath), zap.Error(err))
	}
}

func isSFTPOpUnsupported(err error) bool {
	// 不同服务端对“操作不支持”的返回并不统一，
	// 这里同时兼容结构化状态码和文本兜底，避免因为供应商差异错过安全回退路径。
	if err == nil {
		return false
	}
	var statusErr *sftp.StatusError
	if errors.As(err, &statusErr) {
		return statusErr.FxCode() == sftp.ErrSSHFxOpUnsupported
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "op unsupported") || strings.Contains(text, "unsupported")
}
