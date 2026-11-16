package k8s

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	k8spkg "github.com/opskat/opskat/internal/pkg/k8s"
)

// ShellAuto 是 Shell 选择里的「自动」。
const ShellAuto = "auto"

// k8s:exec:end 的结束原因。
const (
	ExecEndExited = "exited" // 命令退出（含退出码）
	ExecEndClosed = "closed" // 前端关闭会话或应用退出
	ExecEndError  = "error"  // 会话建立后连接出错
)

// execConnectTimeout 限定 exec 从拨号到 SPDY 升级完成的时间；升级后的会话不设超时。
const execConnectTimeout = 30 * time.Second

// K8sExecRequest 是 StartK8sExec 的请求。Shell 为 "auto"、"/bin/bash"、"/bin/sh" 或自定义命令（按空白拆成参数）。
type K8sExecRequest struct {
	AssetID   int64  `json:"assetId"`
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
	Shell     string `json:"shell"`
	Cols      int    `json:"cols"`
	Rows      int    `json:"rows"`
}

// K8sExecEnd 是 k8s:exec:end:<id> 事件的负载。
type K8sExecEnd struct {
	Reason   string `json:"reason"`
	ExitCode *int   `json:"exitCode,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (r K8sExecRequest) validate() error {
	if r.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if r.Pod == "" {
		return fmt.Errorf("pod is required")
	}
	if err := validateSegment("namespace", r.Namespace); err != nil {
		return err
	}
	if err := validateSegment("pod", r.Pod); err != nil {
		return err
	}
	if strings.TrimSpace(r.Shell) == "" {
		return fmt.Errorf("shell is required")
	}
	return validateTerminalSize(r.Cols, r.Rows)
}

func validateTerminalSize(cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > math.MaxUint16 || rows > math.MaxUint16 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	return nil
}

// execCommand 把 Shell 选择换成在容器里运行的命令。
// 「自动」目前直接运行 /bin/sh；先试 bash、没有再退回 sh 并上报实际 shell 由后续的探测接管，只改这里。
func execCommand(shell string) []string {
	if shell == ShellAuto {
		return []string{"/bin/sh"}
	}
	return strings.Fields(shell)
}

// execSession 是一个已建立的终端会话：stdin 写入容器，sizes 送出最新的终端尺寸。
type execSession struct {
	cancel context.CancelFunc
	stdin  *io.PipeWriter

	sizeMu sync.Mutex
	sizes  chan k8spkg.TerminalSize
}

// resize 只保留最新的尺寸：容器还没取走的旧尺寸直接被替换。
func (s *execSession) resize(size k8spkg.TerminalSize) {
	s.sizeMu.Lock()
	defer s.sizeMu.Unlock()
	select {
	case <-s.sizes:
	default:
	}
	s.sizes <- size
}

// execOutput 把容器输出按 k8s:exec:data 事件发给前端；ready 关闭（StartK8sExec 已返回会话 ID）前先挂起，
// 免得前端还没订阅时首屏输出就已发出。
type execOutput struct {
	ready <-chan struct{}
	emit  func(data []byte)
}

func (o *execOutput) Write(p []byte) (int, error) {
	<-o.ready
	o.emit(p)
	return len(p), nil
}

// StartK8sExec 在 Pod 的容器里打开交互式终端，连接（经资产的 SSH 隧道 / 代理链）建立后返回会话 ID。
// 之后容器输出以 base64 经 k8s:exec:data:<id> 发出，会话结束时发 k8s:exec:end:<id>。
func (k *K8s) StartK8sExec(req K8sExecRequest) (string, error) {
	if err := req.validate(); err != nil {
		return "", fmt.Errorf("start K8S exec: %w", err)
	}
	loadCtx, cancel := context.WithTimeout(k.ctx, 30*time.Second)
	defer cancel()
	c, err := k.loadK8sCall(loadCtx, req.AssetID)
	if err != nil {
		logger.Ctx(k.ctx).Error("k8s exec failed", execLogFields(req, zap.Error(err))...)
		return "", err
	}
	return k.startExec(c, req)
}

func execLogFields(req K8sExecRequest, extra ...zap.Field) []zap.Field {
	return append([]zap.Field{
		zap.Int64("assetID", req.AssetID),
		zap.String("namespace", req.Namespace),
		zap.String("pod", req.Pod),
		zap.String("container", req.Container),
		zap.String("shell", req.Shell),
	}, extra...)
}

func (k *K8s) startExec(c *k8sCallContext, req K8sExecRequest) (string, error) {
	sessionID := fmt.Sprintf("k8s-exec-%d", k.execCounter.Add(1))
	log := logger.Ctx(k.ctx).With(execLogFields(req, zap.String("sessionID", sessionID))...)
	log.Info("k8s exec start")
	started := time.Now()

	ctx, cancel := context.WithCancel(k.ctx)
	stdinR, stdinW := io.Pipe()
	sess := &execSession{cancel: cancel, stdin: stdinW, sizes: make(chan k8spkg.TerminalSize, 1)}
	sess.resize(k8spkg.TerminalSize{Width: uint16(req.Cols), Height: uint16(req.Rows)})

	// 输出在返回会话 ID 时才放行；连接失败时也放行，免得已到的输出把执行器的复制协程卡住。
	ready := make(chan struct{})
	defer close(ready)
	connected := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- k8spkg.Exec(ctx, c.kubeconfig, k8spkg.ExecRequest{
			Namespace: req.Namespace,
			Pod:       req.Pod,
			Container: req.Container,
			Command:   execCommand(req.Shell),
			Stdin:     stdinR,
			Stdout: &execOutput{ready: ready, emit: func(data []byte) {
				k.emit("k8s:exec:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
			}},
			Sizes:       sess.sizes,
			OnConnected: func() { close(connected) },
		}, c.opts...)
	}()

	timer := time.NewTimer(execConnectTimeout)
	defer timer.Stop()
	var connectErr error
	select {
	case <-connected:
	case err := <-result:
		select {
		case <-connected: // 刚连上就结束了：按已建立的会话交给下面上报结束原因
			result <- err
		default:
			connectErr = err
		}
	case <-timer.C:
		cancel()
		<-result
		connectErr = fmt.Errorf("connection timed out after %s", execConnectTimeout)
	}
	if connectErr != nil {
		cancel()
		_ = stdinR.Close()
		log.Error("k8s exec failed", zap.Duration("elapsed", time.Since(started)), zap.Error(connectErr))
		return "", fmt.Errorf("start K8S exec: %w", connectErr)
	}

	k.execSessions.Store(sessionID, sess)
	log.Info("k8s exec connected", zap.Duration("elapsed", time.Since(started)))
	go func() {
		err := <-result
		k.execSessions.Delete(sessionID)
		closed := ctx.Err() != nil
		cancel()
		_ = stdinR.Close()
		end := execEnd(closed, err)
		fields := []zap.Field{zap.String("reason", end.Reason), zap.Duration("elapsed", time.Since(started))}
		if end.ExitCode != nil {
			fields = append(fields, zap.Int("exitCode", *end.ExitCode))
		}
		if end.Reason == ExecEndError {
			log.Error("k8s exec failed", append(fields, zap.Error(err))...)
		}
		log.Info("k8s exec ended", fields...)
		<-ready
		k.emit("k8s:exec:end:"+sessionID, end)
	}()
	return sessionID, nil
}

// execEnd 把会话结束时的错误换成上报给前端的结束原因；closed 表示会话是被关闭的（StopK8sExec / 应用退出）。
func execEnd(closed bool, err error) K8sExecEnd {
	if closed {
		return K8sExecEnd{Reason: ExecEndClosed}
	}
	if err == nil {
		code := 0
		return K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}
	}
	if code, ok := k8spkg.ExitCode(err); ok {
		return K8sExecEnd{Reason: ExecEndExited, ExitCode: &code}
	}
	return K8sExecEnd{Reason: ExecEndError, Error: err.Error()}
}

func (k *K8s) execSession(sessionID string) (*execSession, error) {
	v, ok := k.execSessions.Load(sessionID)
	if !ok {
		return nil, fmt.Errorf("K8S exec session %s not found", sessionID)
	}
	return v.(*execSession), nil
}

// WriteK8sExec 把终端输入（base64）写进容器。
func (k *K8s) WriteK8sExec(sessionID, dataB64 string) error {
	sess, err := k.execSession(sessionID)
	if err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return fmt.Errorf("decode K8S exec input: %w", err)
	}
	if _, err := sess.stdin.Write(data); err != nil {
		if errors.Is(err, io.ErrClosedPipe) {
			return fmt.Errorf("K8S exec session %s has ended", sessionID)
		}
		return fmt.Errorf("write K8S exec input: %w", err)
	}
	return nil
}

// ResizeK8sExec 把终端尺寸同步给容器。
func (k *K8s) ResizeK8sExec(sessionID string, cols, rows int) error {
	if err := validateTerminalSize(cols, rows); err != nil {
		return err
	}
	sess, err := k.execSession(sessionID)
	if err != nil {
		return err
	}
	sess.resize(k8spkg.TerminalSize{Width: uint16(cols), Height: uint16(rows)})
	return nil
}

// StopK8sExec 结束会话并释放连接；会话随后发出 reason 为 closed 的 k8s:exec:end。
func (k *K8s) StopK8sExec(sessionID string) {
	if v, ok := k.execSessions.Load(sessionID); ok {
		v.(*execSession).cancel()
	}
}
