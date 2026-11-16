package k8s

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

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
	SessionID string `json:"sessionId"`
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
	// ContainerReason 是容器的终止原因（如 OOMKilled），取自 Pod 状态；PodGone 表示 Pod 已被删除。
	ContainerReason string `json:"containerReason,omitempty"`
	PodGone         bool   `json:"podGone,omitempty"`
}

// K8sExecStarted 是 k8s:exec:started:<id> 的负载：会话实际运行的 shell（「自动」时可能是回退后的 sh）。
type K8sExecStarted struct {
	Shell string `json:"shell"`
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
	if r.SessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if strings.TrimSpace(r.Shell) == "" {
		return fmt.Errorf("shell is required")
	}
	if _, err := r.attempts(); err != nil {
		return err
	}
	return validateTerminalSize(r.Cols, r.Rows)
}

func validateTerminalSize(cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > math.MaxUint16 || rows > math.MaxUint16 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	return nil
}

// execAttempt 是一次要尝试的 shell：Shell 是对用户/前端展示的名字，Command 是容器里运行的 argv。
type execAttempt struct {
	Shell   string
	Command []string
}

// attempts 把 Shell 选择换成要依次尝试的命令：「自动」先 bash、容器里没有再 sh；
// 其余（/bin/bash、/bin/sh、自定义命令）只试一次——自定义命令按 shell 词法拆成 argv（引号可用），不经 shell 解释。
func (r K8sExecRequest) attempts() ([]execAttempt, error) {
	if r.Shell == ShellAuto {
		return []execAttempt{{"/bin/bash", []string{"/bin/bash"}}, {"/bin/sh", []string{"/bin/sh"}}}, nil
	}
	argv, err := splitCommand(r.Shell)
	if err != nil {
		return nil, fmt.Errorf("invalid shell command: %w", err)
	}
	if len(argv) == 0 {
		return nil, fmt.Errorf("shell is required")
	}
	return []execAttempt{{r.Shell, argv}}, nil
}

// splitCommand 按 shell 词法把命令拆成参数：空白分隔，单引号 / 双引号包裹，反斜杠转义；不做任何变量或通配展开。
func splitCommand(s string) ([]string, error) {
	var (
		args   []string
		cur    strings.Builder
		inWord bool
		quote  rune
	)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\'):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == '\\':
			if i+1 == len(rs) {
				return nil, fmt.Errorf("trailing backslash")
			}
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case unicode.IsSpace(r):
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}

// shellMissing 判断一次 exec 的失败是不是「容器里没有这个可执行文件」：运行时以 126 / 127 退出，
// 或在 error 流上报「executable file not found」（各运行时措辞不同，统一看关键词）。
func shellMissing(err error) bool {
	if err == nil {
		return false
	}
	if code, ok := k8spkg.ExitCode(err); ok {
		return code == 126 || code == 127
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "not found in $path") ||
		strings.Contains(msg, "no such file or directory")
}

// execSession 是一个已建立的终端会话：stdin 写入容器，sizes 送出最新的终端尺寸。
// 「自动」回退时会换一组 stdin / sizes（每次尝试各用一组，免得上一次遗留的读取协程抢走新会话的输入）。
type execSession struct {
	cancel context.CancelFunc

	mu    sync.Mutex
	stdin *io.PipeWriter
	sizes chan k8spkg.TerminalSize
	size  k8spkg.TerminalSize
}

// begin 为一次尝试准备 stdin 管道和尺寸通道（带上当前尺寸）。
func (s *execSession) begin() (*io.PipeReader, *io.PipeWriter, <-chan k8spkg.TerminalSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, w := io.Pipe()
	s.stdin = w
	s.sizes = make(chan k8spkg.TerminalSize, 1)
	s.sizes <- s.size
	return r, w, s.sizes
}

func (s *execSession) writeStdin(data []byte) error {
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	_, err := w.Write(data)
	return err
}

// resize 只保留最新的尺寸：容器还没取走的旧尺寸直接被替换。
func (s *execSession) resize(size k8spkg.TerminalSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = size
	if s.sizes == nil {
		return
	}
	select {
	case <-s.sizes:
	default:
	}
	s.sizes <- size
}

// execOutput 把容器输出按 k8s:exec:data 事件发给前端，并记下这次尝试是否已有输出、首次输出时通知一次。
type execOutput struct {
	emit     func(data []byte)
	onFirst  func()
	produced atomic.Bool
}

func (o *execOutput) Write(p []byte) (int, error) {
	if o.produced.CompareAndSwap(false, true) {
		o.onFirst()
	}
	o.emit(p)
	return len(p), nil
}

// StartK8sExec 在 Pod 的容器里打开交互式终端，连接（经资产的 SSH 隧道 / 代理链）建立后返回会话 ID（即请求里的 SessionID）。
// 会话 ID 由前端生成并先订阅事件再调用，所以首屏输出不会丢。之后容器输出以 base64 经 k8s:exec:data:<id> 发出，
// 实际运行的 shell 经 k8s:exec:started:<id> 告知，会话结束时发 k8s:exec:end:<id>。
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
	attempts, err := req.attempts()
	if err != nil {
		return "", fmt.Errorf("start K8S exec: %w", err)
	}
	sessionID := req.SessionID
	log := logger.Ctx(k.ctx).With(execLogFields(req, zap.String("sessionID", sessionID))...)
	log.Info("k8s exec start")
	started := time.Now()

	ctx, cancel := context.WithCancel(k.ctx)
	sess := &execSession{cancel: cancel, size: k8spkg.TerminalSize{Width: uint16(req.Cols), Height: uint16(req.Rows)}}

	connected := make(chan struct{})
	var connectedOnce sync.Once
	result := make(chan error, 1)
	go func() {
		result <- k.runAttempts(ctx, c, req, sess, attempts, sessionID, log, func() { connectedOnce.Do(func() { close(connected) }) })
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
		end := execEnd(closed, err)
		if !closed && (end.ExitCode == nil || *end.ExitCode != 0) {
			k.enrichEnd(c, req, started, &end, log)
		}
		fields := []zap.Field{zap.String("reason", end.Reason), zap.Duration("elapsed", time.Since(started))}
		if end.ExitCode != nil {
			fields = append(fields, zap.Int("exitCode", *end.ExitCode))
		}
		if end.ContainerReason != "" {
			fields = append(fields, zap.String("containerReason", end.ContainerReason))
		}
		if end.PodGone {
			fields = append(fields, zap.Bool("podGone", true))
		}
		if end.Reason == ExecEndError {
			log.Error("k8s exec failed", append(fields, zap.Error(err))...)
		}
		log.Info("k8s exec ended", fields...)
		k.emit("k8s:exec:end:"+sessionID, end)
	}()
	return sessionID, nil
}

// runAttempts 依次尝试 attempts：「自动」的 bash 在容器里不存在（且还没有任何输出）时换 sh 重来，
// 对前端不可见；返回最后一次尝试的结果。onConnected 在首次 SPDY 升级成功后调用。
func (k *K8s) runAttempts(ctx context.Context, c *k8sCallContext, req K8sExecRequest, sess *execSession,
	attempts []execAttempt, sessionID string, log *zap.Logger, onConnected func()) error {
	for i, a := range attempts {
		stdinR, stdinW, sizes := sess.begin()
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		var startedOnce sync.Once
		emitStarted := func() {
			startedOnce.Do(func() { k.emit("k8s:exec:started:"+sessionID, K8sExecStarted{Shell: a.Shell}) })
		}
		var attemptConnected atomic.Bool
		out := &execOutput{
			onFirst: emitStarted,
			emit: func(data []byte) {
				k.emit("k8s:exec:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
			},
		}
		err := k8spkg.Exec(attemptCtx, c.kubeconfig, k8spkg.ExecRequest{
			Namespace: req.Namespace,
			Pod:       req.Pod,
			Container: req.Container,
			Command:   a.Command,
			Stdin:     stdinR,
			Stdout:    out,
			Sizes:     sizes,
			OnConnected: func() {
				attemptConnected.Store(true)
				onConnected()
				// 只有一种可能的 shell 时连上即知；「自动」要等首次输出才能确定没有回退。
				if len(attempts) == 1 {
					emitStarted()
				}
			},
		}, c.opts...)
		cancelAttempt()
		// 关写端让这次尝试遗留的 stdin 读取协程读到 EOF 后退出，不再抢后面输入。
		_ = stdinW.Close()

		if i < len(attempts)-1 && attemptConnected.Load() && !out.produced.Load() && ctx.Err() == nil && shellMissing(err) {
			log.Info("k8s exec shell missing, falling back", zap.String("shell", a.Shell), zap.Error(err))
			continue
		}
		if attemptConnected.Load() {
			emitStarted()
		}
		return err
	}
	return nil
}

// enrichEnd 在会话非正常结束（非 0 退出或连接出错）后读 Pod 状态补充结束原因：容器终止原因（如 OOMKilled）、
// Pod 是否已被删除。读不到（多半是连接已断）时保留原结束信息。
func (k *K8s) enrichEnd(c *k8sCallContext, req K8sExecRequest, since time.Time, end *K8sExecEnd, log *zap.Logger) {
	ctx, cancel := context.WithTimeout(k.ctx, 5*time.Second)
	defer cancel()
	pod, err := k8spkg.GetResource(ctx, c.kubeconfig, k8spkg.ResourceRef{
		Version: "v1", Resource: "pods", Namespace: req.Namespace, Name: req.Pod,
	}, c.opts...)
	if err != nil {
		log.Warn("k8s exec end: read pod status failed", zap.Error(err))
		return
	}
	if pod.NotFound {
		end.PodGone = true
		return
	}
	raw, err := json.Marshal(pod.Object)
	if err != nil {
		log.Warn("k8s exec end: encode pod failed", zap.Error(err))
		return
	}
	var typed corev1.Pod
	if err := json.Unmarshal(raw, &typed); err != nil {
		log.Warn("k8s exec end: decode pod failed", zap.Error(err))
		return
	}
	if term := containerTermination(typed, req.Container, since); term != nil {
		end.ContainerReason = term.Reason
		if end.ExitCode == nil {
			code := int(term.ExitCode)
			end.ExitCode = &code
		}
	}
}

// containerTermination 取容器在 since 之后的终止信息（当前终止状态优先，其次重启前的上一次终止）；
// 早于 since 的终止属于更早的会话，不算。
func containerTermination(pod corev1.Pod, container string, since time.Time) *corev1.ContainerStateTerminated {
	since = since.Truncate(time.Second) // 状态里的时间只到秒
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name != container {
			continue
		}
		for _, term := range []*corev1.ContainerStateTerminated{cs.State.Terminated, cs.LastTerminationState.Terminated} {
			if term != nil && !term.FinishedAt.Time.Before(since) {
				return term
			}
		}
	}
	return nil
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
	if err := sess.writeStdin(data); err != nil {
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
