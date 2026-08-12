package sshpool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/pkg/sftp"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// ProxyRequest 代理请求（JSON 握手消息）
type ProxyRequest struct {
	Token      string `json:"token,omitempty"` // 认证 token
	Op         string `json:"op"`              // "exec" | "upload" | "download" | "copy"
	AssetID    int64  `json:"asset_id"`
	Command    string `json:"command,omitempty"`
	Cols       int    `json:"cols,omitempty"`
	Rows       int    `json:"rows,omitempty"`
	PTY        bool   `json:"pty,omitempty"`
	SrcAssetID int64  `json:"src_asset_id,omitempty"` // copy: 源资产
	SrcPath    string `json:"src_path,omitempty"`
	DstPath    string `json:"dst_path,omitempty"`
}

// ProxyResponse 代理响应（JSON 握手响应）
type ProxyResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// SocketPath 返回 sshpool socket 路径
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, "sshpool.sock")
}

// Server SSH 代理 Unix socket 服务端
type Server struct {
	pool      *Pool
	listener  net.Listener
	done      chan struct{}
	wg        sync.WaitGroup
	authToken string // 认证 token，非空时校验
	mu        sync.Mutex
	conns     map[net.Conn]struct{}
	stopping  bool
	stopOnce  sync.Once
	active    atomic.Int64
}

// NewServer 创建代理服务端
func NewServer(pool *Pool, authToken string) *Server {
	return &Server{
		pool:      pool,
		done:      make(chan struct{}),
		authToken: authToken,
		conns:     make(map[net.Conn]struct{}),
	}
}

// Start 开始监听 Unix socket
func (s *Server) Start(socketPath string) error {
	// 清理 stale socket
	if _, err := os.Stat(socketPath); err == nil {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				logger.Default().Warn("close probe connection", zap.String("path", socketPath), zap.Error(closeErr))
			}
			return fmt.Errorf("another instance is already listening on %s", socketPath)
		}
		if err := os.Remove(socketPath); err != nil {
			logger.Default().Warn("remove stale socket", zap.String("path", socketPath), zap.Error(err))
		}
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	// 设置 socket 文件权限为 0600（仅所有者可访问）
	if err := os.Chmod(socketPath, 0600); err != nil {
		logger.Default().Warn("chmod socket", zap.String("path", socketPath), zap.Error(err))
	}
	s.listener = listener

	s.wg.Add(1)
	go s.acceptLoop()

	logger.Default().Info("server listening", zap.String("path", socketPath))
	return nil
}

// Stop 停止接收并主动断开所有客户端。它不等待请求处理协程，以保证应用退出
// 不会被远端命令、文件传输或异常客户端阻塞。
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		if s.listener != nil {
			if err := s.listener.Close(); err != nil {
				logger.Default().Warn("close listener", zap.Error(err))
			}
		}
		s.mu.Lock()
		s.stopping = true
		for conn := range s.conns {
			if err := conn.Close(); err != nil {
				logger.Default().Warn("close active client connection", zap.Error(err))
			}
		}
		s.mu.Unlock()
	})
}

// ActiveRequests returns authenticated operations that have started and would
// be interrupted by shutting down the desktop app.
func (s *Server) ActiveRequests() int { return int(s.active.Load()) }

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				continue
			}
		}
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close client connection", zap.Error(err))
		}
	}()

	reader := bufio.NewReader(conn)

	// 读取 JSON 请求行
	line, err := reader.ReadBytes('\n')
	if err != nil {
		writeJSONResponse(conn, false, "read request failed")
		return
	}

	var req ProxyRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeJSONResponse(conn, false, "invalid request JSON")
		return
	}

	// 校验认证 token
	if s.authToken != "" && req.Token != s.authToken {
		writeJSONResponse(conn, false, "authentication failed")
		return
	}
	s.active.Add(1)
	defer s.active.Add(-1)

	switch req.Op {
	case "exec":
		s.handleExec(conn, reader, req)
	case "upload":
		s.handleUpload(conn, reader, req)
	case "download":
		s.handleDownload(conn, req)
	case "copy":
		s.handleCopy(conn, req)
	default:
		writeJSONResponse(conn, false, fmt.Sprintf("unknown op: %s", req.Op))
	}
}

// handleExec 处理命令执行或交互式 SSH
func (s *Server) handleExec(conn net.Conn, reader *bufio.Reader, req ProxyRequest) {
	client, err := s.pool.Get(context.Background(), req.AssetID)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("get connection: %v", err))
		return
	}
	defer s.pool.Release(req.AssetID)

	session, err := client.NewSession()
	if err != nil {
		s.handleSSHError(req.AssetID, err)
		writeJSONResponse(conn, false, fmt.Sprintf("create session: %v", err))
		return
	}
	defer func() {
		if err := session.Close(); err != nil {
			logger.Default().Warn("close ssh session", zap.Int64("assetID", req.AssetID), zap.Error(err))
		}
	}()

	// 如果需要 PTY
	if req.PTY {
		cols, rows := req.Cols, req.Rows
		if cols <= 0 {
			cols = 80
		}
		if rows <= 0 {
			rows = 24
		}
		if err := session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}); err != nil {
			writeJSONResponse(conn, false, fmt.Sprintf("request pty: %v", err))
			return
		}
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("stdin pipe: %v", err))
		return
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("stdout pipe: %v", err))
		return
	}

	stderr, err := session.StderrPipe()
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("stderr pipe: %v", err))
		return
	}

	// PTY 模式用 Shell，否则用 Run
	if req.PTY {
		if err := session.Shell(); err != nil {
			writeJSONResponse(conn, false, fmt.Sprintf("start shell: %v", err))
			return
		}
	} else {
		if err := session.Start(req.Command); err != nil {
			writeJSONResponse(conn, false, fmt.Sprintf("start command: %v", err))
			return
		}
	}

	// 握手成功
	writeJSONResponse(conn, true, "")

	done := make(chan struct{})
	var outWg sync.WaitGroup
	var writeMu sync.Mutex // 保护 conn 并发写入，防止帧交错

	// stdout → FrameStdout
	outWg.Add(1)
	go func() {
		defer outWg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				writeMu.Lock()
				writeErr := WriteFrame(conn, FrameStdout, buf[:n])
				writeMu.Unlock()
				if writeErr != nil {
					logger.Default().Warn("write stdout frame", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// stderr → FrameStderr（PTY 模式下 stderr 和 stdout 合并，这个 goroutine 会立即结束）
	outWg.Add(1)
	go func() {
		defer outWg.Done()
		buf := make([]byte, 32*1024)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				writeMu.Lock()
				writeErr := WriteFrame(conn, FrameStderr, buf[:n])
				writeMu.Unlock()
				if writeErr != nil {
					logger.Default().Warn("write stderr frame", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// 读取客户端帧 → stdin / resize
	go func() {
		defer close(done)
		for {
			frameType, payload, err := ReadFrame(reader)
			if err != nil {
				if closeErr := stdin.Close(); closeErr != nil {
					logger.Default().Warn("close stdin pipe", zap.Int64("assetID", req.AssetID), zap.Error(closeErr))
				}
				return
			}
			switch frameType {
			case FrameStdin:
				if _, writeErr := stdin.Write(payload); writeErr != nil {
					logger.Default().Warn("write to stdin", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
				}
			case FrameResize:
				if cols, rows, parseErr := ParseResize(payload); parseErr == nil {
					if resizeErr := session.WindowChange(int(rows), int(cols)); resizeErr != nil {
						logger.Default().Warn("window change", zap.Int64("assetID", req.AssetID), zap.Error(resizeErr))
					}
				}
			}
		}
	}()

	// 等待命令/shell 结束
	exitCode := 0
	if err := session.Wait(); err != nil {
		if exitErr, ok := err.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			// 等待输出 goroutine 结束后再写错误帧，避免帧交错
			outWg.Wait()
			writeMu.Lock()
			if writeErr := WriteError(conn, err.Error()); writeErr != nil {
				logger.Default().Warn("write error frame", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			writeMu.Unlock()
			return
		}
	}

	// 等待 stdout/stderr goroutine 读完所有缓冲数据后再发送退出码
	outWg.Wait()

	writeMu.Lock()
	if err := WriteExitCode(conn, exitCode); err != nil {
		logger.Default().Warn("write exit code frame", zap.Int64("assetID", req.AssetID), zap.Error(err))
	}
	writeMu.Unlock()
	// 等待客户端读循环结束（连接关闭时自然退出）
	<-done
}

// handleUpload 处理文件上传
func (s *Server) handleUpload(conn net.Conn, reader *bufio.Reader, req ProxyRequest) {
	client, err := s.pool.Get(context.Background(), req.AssetID)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("get connection: %v", err))
		return
	}
	defer s.pool.Release(req.AssetID)

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		s.handleSSHError(req.AssetID, err)
		writeJSONResponse(conn, false, fmt.Sprintf("create sftp client: %v", err))
		return
	}
	defer func() {
		if err := sftpClient.Close(); err != nil {
			logger.Default().Warn("close sftp client for upload", zap.Int64("assetID", req.AssetID), zap.Error(err))
		}
	}()

	writeJSONResponse(conn, true, "")

	remoteFile, err := sftpClient.Create(req.DstPath)
	if err != nil {
		if writeErr := WriteFrame(conn, FrameFileErr, []byte(fmt.Sprintf("create remote file: %v", err))); writeErr != nil {
			logger.Default().Warn("write file error frame for upload", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
		}
		return
	}
	defer func() {
		if err := remoteFile.Close(); err != nil {
			logger.Default().Warn("close remote file for upload", zap.Int64("assetID", req.AssetID), zap.String("path", req.DstPath), zap.Error(err))
		}
	}()

	// 读取 FileData 帧直到 FileEOF
	for {
		frameType, payload, err := ReadFrame(reader)
		if err != nil {
			if writeErr := WriteFrame(conn, FrameFileErr, []byte(fmt.Sprintf("read frame: %v", err))); writeErr != nil {
				logger.Default().Warn("write file error frame for upload", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			return
		}
		switch frameType {
		case FrameFileData:
			if _, err := remoteFile.Write(payload); err != nil {
				if writeErr := WriteFrame(conn, FrameFileErr, []byte(fmt.Sprintf("write remote file: %v", err))); writeErr != nil {
					logger.Default().Warn("write file error frame for upload", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
				}
				return
			}
		case FrameFileEOF:
			if writeErr := WriteFrame(conn, FrameOK, nil); writeErr != nil {
				logger.Default().Warn("write ok frame for upload", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			return
		default:
			if writeErr := WriteFrame(conn, FrameFileErr, []byte(fmt.Sprintf("unexpected frame type: 0x%02x", frameType))); writeErr != nil {
				logger.Default().Warn("write file error frame for upload", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			return
		}
	}
}

// handleDownload 处理文件下载
func (s *Server) handleDownload(conn net.Conn, req ProxyRequest) {
	client, err := s.pool.Get(context.Background(), req.AssetID)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("get connection: %v", err))
		return
	}
	defer s.pool.Release(req.AssetID)

	sftpClient, err := sftp.NewClient(client)
	if err != nil {
		s.handleSSHError(req.AssetID, err)
		writeJSONResponse(conn, false, fmt.Sprintf("create sftp client: %v", err))
		return
	}
	defer func() {
		if err := sftpClient.Close(); err != nil {
			logger.Default().Warn("close sftp client for download", zap.Int64("assetID", req.AssetID), zap.Error(err))
		}
	}()

	remoteFile, err := sftpClient.Open(req.SrcPath)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("open remote file: %v", err))
		return
	}
	defer func() {
		if err := remoteFile.Close(); err != nil {
			logger.Default().Warn("close remote file for download", zap.Int64("assetID", req.AssetID), zap.String("path", req.SrcPath), zap.Error(err))
		}
	}()

	writeJSONResponse(conn, true, "")

	// 分块读取远程文件发送 FileData 帧
	buf := make([]byte, 32*1024)
	for {
		n, err := remoteFile.Read(buf)
		if n > 0 {
			if writeErr := WriteFrame(conn, FrameFileData, buf[:n]); writeErr != nil {
				return
			}
		}
		if err == io.EOF {
			if writeErr := WriteFrame(conn, FrameFileEOF, nil); writeErr != nil {
				logger.Default().Warn("write file eof frame for download", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			return
		}
		if err != nil {
			if writeErr := WriteFrame(conn, FrameFileErr, []byte(fmt.Sprintf("read remote file: %v", err))); writeErr != nil {
				logger.Default().Warn("write file error frame for download", zap.Int64("assetID", req.AssetID), zap.Error(writeErr))
			}
			return
		}
	}
}

// handleCopy 处理远程到远程复制
func (s *Server) handleCopy(conn net.Conn, req ProxyRequest) {
	srcClient, err := s.pool.Get(context.Background(), req.SrcAssetID)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("get source connection: %v", err))
		return
	}
	defer s.pool.Release(req.SrcAssetID)

	dstClient, err := s.pool.Get(context.Background(), req.AssetID)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("get destination connection: %v", err))
		return
	}
	defer s.pool.Release(req.AssetID)

	srcSFTP, err := sftp.NewClient(srcClient)
	if err != nil {
		s.handleSSHError(req.SrcAssetID, err)
		writeJSONResponse(conn, false, fmt.Sprintf("create source sftp: %v", err))
		return
	}
	defer func() {
		if err := srcSFTP.Close(); err != nil {
			logger.Default().Warn("close source sftp client for copy", zap.Int64("assetID", req.SrcAssetID), zap.Error(err))
		}
	}()

	dstSFTP, err := sftp.NewClient(dstClient)
	if err != nil {
		s.handleSSHError(req.AssetID, err)
		writeJSONResponse(conn, false, fmt.Sprintf("create destination sftp: %v", err))
		return
	}
	defer func() {
		if err := dstSFTP.Close(); err != nil {
			logger.Default().Warn("close destination sftp client for copy", zap.Int64("assetID", req.AssetID), zap.Error(err))
		}
	}()

	srcFile, err := srcSFTP.Open(req.SrcPath)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("open source file: %v", err))
		return
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			logger.Default().Warn("close source file for copy", zap.String("path", req.SrcPath), zap.Error(err))
		}
	}()

	dstFile, err := dstSFTP.Create(req.DstPath)
	if err != nil {
		writeJSONResponse(conn, false, fmt.Sprintf("create destination file: %v", err))
		return
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			logger.Default().Warn("close destination file for copy", zap.String("path", req.DstPath), zap.Error(err))
		}
	}()

	writeJSONResponse(conn, true, "")

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		if writeErr := WriteFrame(conn, FrameError, []byte(fmt.Sprintf("copy: %v", err))); writeErr != nil {
			logger.Default().Warn("write error frame for copy", zap.Error(writeErr))
		}
		return
	}

	if writeErr := WriteFrame(conn, FrameOK, nil); writeErr != nil {
		logger.Default().Warn("write ok frame for copy", zap.Error(writeErr))
	}
}

// handleSSHError 处理 SSH 连接错误，移除可能已断开的连接
func (s *Server) handleSSHError(assetID int64, err error) {
	// 如果是连接层面的错误，移除缓存的连接
	if isConnectionError(err) {
		s.pool.Remove(assetID)
	}
}

func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	// SSH session 创建失败或 SFTP 创建失败通常意味着底层连接有问题
	if err == io.EOF {
		return true
	}
	if _, ok := err.(*net.OpError); ok {
		return true
	}
	return false
}

func writeJSONResponse(conn net.Conn, ok bool, errMsg string) {
	resp := ProxyResponse{OK: ok, Error: errMsg}
	data, err := json.Marshal(resp)
	if err != nil {
		logger.Default().Warn("marshal JSON response", zap.Error(err))
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		logger.Default().Warn("write JSON response", zap.Error(err))
	}
}
