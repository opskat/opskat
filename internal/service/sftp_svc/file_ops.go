package sftp_svc

import (
	"context"
	"fmt"
	"io"
	"os"
	pathpkg "path"
	"regexp"
	"strconv"
	"strings"

	"github.com/pkg/sftp"
)

// ClipboardItem describes a remote path captured by the file-manager clipboard.
type ClipboardItem struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	IsDir     bool   `json:"isDir"`
	Size      int64  `json:"size"`
}

// PasteRequest asks the backend to paste clipboard items into TargetDir.
type PasteRequest struct {
	TargetSessionID string          `json:"targetSessionId"`
	TargetDir       string          `json:"targetDir"`
	Mode            string          `json:"mode"` // "copy" | "cut"
	Items           []ClipboardItem `json:"items"`
}

// FileProperties contains metadata displayed by the file-manager properties dialog.
type FileProperties struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	IsDir      bool   `json:"isDir"`
	ModTime    int64  `json:"modTime"`
	Mode       string `json:"mode"`
	UID        uint32 `json:"uid"`
	GID        uint32 `json:"gid"`
	ChildCount int64  `json:"childCount,omitempty"`
	// Truncated 表示目录递归统计在达到上限/取消前提前结束，ChildCount/Size 为下界。
	Truncated bool `json:"truncated,omitempty"`
}

// PermissionApplyRequest updates chmod/chown data for one path.
type PermissionApplyRequest struct {
	Path            string `json:"path"`
	Mode            string `json:"mode"`
	Owner           string `json:"owner"`
	Group           string `json:"group"`
	Recursive       bool   `json:"recursive"`
	RecursiveTarget string `json:"recursiveTarget"` // "all" | "files" | "dirs"
}

var (
	modePattern  = regexp.MustCompile(`^[0-7]{3,4}$`)
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// Mkdir creates one remote directory.
func (s *Service) Mkdir(sessionID, remotePath string) error {
	client, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	return client.Mkdir(remotePath)
}

// CreateFile creates an empty remote file, failing if the server rejects creation.
func (s *Service) CreateFile(sessionID, remotePath string) error {
	client, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	file, err := client.Create(remotePath)
	if err != nil {
		return err
	}
	return file.Close()
}

// Rename renames or moves a remote path within the same SFTP session.
func (s *Service) Rename(sessionID, oldPath, newPath string) error {
	client, err := s.getSFTPClient(sessionID)
	if err != nil {
		return err
	}
	if err := client.PosixRename(oldPath, newPath); err == nil {
		return nil
	}
	return client.Rename(oldPath, newPath)
}

// Paste copies or moves clipboard items into a target directory. Name conflicts are
// resolved with _copy / _copy2 suffixes, including same-directory copy.
func (s *Service) Paste(ctx context.Context, req PasteRequest) error {
	if req.TargetSessionID == "" || strings.TrimSpace(req.TargetDir) == "" {
		return fmt.Errorf("target directory is required")
	}
	if req.Mode != "copy" && req.Mode != "cut" {
		return fmt.Errorf("unsupported paste mode %q", req.Mode)
	}
	if len(req.Items) == 0 {
		return fmt.Errorf("clipboard is empty")
	}

	for _, item := range req.Items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if item.SessionID == "" || item.Path == "" {
			return fmt.Errorf("invalid clipboard item")
		}
		srcClient, err := s.getSFTPClient(item.SessionID)
		if err != nil {
			return err
		}
		dstClient, err := s.getSFTPClient(req.TargetSessionID)
		if err != nil {
			return err
		}

		name := item.Name
		if name == "" {
			name = pathpkg.Base(item.Path)
		}
		if req.Mode == "cut" && item.SessionID == req.TargetSessionID && sameRemotePath(pathpkg.Dir(item.Path), req.TargetDir) {
			continue
		}
		dstPath, err := uniqueRemotePath(dstClient, pathpkg.Join(req.TargetDir, name))
		if err != nil {
			return err
		}

		if req.Mode == "cut" && item.SessionID == req.TargetSessionID {
			if sameRemotePath(item.Path, dstPath) {
				continue
			}
			if err := s.Rename(item.SessionID, item.Path, dstPath); err != nil {
				return fmt.Errorf("move %s: %w", item.Path, err)
			}
			continue
		}

		if item.IsDir {
			if err := copyRemoteDir(ctx, srcClient, dstClient, item.Path, dstPath); err != nil {
				return fmt.Errorf("copy directory %s: %w", item.Path, err)
			}
		} else if err := copyRemoteFile(ctx, srcClient, dstClient, item.Path, dstPath); err != nil {
			return fmt.Errorf("copy file %s: %w", item.Path, err)
		}
		if req.Mode == "cut" {
			if item.IsDir {
				if err := s.removeDirRecursive(srcClient, item.Path); err != nil {
					return fmt.Errorf("remove source directory %s: %w", item.Path, err)
				}
			} else if err := srcClient.Remove(item.Path); err != nil {
				return fmt.Errorf("remove source file %s: %w", item.Path, err)
			}
		}
	}
	return nil
}

// 目录递归统计上限：超过这个上限或 ctx 取消时，countRemoteDir 提前返回，
// Properties 弹窗也据此把结果标记为近似值，避免在巨大目录上阻塞 IPC。
const remoteDirCountLimit = 50000

// Properties returns detailed metadata for one remote path.
// 对于目录会递归统计子项数量和总大小，但有 ctx/上限保护，遇到大目录会提前返回近似值。
func (s *Service) Properties(ctx context.Context, sessionID, remotePath string) (FileProperties, error) {
	client, err := s.getSFTPClient(sessionID)
	if err != nil {
		return FileProperties{}, err
	}
	info, err := client.Stat(remotePath)
	if err != nil {
		return FileProperties{}, err
	}

	props := FileProperties{
		Path:    remotePath,
		Name:    info.Name(),
		Size:    info.Size(),
		IsDir:   info.IsDir(),
		ModTime: info.ModTime().Unix(),
		Mode:    fmt.Sprintf("%04o", uint32(info.Mode().Perm())|specialBitsFromMode(info.Mode())),
	}
	if stat, ok := info.Sys().(*sftp.FileStat); ok && stat != nil {
		props.UID = stat.UID
		props.GID = stat.GID
	}

	if info.IsDir() {
		children, size, truncated, err := countRemoteDir(ctx, client, remotePath, remoteDirCountLimit)
		if err == nil {
			props.ChildCount = children
			props.Size = size
			props.Truncated = truncated
		}
		return props, nil
	}

	return props, nil
}

// ApplyPermissions applies chmod and/or chown settings to a remote path.
func (s *Service) ApplyPermissions(sessionID string, req PermissionApplyRequest) error {
	if req.Path == "" {
		return fmt.Errorf("path is required")
	}
	if req.Mode != "" && !modePattern.MatchString(req.Mode) {
		return fmt.Errorf("invalid mode %q", req.Mode)
	}
	if err := validateOwnerPart(req.Owner); err != nil {
		return err
	}
	if err := validateOwnerPart(req.Group); err != nil {
		return err
	}
	if req.Mode == "" && req.Owner == "" && req.Group == "" {
		return nil
	}

	if !req.Recursive && req.Owner == "" && req.Group == "" && req.Mode != "" {
		client, err := s.getSFTPClient(sessionID)
		if err != nil {
			return err
		}
		parsed, err := strconv.ParseUint(req.Mode, 8, 32)
		if err != nil {
			return err
		}
		return client.Chmod(req.Path, os.FileMode(parsed))
	}

	sess, ok := s.sshManager.GetSession(sessionID)
	if !ok || sess.IsClosed() {
		return fmt.Errorf("SSH 会话不存在或已关闭: %s", sessionID)
	}
	cmds := buildPermissionCommands(req)
	if len(cmds) == 0 {
		return nil
	}
	sshSession, err := sess.Client().NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = sshSession.Close() }()
	cmd := strings.Join(cmds, " && ")
	if out, err := sshSession.CombinedOutput(cmd); err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func validateOwnerPart(value string) error {
	if value == "" {
		return nil
	}
	if !ownerPattern.MatchString(value) {
		return fmt.Errorf("invalid owner/group %q", value)
	}
	return nil
}

func buildPermissionCommands(req PermissionApplyRequest) []string {
	path := shellQuote(req.Path)
	target := req.RecursiveTarget
	if target == "" {
		target = "all"
	}
	var cmds []string
	add := func(base string, arg string) {
		if arg == "" {
			return
		}
		qarg := shellQuote(arg)
		if req.Recursive {
			switch target {
			case "files":
				cmds = append(cmds, "find "+path+" -type f -exec "+base+" "+qarg+" {} +")
			case "dirs":
				cmds = append(cmds, "find "+path+" -type d -exec "+base+" "+qarg+" {} +")
			default:
				cmds = append(cmds, base+" -R "+qarg+" "+path)
			}
			return
		}
		cmds = append(cmds, base+" "+qarg+" "+path)
	}
	add("chmod", req.Mode)
	if req.Owner != "" || req.Group != "" {
		spec := req.Owner
		if req.Group != "" {
			spec += ":" + req.Group
		}
		add("chown", spec)
	}
	return cmds
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func specialBitsFromMode(mode os.FileMode) uint32 {
	var bits uint32
	if mode&os.ModeSetuid != 0 {
		bits |= 04000
	}
	if mode&os.ModeSetgid != 0 {
		bits |= 02000
	}
	if mode&os.ModeSticky != 0 {
		bits |= 01000
	}
	return bits
}

func sameRemotePath(a, b string) bool {
	return pathpkg.Clean(a) == pathpkg.Clean(b)
}

func uniqueRemotePath(client interface {
	Stat(string) (os.FileInfo, error)
}, desired string) (string, error) {
	if _, err := client.Stat(desired); err != nil {
		if os.IsNotExist(err) {
			return desired, nil
		}
		// Many SFTP servers return generic errors for missing paths; try the desired name first.
		return desired, nil
	}
	dir := pathpkg.Dir(desired)
	base := pathpkg.Base(desired)
	ext := pathpkg.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; i < 10000; i++ {
		suffix := "_copy"
		if i > 0 {
			suffix = fmt.Sprintf("_copy%d", i+1)
		}
		candidate := pathpkg.Join(dir, stem+suffix+ext)
		if _, err := client.Stat(candidate); err != nil {
			// Stat 失败视为"名字可用"：SFTP 服务端常返回通用错误而非 ENOENT。
			return candidate, nil //nolint:nilerr
		}
	}
	return "", fmt.Errorf("cannot find available copy name for %s", desired)
}

func copyRemoteFile(ctx context.Context, srcClient, dstClient interface {
	Open(string) (*sftp.File, error)
	Create(string) (*sftp.File, error)
	Chmod(string, os.FileMode) error
	Stat(string) (os.FileInfo, error)
}, srcPath, dstPath string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	src, err := srcClient.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := dstClient.Create(dstPath)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if info, err := srcClient.Stat(srcPath); err == nil {
		_ = dstClient.Chmod(dstPath, info.Mode())
	}
	return nil
}

func copyRemoteDir(ctx context.Context, srcClient, dstClient interface {
	Open(string) (*sftp.File, error)
	Create(string) (*sftp.File, error)
	Chmod(string, os.FileMode) error
	Stat(string) (os.FileInfo, error)
	ReadDir(string) ([]os.FileInfo, error)
	MkdirAll(string) error
}, srcDir, dstDir string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := dstClient.MkdirAll(dstDir); err != nil {
		return err
	}
	if info, err := srcClient.Stat(srcDir); err == nil {
		_ = dstClient.Chmod(dstDir, info.Mode())
	}
	entries, err := srcClient.ReadDir(srcDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := pathpkg.Join(srcDir, entry.Name())
		dstPath := pathpkg.Join(dstDir, entry.Name())
		if entry.IsDir() {
			if err := copyRemoteDir(ctx, srcClient, dstClient, srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyRemoteFile(ctx, srcClient, dstClient, srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// countRemoteDir 递归统计目录下条目数与总大小。
// ctx 取消或累计 count 超过 limit (>0) 时提前返回，truncated=true 表示结果是下界。
func countRemoteDir(ctx context.Context, client interface {
	ReadDir(string) ([]os.FileInfo, error)
}, dir string, limit int64) (int64, int64, bool, error) {
	if ctx.Err() != nil {
		// ctx 取消视为截断，吞掉 err 并返回部分结果让调用方据 truncated 标注。
		return 0, 0, true, nil //nolint:nilerr
	}
	entries, err := client.ReadDir(dir)
	if err != nil {
		return 0, 0, false, err
	}
	var count int64
	var size int64
	for _, entry := range entries {
		if ctx.Err() != nil {
			return count, size, true, nil //nolint:nilerr
		}
		count++
		fullPath := pathpkg.Join(dir, entry.Name())
		if entry.IsDir() {
			var remaining int64
			if limit > 0 {
				remaining = limit - count
				if remaining <= 0 {
					return count, size, true, nil
				}
			}
			childCount, childSize, childTruncated, err := countRemoteDir(ctx, client, fullPath, remaining)
			if err != nil {
				return count, size, false, err
			}
			count += childCount
			size += childSize
			if childTruncated {
				return count, size, true, nil
			}
		} else {
			size += entry.Size()
		}
		if limit > 0 && count >= limit {
			return count, size, true, nil
		}
	}
	return count, size, false, nil
}
