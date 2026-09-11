package sshpool

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/opskat/opskat/internal/localipc"
	"go.uber.org/zap"
)

// Client 连接到 SSH 代理 socket 的客户端
type Client struct {
	sockPath  string
	authToken string
}

// NewClient 创建客户端
func NewClient(sockPath string) *Client {
	return &Client{sockPath: sockPath}
}

// NewClientWithToken 创建带认证 token 的客户端
func NewClientWithToken(sockPath, token string) *Client {
	return &Client{sockPath: sockPath, authToken: token}
}

// IsAvailable 检测 proxy socket 是否可连
func (c *Client) IsAvailable() bool {
	conn, err := localipc.Dial(c.sockPath)
	if err != nil {
		return false
	}
	if err := conn.Close(); err != nil {
		logger.Default().Warn("close availability check connection", zap.Error(err))
	}
	return true
}

// Exec 通过代理执行命令，返回退出码
func (c *Client) Exec(req ProxyRequest, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	req.Op = "exec"
	conn, reader, err := c.handshake(req)
	if err != nil {
		return -1, err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close exec connection", zap.Error(err))
		}
	}()

	// 发送 stdin 帧
	if stdin != nil {
		go func() {
			buf := make([]byte, 32*1024)
			for {
				n, readErr := stdin.Read(buf)
				if n > 0 {
					if writeErr := WriteFrame(conn, FrameStdin, buf[:n]); writeErr != nil {
						logger.Default().Warn("write stdin frame", zap.Error(writeErr))
						return
					}
				}
				if readErr != nil {
					return
				}
			}
		}()
	}

	// 读取输出帧
	return c.readOutputFrames(reader, stdout, stderr)
}

// InteractiveSSH 通过代理建立交互式 SSH 会话
// resizeCh 传入终端大小变更通知，格式 [cols, rows]
func (c *Client) InteractiveSSH(req ProxyRequest, stdin io.Reader, stdout io.Writer, resizeCh <-chan [2]uint16) (int, error) {
	req.Op = "exec"
	req.PTY = true
	conn, reader, err := c.handshake(req)
	if err != nil {
		return -1, err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close interactive ssh connection", zap.Error(err))
		}
	}()

	// stdin → FrameStdin
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, readErr := stdin.Read(buf)
			if n > 0 {
				if writeErr := WriteFrame(conn, FrameStdin, buf[:n]); writeErr != nil {
					logger.Default().Warn("write interactive stdin frame", zap.Error(writeErr))
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// resize → FrameResize
	if resizeCh != nil {
		go func() {
			for size := range resizeCh {
				if err := WriteResize(conn, size[0], size[1]); err != nil {
					logger.Default().Warn("write resize frame", zap.Error(err))
				}
			}
		}()
	}

	// 读取输出帧（PTY 模式下 stdout 和 stderr 合并到 stdout）
	return c.readOutputFrames(reader, stdout, stdout)
}

// Upload 通过代理上传文件
func (c *Client) Upload(req ProxyRequest, localFile io.Reader) error {
	req.Op = "upload"
	conn, reader, err := c.handshake(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close upload connection", zap.Error(err))
		}
	}()

	// 分块发送文件数据
	buf := make([]byte, 32*1024)
	for {
		n, err := localFile.Read(buf)
		if n > 0 {
			if writeErr := WriteFrame(conn, FrameFileData, buf[:n]); writeErr != nil {
				return fmt.Errorf("write file data: %w", writeErr)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read local file: %w", err)
		}
	}

	// 发送 EOF
	if err := WriteFrame(conn, FrameFileEOF, nil); err != nil {
		return fmt.Errorf("write file eof: %w", err)
	}

	// 等待响应
	frameType, payload, err := ReadFrame(reader)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	switch frameType {
	case FrameOK:
		return nil
	case FrameFileErr:
		return fmt.Errorf("remote error: %s", string(payload))
	default:
		return fmt.Errorf("unexpected frame type: 0x%02x", frameType)
	}
}

// Download 通过代理下载文件
func (c *Client) Download(req ProxyRequest, localFile io.Writer) error {
	req.Op = "download"
	conn, reader, err := c.handshake(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close download connection", zap.Error(err))
		}
	}()

	// 读取文件数据帧
	for {
		frameType, payload, err := ReadFrame(reader)
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}
		switch frameType {
		case FrameFileData:
			if _, err := localFile.Write(payload); err != nil {
				return fmt.Errorf("write local file: %w", err)
			}
		case FrameFileEOF:
			return nil
		case FrameFileErr:
			return fmt.Errorf("remote error: %s", string(payload))
		default:
			return fmt.Errorf("unexpected frame type: 0x%02x", frameType)
		}
	}
}

// Copy 通过代理进行远程到远程复制
func (c *Client) Copy(req ProxyRequest) error {
	req.Op = "copy"
	conn, reader, err := c.handshake(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Default().Warn("close copy connection", zap.Error(err))
		}
	}()

	// 等待完成
	frameType, payload, err := ReadFrame(reader)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	switch frameType {
	case FrameOK:
		return nil
	case FrameError:
		return fmt.Errorf("remote error: %s", string(payload))
	default:
		return fmt.Errorf("unexpected frame type: 0x%02x", frameType)
	}
}

// handshake 连接 socket 并完成 JSON 握手
func (c *Client) handshake(req ProxyRequest) (net.Conn, *bufio.Reader, error) {
	conn, err := localipc.Dial(c.sockPath)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot connect to desktop app (is it running?): %w", err)
	}

	// 注入认证 token
	if c.authToken != "" {
		req.Token = c.authToken
	}

	// 发送 JSON 请求
	data, err := json.Marshal(req)
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after marshal error", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after write error", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("send request: %w", err)
	}

	// 读取 JSON 响应
	reader := bufio.NewReader(conn)
	respLine, err := reader.ReadBytes('\n')
	if err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after read error", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	var resp ProxyResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after unmarshal error", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if !resp.OK {
		if closeErr := conn.Close(); closeErr != nil {
			logger.Default().Warn("close connection after proxy error", zap.Error(closeErr))
		}
		return nil, nil, fmt.Errorf("proxy error: %s", resp.Error)
	}

	return conn, reader, nil
}

// readOutputFrames 读取输出帧直到 ExitCode 或 Error
func (c *Client) readOutputFrames(reader *bufio.Reader, stdout, stderr io.Writer) (int, error) {
	for {
		frameType, payload, err := ReadFrame(reader)
		if err != nil {
			if err == io.EOF {
				return 0, nil
			}
			return -1, fmt.Errorf("read frame: %w", err)
		}
		switch frameType {
		case FrameStdout:
			if stdout != nil {
				if _, err := stdout.Write(payload); err != nil {
					logger.Default().Warn("write stdout", zap.Error(err))
				}
			}
		case FrameStderr:
			if stderr != nil {
				if _, err := stderr.Write(payload); err != nil {
					logger.Default().Warn("write stderr", zap.Error(err))
				}
			}
		case FrameExitCode:
			code, err := ParseExitCode(payload)
			if err != nil {
				return -1, err
			}
			return code, nil
		case FrameError:
			return -1, fmt.Errorf("remote error: %s", string(payload))
		}
	}
}
