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

	client, err := sftp.NewClient(sess.Client(), sftpClientOptions()...)
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

// sftpClientOptions 是所有 SFTP 客户端共用的选项，测试里的进程内客户端也走这一份，
// 以免"生产开了并发写、测试没开"这种最容易漏掉的差异。
//
// UseConcurrentWrites 让 File.ReadFrom 走并发写流水线，在高延迟链路上显著提升上传吞吐。
// 代价见 pkg/sftp 文档：写入出错时文件长度可能长于成功写入的部分（中间留空洞），
// 因此每条写入路径都必须在失败时清理或截断目标 —— 这是启用该选项的前置条件，不是可选项。
//
// 不显式设置 UseConcurrentReads：pkg/sftp 文档写明它默认即为开启，传 true 是空操作。
func sftpClientOptions() []sftp.ClientOption {
	return []sftp.ClientOption{sftp.UseConcurrentWrites(true)}
}

type remoteAtomicWriter interface {
	io.Writer
	io.ReaderFrom
	Truncate(size int64) error
	Close() error
}

// remoteAtomicClient 是"把一个文件安全地换成新内容"所需的远端文件系统操作集合。
// 上传与远程文件编辑都经由它，因此 PosixRename 不被支持时的回退路径也能用 fake 驱动。
type remoteAtomicClient interface {
	OpenFile(path string, f int) (remoteAtomicWriter, error)
	Stat(path string) (os.FileInfo, error)
	Lstat(path string) (os.FileInfo, error)
	ReadLink(path string) (string, error)
	Chmod(path string, mode os.FileMode) error
	Chown(path string, uid, gid int) error
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

func (c sftpAtomicClient) Lstat(path string) (os.FileInfo, error) {
	return c.client.Lstat(path)
}

func (c sftpAtomicClient) ReadLink(path string) (string, error) {
	return c.client.ReadLink(path)
}

func (c sftpAtomicClient) Chmod(path string, mode os.FileMode) error {
	return c.client.Chmod(path, mode)
}

func (c sftpAtomicClient) Chown(path string, uid, gid int) error {
	return c.client.Chown(path, uid, gid)
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

	tracker := transfer.NewTracker(transferID, 1, stat.Size(), onProgress)
	return s.uploadFile(ctx, tracker, sftpAtomicClient{client: sftpClient}, localFile, remotePath, filepath.Base(remotePath), stat.Size())
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

	tracker := transfer.NewTracker(transferID, 1, stat.Size(), onProgress)
	return s.downloadFile(ctx, tracker, remoteFile, localPath, filepath.Base(remotePath))
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
	tracker := transfer.NewTracker(transferID, filesTotal, bytesTotal, onProgress)

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

		if err := s.uploadFile(ctx, tracker, sftpAtomicClient{client: sftpClient}, localFile, remoteFull, relPath, stat.Size()); err != nil {
			return err
		}
		tracker.FileDone(stat.Size())
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
	tracker := transfer.NewTracker(transferID, filesTotal, bytesTotal, onProgress)

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

		remoteFile, err := sftpClient.Open(entry.remotePath)
		if err != nil {
			return err
		}

		// downloadFile 自己会建父目录，这里不重复。
		err = s.downloadFile(ctx, tracker, remoteFile, localFull, relPath)
		_ = remoteFile.Close()
		if err != nil {
			return err
		}
		tracker.FileDone(entry.size)
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

// remoteTarget 描述上传目标路径当前的状态，决定用哪条写入路径。
type remoteTarget struct {
	path     string      // 解析符号链接之后的真实路径
	mode     os.FileMode // 完整模式，含类型位
	exists   bool
	uid, gid int
	hasOwner bool
}

func (t remoteTarget) regular() bool { return t.mode.IsRegular() }

// streamOnly 表示目标是命名管道或套接字。SFTP 的写请求带绝对偏移量，
// 服务端对这类节点做 pwrite 会返回 illegal seek —— 它们根本无法经 SFTP 写入。
func (t remoteTarget) streamOnly() bool { return t.mode&(os.ModeNamedPipe|os.ModeSocket) != 0 }

// inspectRemoteTarget 解析目标路径：跟随符号链接找到真正要写的文件，并读出它的权限与属主。
//
// 跟随符号链接是必须的 —— 原地覆盖的语义是"写穿到链接指向的文件"，
// 而 rename 作用在链接本身会把链接替换成普通文件，悄悄破坏 sites-enabled/ 这类布局。
func inspectRemoteTarget(client remoteAtomicClient, remotePath string) (remoteTarget, error) {
	const maxSymlinkHops = 8
	current := remotePath
	for hop := 0; ; hop++ {
		info, err := client.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return remoteTarget{path: current}, nil
			}
			return remoteTarget{}, fmt.Errorf("获取远程文件信息失败: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			target := remoteTarget{
				path:   current,
				exists: true,
				mode:   info.Mode(),
			}
			if stat, ok := info.Sys().(*sftp.FileStat); ok && stat != nil {
				target.uid, target.gid, target.hasOwner = int(stat.UID), int(stat.GID), true
			}
			return target, nil
		}
		if hop >= maxSymlinkHops {
			return remoteTarget{}, fmt.Errorf("远程路径符号链接层级过深: %s", remotePath)
		}
		dest, err := client.ReadLink(current)
		if err != nil {
			return remoteTarget{}, fmt.Errorf("解析远程符号链接失败: %w", err)
		}
		if !path.IsAbs(dest) {
			dest = path.Join(path.Dir(current), dest)
		}
		current = dest
	}
}

// uploadFile 把 local 写到 remotePath。默认走"同目录临时文件 + 原子切换"，
// 让远端只能看到旧版本或完整新版本，不会暴露半写入状态。
//
// 两种情况回退到原地写入，因为它们根本无法用 rename 表达：
// 目标是 FIFO/设备节点等非常规文件（rename 会把节点本身换成普通文件），
// 以及父目录不可写（建不出临时文件，但目标文件本身仍可写）。
func (s *Service) uploadFile(ctx context.Context, tracker *transfer.Tracker, client remoteAtomicClient, local io.Reader, remotePath, currentFile string, fileSize int64) error {
	target, err := inspectRemoteTarget(client, remotePath)
	if err != nil {
		return err
	}

	if target.exists && !target.regular() {
		if target.streamOnly() {
			return fmt.Errorf("远程路径是命名管道或套接字，SFTP 按偏移量写入，无法写入该类型: %s", target.path)
		}
		return uploadInPlace(ctx, tracker, client, local, target, currentFile, fileSize)
	}

	tempPath := buildRemoteTempPath(target.path, "part")
	// O_EXCL：临时名带纳秒时间戳，撞名说明有并发写入，宁可失败也不覆盖
	remoteFile, err := client.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		if os.IsPermission(err) && target.exists {
			logger.Default().Info("remote dir not writable, uploading in place",
				zap.String("path", target.path), zap.Error(err))
			return uploadInPlace(ctx, tracker, client, local, target, currentFile, fileSize)
		}
		return fmt.Errorf("创建远程临时文件失败: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			cleanupRemotePath(client, tempPath)
		}
	}()

	if _, err := remoteFile.ReadFrom(tracker.Reader(ctx, local, currentFile, fileSize)); err != nil {
		_ = remoteFile.Close()
		return fmt.Errorf("写入远程临时文件失败: %w", err)
	}
	if err := remoteFile.Close(); err != nil {
		return fmt.Errorf("关闭远程临时文件失败: %w", err)
	}
	if err := verifyRemoteSize(client, tempPath, fileSize); err != nil {
		return err
	}

	if target.exists && target.hasOwner {
		// rename 后的文件属主是上传者。非 root 无权改回，属于这条路径的固有限制，
		// 记一条日志让排查有据可依，不当作上传失败。
		if err := client.Chown(tempPath, target.uid, target.gid); err != nil {
			logger.Default().Info("preserve remote file owner failed",
				zap.String("path", target.path), zap.Int("uid", target.uid), zap.Int("gid", target.gid), zap.Error(err))
		}
	}

	if err := commitRemoteTempFile(client, tempPath, target.path, target.mode.Perm(), target.exists); err != nil {
		return err
	}
	committed = true
	return nil
}

// uploadInPlace 直接覆盖目标文件本身，用于无法用 rename 表达的目标。
// 并发写出错时文件长度可能长于成功写入的部分，按 pkg/sftp 的要求截断回去。
func uploadInPlace(ctx context.Context, tracker *transfer.Tracker, client remoteAtomicClient, local io.Reader, target remoteTarget, currentFile string, fileSize int64) error {
	remoteFile, err := client.OpenFile(target.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("打开远程文件失败: %w", err)
	}
	written, err := remoteFile.ReadFrom(tracker.Reader(ctx, local, currentFile, fileSize))
	if err != nil {
		if truncErr := remoteFile.Truncate(written); truncErr != nil {
			logger.Default().Warn("truncate remote file after failed write",
				zap.String("path", target.path), zap.Int64("size", written), zap.Error(truncErr))
		}
		_ = remoteFile.Close()
		return fmt.Errorf("写入远程文件失败: %w", err)
	}
	if err := remoteFile.Close(); err != nil {
		return fmt.Errorf("关闭远程文件失败: %w", err)
	}
	if target.exists && !target.regular() {
		// 设备节点的"大小"没有意义（写 /dev/null 后 Stat 仍是 0），跳过复核。
		return nil
	}
	return verifyRemoteSize(client, target.path, fileSize)
}

// verifyRemoteSize 复核写入的字节数确实落到了远端。
//
// pkg/sftp v1.13.10 的 File.ReadFrom 会在"最后一个分片是短读"时丢掉该分片的写入错误：
// io.ReadFull 返回 ErrUnexpectedEOF 覆盖了 writeChunkAt 的错误，随后被当成正常 EOF 返回 nil
// （client.go:2078-2090）。绝大多数文件的最后一片都是短读，因此一次失败的上传会报告成功。
// 这是上游缺陷，不在这里绕开 ReadFrom，只按大小复核，避免把静默的数据丢失当作上传成功。
func verifyRemoteSize(client remoteAtomicClient, remotePath string, want int64) error {
	info, err := client.Stat(remotePath)
	if err != nil {
		return fmt.Errorf("校验远程文件失败: %w", err)
	}
	if info.Size() != want {
		return fmt.Errorf("远程文件大小校验失败：期望 %d 字节，实际写入 %d 字节", want, info.Size())
	}
	return nil
}

// downloadFile：远程 → 本地临时文件（并发读）→ 原子 rename。
func (s *Service) downloadFile(ctx context.Context, tracker *transfer.Tracker, remote io.WriterTo, localPath, currentFile string) error {
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建本地目录失败: %w", err)
	}

	// 覆盖已有文件时沿用它的权限：os.Create 对已存在文件不改权限，
	// 而"新建临时文件再 rename"会把权限换成新建时的模式 ——
	// 重新下载一个 0600 的私钥不能把它变成 0644。
	perm := os.FileMode(0o644)
	if info, err := os.Stat(localPath); err == nil {
		perm = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("获取本地文件信息失败: %w", err)
	}

	tempPath := filepath.Join(dir, fmt.Sprintf(".%s.opskat-part-%d", filepath.Base(localPath), time.Now().UnixNano()))
	localFile, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) //nolint:gosec // perm 继承自被覆盖的目标文件
	if err != nil {
		return fmt.Errorf("创建本地临时文件失败: %w", err)
	}
	committed := false
	defer func() {
		_ = localFile.Close()
		if !committed {
			if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
				logger.Default().Warn("cleanup local temp file", zap.String("path", tempPath), zap.Error(err))
			}
		}
	}()

	if _, err := remote.WriteTo(tracker.Writer(ctx, localFile, currentFile)); err != nil {
		return err
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

func writeFileAtomically(client remoteAtomicClient, remotePath string, data []byte) error {
	// 优先写临时文件再切换目标文件名：
	// 成功时远端始终只会看到“旧版本”或“完整新版本”，不会暴露半写入状态。
	target, err := inspectRemoteTarget(client, remotePath)
	if err != nil {
		return err
	}
	if target.exists && !target.regular() {
		// 目录、管道或其他特殊节点一旦进入原子替换流程，失败恢复和权限继承语义都会变得不可控。
		return fmt.Errorf("远程路径不是常规文件: %s (mode=%s, perm=%#o, isDir=%t)",
			target.path, target.mode, target.mode.Perm(), target.mode.IsDir())
	}
	remotePath = target.path // 跟随符号链接：写穿到链接指向的文件，而不是把链接换成普通文件

	tempPath := buildRemoteTempPath(remotePath, "tmp")
	committed := false
	defer func() {
		if !committed {
			cleanupRemotePath(client, tempPath)
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

	if err := commitRemoteTempFile(client, tempPath, remotePath, target.mode.Perm(), target.exists); err != nil {
		return err
	}
	committed = true
	return nil
}

// commitRemoteTempFile 把写好的临时文件切换成目标文件。返回 nil 表示 tempPath 已经易主，
// 调用方不应再清理它。
//
// 优先 PosixRename（单次原子操作）；服务端不支持时回退到"先备份旧文件，再切换新文件，
// 再尽力恢复"，把副作用控制在单个目标文件范围内。
func commitRemoteTempFile(client remoteAtomicClient, tempPath, remotePath string, targetMode os.FileMode, targetExists bool) error {
	if !targetExists {
		if err := client.Rename(tempPath, remotePath); err != nil {
			return fmt.Errorf("提交远程临时文件失败: %w", err)
		}
		return nil
	}

	if err := client.Chmod(tempPath, targetMode); err != nil {
		return fmt.Errorf("同步远程文件权限失败: %w", err)
	}
	if err := client.PosixRename(tempPath, remotePath); err == nil {
		return nil
	} else if !isSFTPOpUnsupported(err) {
		return fmt.Errorf("原子替换远程文件失败: %w", err)
	}

	backupPath := buildRemoteTempPath(remotePath, "bak")
	if err := client.Rename(remotePath, backupPath); err != nil {
		return fmt.Errorf("创建远程备份文件失败: %w", err)
	}

	if err := client.Rename(tempPath, remotePath); err != nil {
		if restoreErr := client.Rename(backupPath, remotePath); restoreErr != nil {
			// 目标路径已经空出来了，原文件只剩备份这一份 —— 绝不能删，
			// 必须把路径告诉用户，否则这就是一次静默的数据丢失。
			return fmt.Errorf("替换远程文件失败且恢复原文件失败，原文件保留在 %s: %w; restore: %v", backupPath, err, restoreErr)
		}
		return fmt.Errorf("替换远程文件失败，已恢复原文件: %w", err)
	}
	// 恢复分支里备份已经改回目标路径，只有切换成功这一条路需要清理备份。
	cleanupRemotePath(client, backupPath)
	return nil
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
