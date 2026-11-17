package k8s

import (
	"context"
	"crypto/rand"
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
	ExecEndFailed = "failed" // 集群报告命令无法运行（如自定义命令不存在），原因在 Error 里
)

// 会话非正常结束后读 Pod 补结束原因：kubelet 写入容器终止状态晚于 exec 流结束，读不到时每隔
// execEndPollInterval 再读，最多 execEndPollWindow。
var (
	execEndPollInterval = 500 * time.Millisecond
	execEndPollWindow   = 5 * time.Second
)

// execConnectTimeout 限定 exec 从拨号到 SPDY 升级完成的时间；升级后的会话不设超时。
const execConnectTimeout = 30 * time.Second

// execShellEndTimeout 限定会话结束后在容器里结束 shell 的那次 exec（含拨号与约 1 秒的 SIGHUP 宽限）。
var execShellEndTimeout = 10 * time.Second

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
	// shellTag 是当前这次尝试的 shell 标记（k8spkg.ShellCommand），会话结束时据此在容器里结束 shell；
	// 不带包装运行时为空。
	shellTag string
}

// begin 为一次尝试准备 stdin 管道和尺寸通道（带上当前尺寸），并记下这次尝试的 shell 标记。
func (s *execSession) begin(shellTag string) (*io.PipeReader, *io.PipeWriter, <-chan k8spkg.TerminalSize) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, w := io.Pipe()
	s.stdin = w
	s.sizes = make(chan k8spkg.TerminalSize, 1)
	s.sizes <- s.size
	s.shellTag = shellTag
	return r, w, s.sizes
}

func (s *execSession) currentShellTag() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shellTag
}

func (s *execSession) writeStdin(data []byte) error {
	s.mu.Lock()
	w := s.stdin
	s.mu.Unlock()
	if w == nil {
		return errors.New("K8S exec session is not connected yet")
	}
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
		logger.Ctx(k.ctx).Error("k8s exec failed", execLogFields(req, zap.Error(err))...)
		return "", fmt.Errorf("start K8S exec: %w", err)
	}
	loadCtx, cancel := context.WithTimeout(k.ctx, 30*time.Second)
	defer cancel()
	c, err := k.call(loadCtx, req.AssetID)
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
	// 连接期间就登记：重复的 ID 不能顶掉仍在运行的会话，连接中的会话也能被 StopK8sExec / Cleanup 停掉。
	if _, loaded := k.execSessions.LoadOrStore(sessionID, sess); loaded {
		cancel()
		err := fmt.Errorf("session id %s is already in use", sessionID)
		log.Error("k8s exec failed", zap.Error(err))
		return "", fmt.Errorf("start K8S exec: %w", err)
	}

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
		k.execSessions.Delete(sessionID)
		log.Error("k8s exec failed", zap.Duration("elapsed", time.Since(started)), zap.Error(connectErr))
		return "", fmt.Errorf("start K8S exec: %w", connectErr)
	}

	log.Info("k8s exec connected", zap.Duration("elapsed", time.Since(started)))
	go func() {
		err := <-result
		k.execSessions.Delete(sessionID)
		closed := ctx.Err() != nil
		cancel()
		end := execEnd(closed, err)
		// 命令退出码非 0 或连接出错时容器可能已终止 / Pod 已删除；集群拒绝运行命令（failed）时容器无恙，不必读。
		if end.Reason == ExecEndError || (end.Reason == ExecEndExited && *end.ExitCode != 0) {
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
		if end.Reason == ExecEndError || end.Reason == ExecEndFailed {
			log.Error("k8s exec failed", append(fields, zap.Error(err))...)
		}
		log.Info("k8s exec ended", fields...)
		k.emit("k8s:exec:end:"+sessionID, end)
		k.endShell(c, req, sess.currentShellTag(), end, log)
	}()
	return sessionID, nil
}

// endShell 在容器里结束会话启动的 shell 及其前台程序（kubelet 不会在 exec 连接断开时结束它们，见 k8spkg.ShellCommand）。
// 在会话的结束协程里、结束事件发出之后运行，有时限；做不到只记日志，不影响已上报的结束原因。
func (k *K8s) endShell(c *k8sCallContext, req K8sExecRequest, tag string, end K8sExecEnd, log *zap.Logger) {
	// 命令没能运行、Pod 已删除或容器已终止时容器里没有要结束的 shell。
	if end.Reason == ExecEndFailed || end.PodGone || end.ContainerReason != "" {
		return
	}
	if tag == "" {
		// 未经包装的 shell 自己退出了就没有遗留；被关闭或断开时它可能还在，但找不到它。
		if end.Reason != ExecEndExited {
			log.Warn("k8s exec shell cleanup unavailable",
				zap.String("reason", "the container has no /bin/sh, so the shell was started without tracking"))
		}
		return
	}
	// shell 自己退出时这一步只删掉记录。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(k.ctx), execShellEndTimeout)
	defer cancel()
	started := time.Now()
	log.Info("k8s exec shell cleanup start")
	err := k8spkg.EndShell(ctx, c.conn, k8spkg.ExecTarget{Namespace: req.Namespace, Pod: req.Pod, Container: req.Container}, tag)
	if err != nil {
		log.Warn("k8s exec shell cleanup failed", zap.Duration("elapsed", time.Since(started)), zap.Error(err))
		return
	}
	log.Info("k8s exec shell cleanup done", zap.Duration("elapsed", time.Since(started)))
}

// runAttempts 依次尝试 attempts：「自动」的 bash 在容器里不存在（且还没有任何输出）时换 sh 重来，
// 对前端不可见；返回最后一次尝试的结果。onConnected 在首次 SPDY 升级成功后调用。
// 每次尝试都经 k8spkg.ShellCommand 包装，以便会话结束时结束容器里的 shell；最后一次尝试的包装报告命令不存在时
// （或容器里根本没有 /bin/sh），不带包装再运行一次：让集群给出它自己的原因，没有 sh 的容器也照常可用（只是无法清理）。
func (k *K8s) runAttempts(ctx context.Context, c *k8sCallContext, req K8sExecRequest, sess *execSession,
	attempts []execAttempt, sessionID string, log *zap.Logger, onConnected func()) error {
	for i, a := range attempts {
		var startedOnce sync.Once
		emitStarted := func() {
			startedOnce.Do(func() { k.emit("k8s:exec:started:"+sessionID, K8sExecStarted{Shell: a.Shell}) })
		}
		// 只有一种可能的 shell 时连上即知；「自动」要等首次输出才能确定没有回退。
		announceOnConnect := len(attempts) == 1
		tag := newShellTag()
		connected, produced, err := k.runAttempt(ctx, c, req, sess, k8spkg.ShellCommand(tag, a.Command), tag, sessionID,
			emitStarted, announceOnConnect, onConnected)
		if connected && !produced && ctx.Err() == nil && shellMissing(err) {
			if i < len(attempts)-1 {
				log.Info("k8s exec shell missing, falling back", zap.String("shell", a.Shell), zap.Error(err))
				continue
			}
			log.Info("k8s exec wrapped command did not start, running it untracked", zap.Error(err))
			connected, _, err = k.runAttempt(ctx, c, req, sess, a.Command, "", sessionID, emitStarted, announceOnConnect, onConnected)
		}
		if connected {
			emitStarted()
		}
		return err
	}
	return nil
}

// runAttempt 运行一次 exec：返回是否完成了 SPDY 升级、是否已有输出，以及 exec 的结果。
func (k *K8s) runAttempt(ctx context.Context, c *k8sCallContext, req K8sExecRequest, sess *execSession,
	command []string, shellTag, sessionID string, emitStarted func(), announceOnConnect bool, onConnected func()) (bool, bool, error) {
	stdinR, stdinW, sizes := sess.begin(shellTag)
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	var attemptConnected atomic.Bool
	out := &execOutput{
		onFirst: emitStarted,
		emit: func(data []byte) {
			k.emit("k8s:exec:data:"+sessionID, base64.StdEncoding.EncodeToString(data))
		},
	}
	err := k8spkg.Exec(attemptCtx, c.conn, k8spkg.ExecRequest{
		Namespace: req.Namespace,
		Pod:       req.Pod,
		Container: req.Container,
		Command:   command,
		Stdin:     stdinR,
		Stdout:    out,
		Sizes:     sizes,
		OnConnected: func() {
			attemptConnected.Store(true)
			onConnected()
			if announceOnConnect {
				emitStarted()
			}
		},
	})
	cancelAttempt()
	// 关写端让这次尝试遗留的 stdin 读取协程读到 EOF 后退出，不再抢后面输入。
	_ = stdinW.Close()
	return attemptConnected.Load(), out.produced.Load(), err
}

// enrichEnd 在会话非正常结束（非 0 退出或连接出错）后读 Pod 状态补充结束原因：容器终止原因（如 OOMKilled）、
// Pod 是否已被删除（已不存在，或正在删除）。kubelet 写入终止状态晚于 exec 流结束，所以结束方式像容器被终止
// 而两者都读不到时隔一会儿再读，直到 execEndPollWindow 用完；读失败（多半是连接已断）时保留原结束信息。
func (k *K8s) enrichEnd(c *k8sCallContext, req K8sExecRequest, since time.Time, end *K8sExecEnd, log *zap.Logger) {
	ctx, cancel := context.WithTimeout(k.ctx, execEndPollWindow)
	defer cancel()
	for {
		pod, err := k8spkg.GetResource(ctx, c.conn, k8spkg.ResourceRef{
			Version: "v1", Resource: "pods", Namespace: req.Namespace, Name: req.Pod,
		})
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("k8s exec end: read pod status failed", zap.Error(err))
			}
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
			return
		}
		if typed.DeletionTimestamp != nil {
			end.PodGone = true
			return
		}
		if !containerMayHaveDied(*end) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(execEndPollInterval):
		}
	}
}

// containerMayHaveDied 判断结束方式是否像容器被终止：连接出错，或以信号退出（128+n，如 OOMKilled 的 137）。
// 普通的非 0 退出（如在 shell 里 exit 1）只读一次 Pod，不为等终止状态拖延结束提示。
func containerMayHaveDied(end K8sExecEnd) bool {
	return end.Reason == ExecEndError || (end.ExitCode != nil && *end.ExitCode >= 128)
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
	var cmdErr *k8spkg.CommandError
	if errors.As(err, &cmdErr) {
		return K8sExecEnd{Reason: ExecEndFailed, Error: err.Error()}
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

// StopK8sExec 结束会话并释放连接；已建立的会话随后发出 reason 为 closed 的 k8s:exec:end，连接中的会话则让 StartK8sExec 返回错误。
func (k *K8s) StopK8sExec(sessionID string) {
	if v, ok := k.execSessions.Load(sessionID); ok {
		v.(*execSession).cancel()
	}
}

// newShellTag 生成每次尝试的 shell 标记（见 k8spkg.ShellCommand）。
var newShellTag = rand.Text
