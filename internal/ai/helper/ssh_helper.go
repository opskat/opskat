package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// --- SSH 客户端缓存（同一次 AI Send 内复用连接）---
//
// 原实现位于 internal/ai/tool，因 execimpl 不能依赖 tool（tool 反过来要 blank-import
// execimpl 触发注册，避免循环依赖）而移入 helper。tool 包保留同名导出符号作为薄别名，
// 外部调用方（如 internal/app/ai）不受影响。

type sshCacheKeyType struct{}

// SSHClientCache 在同一次 AI Send 中复用 SSH 连接。
type SSHClientCache = ConnCache[*ssh.Client]

// SFTPClientCache 在一次 cp 命令内按资产复用 SFTP 会话。
type SFTPClientCache struct {
	clients *ConnCache[*sftp.Client]
	dial    func(context.Context, int64) (*sftp.Client, io.Closer, error)
}

type sftpCacheKeyType struct{}

// NewSFTPClientCache 创建使用真实资产连接的任务级 SFTP cache。
func NewSFTPClientCache() *SFTPClientCache {
	return newSFTPClientCache(dialAssetSFTP)
}

func newSFTPClientCache(dial func(context.Context, int64) (*sftp.Client, io.Closer, error)) *SFTPClientCache {
	return &SFTPClientCache{clients: NewConnCache[*sftp.Client]("SFTP"), dial: dial}
}

func (c *SFTPClientCache) Close() error { return c.clients.Close() }

func (c *SFTPClientCache) client(ctx context.Context, assetID int64) (*sftp.Client, error) {
	client, _, err := c.clients.GetOrDial(assetID, func() (*sftp.Client, io.Closer, error) {
		return c.dial(ctx, assetID)
	})
	return client, err
}

// WithSFTPClientCache 把任务级 SFTP cache 注入传输调用链。
func WithSFTPClientCache(ctx context.Context, cache *SFTPClientCache) context.Context {
	return context.WithValue(ctx, sftpCacheKeyType{}, cache)
}

func getSFTPClientCache(ctx context.Context) *SFTPClientCache {
	cache, _ := ctx.Value(sftpCacheKeyType{}).(*SFTPClientCache)
	return cache
}

// EnsureSFTPClientCache 复用已有 cache，或创建一个由调用方负责关闭的新 cache。
// 第三个返回值说明本次调用是否拥有新 cache。
func EnsureSFTPClientCache(ctx context.Context) (context.Context, *SFTPClientCache, bool) {
	if cache := getSFTPClientCache(ctx); cache != nil {
		return ctx, cache, false
	}
	cache := NewSFTPClientCache()
	return WithSFTPClientCache(ctx, cache), cache, true
}

func dialAssetSFTP(ctx context.Context, assetID int64) (*sftp.Client, io.Closer, error) {
	if sshCache := getSSHCache(ctx); sshCache != nil {
		client, _, err := sshCache.GetOrDial(assetID, func() (*ssh.Client, io.Closer, error) {
			sshClient, extras, dialErr := credential_resolver.Default().DialAssetSSH(ctx, assetID)
			if dialErr != nil {
				return nil, nil, dialErr
			}
			return sshClient, ClosersAsOne(extras), nil
		})
		if err != nil {
			return nil, nil, err
		}
		sftpClient, err := sftp.NewClient(client)
		if err != nil {
			sshCache.Remove(assetID)
			return nil, nil, fmt.Errorf("failed to create SFTP client: %w", err)
		}
		return sftpClient, nil, nil
	}
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create SFTP client: %w", err)
	}
	return sftpClient, closerFunc(func() error { cleanup(); return nil }), nil
}

// NewSSHClientCache 创建 SSH 客户端缓存。
func NewSSHClientCache() *SSHClientCache {
	return NewConnCache[*ssh.Client]("SSH")
}

// WithSSHCache 将 SSH 缓存注入 context。
func WithSSHCache(ctx context.Context, cache *SSHClientCache) context.Context {
	return context.WithValue(ctx, sshCacheKeyType{}, cache)
}

func getSSHCache(ctx context.Context) *SSHClientCache {
	if cache, ok := ctx.Value(sshCacheKeyType{}).(*SSHClientCache); ok {
		return cache
	}
	return nil
}

// ExecCommandOnAsset 是不含权限检查的纯执行入口：权限检查由调用方（统一 exec 工具的
// handleExec、batch_exec 的预检）在调用之前完成。scope 对 SSH 无意义，忽略。
func ExecCommandOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	// 如果 context 注入了 SSH 缓存，复用同一资产的连接
	if cache := getSSHCache(ctx); cache != nil {
		return runCommandWithCache(ctx, cache, asset.ID, command)
	}
	// 无缓存，创建一次性连接
	return ExecuteSSHCommand(ctx, asset.ID, command)
}

func runCommandWithCache(ctx context.Context, cache *SSHClientCache, assetID int64, command string) (string, error) {
	dial := func() (*ssh.Client, io.Closer, error) {
		client, extras, err := credential_resolver.Default().DialAssetSSH(ctx, assetID)
		if err != nil {
			return nil, nil, err
		}
		return client, ClosersAsOne(extras), nil
	}

	client, _, err := cache.GetOrDial(assetID, dial)
	if err != nil {
		return "", err
	}
	output, err := RunSSHCommand(ctx, client, command)
	if err != nil {
		// 当前会话已经取消时，RunSSHCommand 已主动关闭 client 以打断阻塞；
		// 这里只需把条目从缓存中摘除（避免下次复用半失效连接），不能再次 Close。
		if ctx.Err() != nil {
			cache.Forget(assetID)
			return "", ctx.Err()
		}
		// 非取消错误优先按连接失效处理，删除缓存后只重试一次，避免重复执行
		cache.Remove(assetID)
		client, _, err = cache.GetOrDial(assetID, dial)
		if err != nil {
			return "", err
		}
		output, err = RunSSHCommand(ctx, client, command)
		if err != nil {
			cache.Remove(assetID)
			return "", err
		}
	}
	return output, nil
}

// IsExpectedCloseErr 判断 SSH/网络连接关闭时的预期错误。
// 取消路径会主动 Close session/client 打断阻塞，随后的 defer 关闭就会返回这些错误；
// 归类为预期错误后，上层可以跳过 warn 日志，避免噪音。
func IsExpectedCloseErr(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed)
}

// closeOnCancel 启动 watcher goroutine，ctx 取消时调用所有 closers。
// 用于打断 SFTP io.Copy 等不感知 ctx 的阻塞操作 —— 关闭底层连接后，
// Copy 会立即因 net.ErrClosed 返回。
// 返回的 stop 函数必须 defer 调用，确保正常路径下 watcher 退出，不泄漏 goroutine。
// Close 错误忽略：connection 可能已被正常路径关闭，Close 是幂等的。
func closeOnCancel(ctx context.Context, closers ...io.Closer) func() {
	if ctx == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			for _, c := range closers {
				_ = c.Close()
			}
		case <-done:
		}
	}()
	return func() { close(done) }
}

// DialAssetSSH 建立到资产的 SSH 连接，返回 client 与一次性清理函数。
// 底层委托给 credential_resolver.DialAssetSSH，统一支持 SOCKS5/HTTP 代理与跳板机链。
// 调用方必须在使用结束后调用返回的 cleanup，一并关闭 client 与跳板机链上的中间连接。
func DialAssetSSH(ctx context.Context, assetID int64) (*ssh.Client, func(), error) {
	client, extraClosers, err := credential_resolver.Default().DialAssetSSH(ctx, assetID)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if err := client.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH client", zap.Error(err))
		}
		closeExtras(extraClosers)
	}
	return client, cleanup, nil
}

// closeExtras 关闭跳板机链等附加资源，预期关闭错误静默跳过。
func closeExtras(closers []io.Closer) {
	for _, c := range closers {
		if c == nil {
			continue
		}
		if err := c.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH chain resource", zap.Error(err))
		}
	}
}

// ClosersAsOne 将多个 closer 打包成单个 io.Closer（用于只接受单 closer 的 API，如 ConnCache）。
func ClosersAsOne(closers []io.Closer) io.Closer {
	if len(closers) == 0 {
		return nil
	}
	return closerFunc(func() error {
		closeExtras(closers)
		return nil
	})
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// ExecuteSSHCommand 执行一次性 SSH 命令并返回输出（每次新建连接）
func ExecuteSSHCommand(ctx context.Context, assetID int64, command string) (string, error) {
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return RunSSHCommand(ctx, client, command)
}

// RunSSHCommand 在已有的 SSH 客户端上执行命令
func RunSSHCommand(ctx context.Context, client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer func() {
		// ctx 取消路径下 session 已经被主动关闭，defer 再次 Close 会拿到已关闭错误，静默跳过。
		if err := session.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH session", zap.Error(err))
		}
	}()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	runCh := make(chan error, 1)
	go func() {
		runCh <- session.Run(command)
	}()

	select {
	case err := <-runCh:
		if err != nil {
			if stderr.Len() > 0 {
				return "", fmt.Errorf("command failed: %s", stderr.String())
			}
			return "", fmt.Errorf("command failed: %w", err)
		}
	case <-ctx.Done():
		// 仅关闭 session 可能不足以唤醒底层 Run/Wait，这里连 client 一并关闭来打断阻塞。
		// 上层 defer 会再次 Close，已通过 IsExpectedCloseErr 过滤预期错误。
		if err := session.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH session on cancel", zap.Error(err))
		}
		if err := client.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH client on cancel", zap.Error(err))
		}
		return "", ctx.Err()
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}
	return output, nil
}

// ExecuteWithSFTP 在 context 带任务级 SFTP cache 时复用该资产的会话；普通调用保持一次性
// SSH+SFTP 连接语义。
// ctx 取消时主动关闭底层连接以打断 fn 内部可能的 io.Copy 阻塞，
// 从而让 AI 停止会话能立即生效（否则大文件传输会挂住 runner.Stop）。
func ExecuteWithSFTP(ctx context.Context, assetID int64, fn func(*sftp.Client) error) error {
	if cache := getSFTPClientCache(ctx); cache != nil {
		client, err := cache.client(ctx, assetID)
		if err != nil {
			return err
		}
		stopWatch := closeOnCancel(ctx, client)
		defer stopWatch()
		if err := fn(client); err != nil {
			if ctx != nil && ctx.Err() != nil {
				cache.clients.Remove(assetID)
				return ctx.Err()
			}
			return err
		}
		return nil
	}
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return err
	}
	defer cleanup()

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("failed to create SFTP client: %w", err)
	}
	defer func() {
		if err := sftpClient.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SFTP client", zap.Error(err))
		}
	}()

	// 顺序：先关 sftpClient 结束 SFTP 会话，再关 SSH client 打断底层 TCP。
	stopWatch := closeOnCancel(ctx, sftpClient, client)
	defer stopWatch()

	if err := fn(sftpClient); err != nil {
		// ctx 已取消时，优先返回 ctx.Err()，避免把底层 EOF/closed 暴露给上层。
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// openSFTPFile 打开远端文件用于流式读取，返回 reader 与文件大小。
//
// 一次性路径把连接生命周期绑在返回的 ReadCloser 上；任务 cache 路径只把远端文件句柄绑给
// reader，SFTP/SSH 会话由整个 cp 命令统一关闭。
func openSFTPFile(ctx context.Context, assetID int64, path string) (io.ReadCloser, int64, error) {
	if cache := getSFTPClientCache(ctx); cache != nil {
		client, err := cache.client(ctx, assetID)
		if err != nil {
			return nil, 0, err
		}
		file, err := client.Open(path)
		if err != nil {
			return nil, 0, ctxErrOr(ctx, fmt.Errorf("failed to open remote file: %w", err))
		}
		stopWatch := closeOnCancel(ctx, file, closerFunc(func() error {
			cache.clients.Remove(assetID)
			return nil
		}))
		reader := newConnBoundReadCloser(ctx, file, stopWatch)
		info, err := file.Stat()
		if err != nil {
			_ = reader.Close()
			return nil, 0, ctxErrOr(ctx, fmt.Errorf("failed to stat remote file: %w", err))
		}
		return reader, info.Size(), nil
	}
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return nil, 0, err
	}
	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		cleanup()
		return nil, 0, fmt.Errorf("failed to create SFTP client: %w", err)
	}
	// ctx 取消时关闭两端，打断 reader 上阻塞的 Read——与 ExecuteWithSFTP 的顺序一致：
	// 先关 SFTP 会话，再关底层 SSH。
	stopWatch := closeOnCancel(ctx, sftpClient, client)
	teardown := func() {
		stopWatch()
		if err := sftpClient.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SFTP client", zap.Error(err))
		}
		cleanup()
	}

	return openSFTPReader(ctx, sftpClient, path, teardown)
}

// openSFTPReader 在已有的 SFTP 连接上打开 p，把 teardown（拆连接）绑到返回的 ReadCloser 上。
// 从 openSFTPFile 拆出来，是为了让"打开失败也照样拆连接"这条能在进程内 SFTP 服务端上验证：
// dial 之后的任何一条失败路径漏掉 teardown，就是每次读失败漏一条 SSH 连接。
func openSFTPReader(
	ctx context.Context, client *sftp.Client, p string, teardown func(),
) (io.ReadCloser, int64, error) {
	file, err := client.Open(p)
	if err != nil {
		teardown()
		return nil, 0, ctxErrOr(ctx, fmt.Errorf("failed to open remote file: %w", err))
	}
	reader := newConnBoundReadCloser(ctx, file, teardown)
	info, err := file.Stat()
	if err != nil {
		if closeErr := reader.Close(); closeErr != nil {
			logger.Default().Warn("close remote file", zap.String("path", p), zap.Error(closeErr))
		}
		return nil, 0, ctxErrOr(ctx, fmt.Errorf("failed to stat remote file: %w", err))
	}
	return reader, info.Size(), nil
}

// ctxErrOr 在 ctx 已取消时把底层错误换成 ctx.Err()，与 ExecuteWithSFTP 同一条规矩：
// 取消路径下 closeOnCancel 主动拆连接，上层看到的会是 net.ErrClosed，甚至一次截断读的
// io.EOF——那会让"已取消"看起来像"读完了"。
func ctxErrOr(ctx context.Context, err error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// connBoundReadCloser 把一个流和它赖以存在的连接绑成单个 Closer。Close 只生效一次：
// 重复 Close 不该把已经拆掉的连接再拆一遍。
type connBoundReadCloser struct {
	ctx      context.Context
	reader   io.ReadCloser
	teardown func()
	once     sync.Once
	err      error
}

func newConnBoundReadCloser(ctx context.Context, reader io.ReadCloser, teardown func()) io.ReadCloser {
	return &connBoundReadCloser{ctx: ctx, reader: reader, teardown: teardown}
}

func (c *connBoundReadCloser) Read(p []byte) (int, error) {
	n, err := c.reader.Read(p)
	if err != nil {
		return n, ctxErrOr(c.ctx, err)
	}
	return n, nil
}

func (c *connBoundReadCloser) Close() error {
	c.once.Do(func() {
		if err := c.reader.Close(); err != nil && !IsExpectedCloseErr(err) {
			c.err = err
		}
		c.teardown()
	})
	return c.err
}

// DialSSHClient 创建 SSH 客户端连接，自动解析凭据、代理、跳板机链。
// 调用者必须调用返回的 cleanup 关闭 client 与链路资源。
func DialSSHClient(ctx context.Context, assetID int64) (*ssh.Client, func(), error) {
	return DialAssetSSH(ctx, assetID)
}

// ExecWithStdio 在远程服务器执行命令，直接连接 stdio（支持管道）
func ExecWithStdio(ctx context.Context, assetID int64, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return err
	}
	defer cleanup()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			logger.Default().Warn("close ExecWithStdio SSH session", zap.Error(err))
		}
	}()

	if stdin != nil {
		session.Stdin = stdin
	}
	session.Stdout = stdout
	session.Stderr = stderr

	return session.Run(command)
}

// AIPoolDialer 实现 sshpool.PoolDialer，委托给 credential_resolver 统一 dial
type AIPoolDialer struct{}

func (d *AIPoolDialer) DialAsset(ctx context.Context, assetID int64) (*ssh.Client, []io.Closer, error) {
	return credential_resolver.Default().DialAssetSSH(ctx, assetID)
}
