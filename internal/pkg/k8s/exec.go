package k8s

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"
	utilexec "k8s.io/client-go/util/exec"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	streamspdy "k8s.io/streaming/pkg/httpstream/spdy"
)

// TerminalSize 是终端的列数（Width）与行数（Height）。
type TerminalSize struct {
	Width  uint16
	Height uint16
}

// ExecRequest 描述一次交互式（TTY）exec：Stdin 送进容器，容器输出（TTY 下 stderr 已并入）写到 Stdout，
// Sizes 上的每个尺寸同步给容器；OnConnected 在 SPDY 升级成功后调用一次。
type ExecRequest struct {
	Namespace   string
	Pod         string
	Container   string
	Command     []string
	Stdin       io.Reader
	Stdout      io.Writer
	Sizes       <-chan TerminalSize
	OnConnected func()
}

// Exec 在容器里运行命令直到它退出、连接断开或 ctx 取消；命令以非 0 退出时返回的错误可用 ExitCode 取出退出码。
func Exec(ctx context.Context, conn *Conn, req ExecRequest) error {
	target := ExecTarget{Namespace: req.Namespace, Pod: req.Pod, Container: req.Container}
	return execStream(ctx, conn, target, req.Command, req.OnConnected, remotecommand.StreamOptions{
		Stdin:             req.Stdin,
		Stdout:            req.Stdout,
		Tty:               true,
		TerminalSizeQueue: &sizeQueue{ctx: ctx, sizes: req.Sizes},
	})
}

// ExecTarget 是 exec 所在的容器。
type ExecTarget struct {
	Namespace string
	Pod       string
	Container string
}

// execStream 按 streamOpts 建流（有 Stdin 才开 stdin，Tty 决定是否分配终端）运行一次 exec。
func execStream(ctx context.Context, conn *Conn, target ExecTarget, command []string, onConnected func(),
	streamOpts remotecommand.StreamOptions) error {
	// exec 会话是长连接，不能套用读取类请求的 30 秒超时；它经 SPDY 升级独占一条自己拨出的连接。
	config := conn.streamConfig()
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create k8s clientset: %w", err)
	}
	execURL := clientset.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(target.Namespace).Name(target.Pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: target.Container,
			Command:   command,
			Stdin:     streamOpts.Stdin != nil,
			Stdout:    streamOpts.Stdout != nil,
			TTY:       streamOpts.Tty,
		}, scheme.ParameterCodec).URL()

	statusSeen := &atomic.Bool{}
	transport, upgrader, err := spdyTransport(config, onConnected, statusSeen)
	if err != nil {
		return fmt.Errorf("create exec transport: %w", err)
	}
	executor, err := remotecommand.NewSPDYExecutorForTransports(transport, upgrader, http.MethodPost, execURL)
	if err != nil {
		return fmt.Errorf("create exec executor: %w", err)
	}
	err = executor.StreamWithContext(ctx, streamOpts)
	// 命令正常结束时服务端会在 error 流上写一条 Success 状态；连接被掐断时 error 流直接 EOF，
	// client-go 把两者都当成 nil，所以靠有没有收到状态来区分。
	if err == nil && !statusSeen.Load() {
		return ErrConnectionClosed
	}
	// error 流上收到的非退出码失败是集群对命令本身的回答（如可执行文件不存在），不是连接问题。
	if _, isExit := ExitCode(err); err != nil && !isExit && statusSeen.Load() {
		return &CommandError{Err: err}
	}
	return err
}

// 结束会话时为什么要另起一次 exec 去结束 shell：kubelet / CRI 运行时（containerd 等）在 exec 的流式连接断开时
// 不会结束 exec 启动的进程，TTY 会话里半关闭的 stdin 也到不了 shell，所以只断开连接会在容器里遗留 shell 及其前台程序。
// 做法是会话的命令经 ShellCommand 的包装脚本启动：脚本把自己的进程号与启动时间记到 /tmp（不可写时 /dev/shm）下
// 以 tag 命名的隐藏文件，再 exec 成用户的命令（进程号不变、终端里不输出任何东西）；会话结束时 EndShell 以一次非交互
// exec 读回该记录，确认进程仍是那个 shell（防进程号复用）后向它所在会话的全部进程发 SIGHUP（同真实终端挂断），
// 稍后对仍在运行的 shell 与前台进程组补 SIGKILL。只用 POSIX sh 的内建命令与 /proc，bash / busybox sh 容器都可用；
// tag 每次会话随机生成，不会碰到其他会话的进程。

// shellScript 是会话命令的包装：$1 是 tag，其后是用户的 argv。命令不存在时不留输出、以 127 退出，
// 让调用方仍能按「可执行文件不存在」处理（「自动」回退到 sh，或不带包装重跑以拿到集群自己的原因）。
const shellScript = `t=$1; shift
command -v "$1" >/dev/null 2>&1 || exit 127
st() { set -- ${1##*") "}; echo "${20}"; }
s=; read -r s 2>/dev/null </proc/$$/stat
r="$$ $(st "$s")"
for d in /tmp /dev/shm; do { echo "$r" >"$d/.opskat-shell-$t"; } 2>/dev/null && break; done
exec "$@"`

// endShellScript 结束 tag 记录的 shell（见上）；做不到时在 stdout 说明原因并以非 0 退出。shell 已经退出不算失败。
const endShellScript = `exec 2>&1
t=$1 pid= start=
[ -r /proc/self/stat ] || { echo "/proc is not available in the container"; exit 4; }
for d in /tmp /dev/shm; do
	f=$d/.opskat-shell-$t
	if [ -f "$f" ]; then read -r pid start <"$f"; rm -f "$f" 2>/dev/null; break; fi
done
[ -n "$pid" ] || { echo "no record of the shell in /tmp or /dev/shm (not writable, or already ended)"; exit 3; }
s=; read -r s 2>/dev/null <"/proc/$pid/stat" || exit 0
set -- ${s##*") "}
[ "$1" != Z ] && [ "${20}" = "$start" ] || exit 0
sid=$4 tpgid=$6 all=$pid fg=$pid
if [ "$sid" = "$pid" ]; then
	for d in /proc/[0-9]*; do
		p=${d#/proc/}
		[ "$p" = "$pid" ] && continue
		s=; read -r s 2>/dev/null <"$d/stat" || continue
		set -- ${s##*") "}
		[ "$4" = "$pid" ] || continue
		all="$all $p"
		[ "$3" = "$tpgid" ] && fg="$fg $p"
	done
fi
live() { for p in $fg; do s=; read -r s 2>/dev/null <"/proc/$p/stat" || continue; set -- ${s##*") "}; [ "$1" = Z ] || echo "$p"; done; }
kill -HUP $all 2>/dev/null
kill -CONT $all 2>/dev/null
i=0
while [ -n "$(live)" ] && [ $i -lt 5 ]; do sleep 0.2 2>/dev/null || break; i=$((i+1)); done
left=$(live)
[ -n "$left" ] || exit 0
kill -KILL $left 2>/dev/null
sleep 0.2 2>/dev/null
left=$(live)
[ -z "$left" ] || { echo "still running after SIGKILL: $left"; exit 5; }`

// ShellCommandNotFound 是 ShellCommand 的包装找不到用户命令时的退出码（shellScript 第二行）。
const ShellCommandNotFound = 127

// ShellCommand 把会话要运行的 argv 包进记录进程号的脚本（见上）；argv 作为脚本参数原样传入，不经 shell 再解释。
func ShellCommand(tag string, argv []string) []string {
	return append([]string{"/bin/sh", "-c", shellScript, "opskat-shell", tag}, argv...)
}

// EndShellCommand 是 EndShell 在容器里运行的命令。
func EndShellCommand(tag string) []string {
	return []string{"/bin/sh", "-c", endShellScript, "opskat-shell-end", tag}
}

// EndShell 结束经 ShellCommand(tag, …) 启动、仍在容器里运行的 shell 及其前台程序；失败时错误里带上容器内给出的原因。
func EndShell(ctx context.Context, conn *Conn, target ExecTarget, tag string) error {
	var out limitedBuffer
	err := execStream(ctx, conn, target, EndShellCommand(tag), nil, remotecommand.StreamOptions{Stdout: &out})
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(out.String()); msg != "" {
		return fmt.Errorf("%s: %w", msg, err)
	}
	return err
}

// limitedBuffer 只留前 4KB 输出（清理脚本的原因只有一行）；ctx 取消时执行器先返回、输出协程可能还在写，所以加锁。
type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 4096 - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ErrConnectionClosed 表示连接在命令结束前被对端或网络断开（没有收到命令的退出状态）。
var ErrConnectionClosed = errors.New("connection closed before the command finished")

// CommandError 是集群在 exec 的 error 流上报告的命令失败（命令没能运行，例如可执行文件不存在），
// 区别于连接错误；Error() 是集群原样给出的原因。
type CommandError struct {
	Err error
}

func (e *CommandError) Error() string { return e.Err.Error() }

func (e *CommandError) Unwrap() error { return e.Err }

// spdyTransport 构造 exec 用的 SPDY 传输（等同 client-go 的 spdy.RoundTripperFor，另加两点）：
//   - 配置了拨号器（SSH 隧道 / 代理链 / .local）时，升级连接经它拨出。client-go 默认的 SPDY 升级连接自己拨号、
//     不看 config.Dial，会绕过资产的隧道与代理；WebSocket 执行器同样忽略 config.Dial，所以 exec 只走 SPDY。
//   - onConnected 非空时在 SPDY 升级成功后调用一次。
func spdyTransport(config *rest.Config, onConnected func(), statusSeen *atomic.Bool) (http.RoundTripper, spdy.Upgrader, error) {
	tlsConfig, err := rest.TLSConfigFor(config)
	if err != nil {
		return nil, nil, err
	}
	rtConfig := streamspdy.RoundTripperConfig{PingPeriod: 5 * time.Second}
	if config.Dial != nil {
		if tlsConfig == nil {
			// 没有任何 TLS 设置时 client-go 用系统根证书校验；而 UpgradeTransport 的 TLS 配置为 nil 时
			// SPDY 拨号会跳过证书校验，所以显式给一个默认配置。
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		rtConfig.UpgradeTransport = &http.Transport{DialContext: config.Dial, TLSClientConfig: tlsConfig}
	} else {
		rtConfig.TLS = tlsConfig
		rtConfig.Proxier = config.Proxy
	}
	roundTripper, err := streamspdy.NewRoundTripperWithConfig(rtConfig)
	if err != nil {
		return nil, nil, err
	}
	upgrader := &notifyingUpgrader{UpgradeRoundTripper: roundTripper, onConnected: onConnected, statusSeen: statusSeen}
	wrapper, err := rest.HTTPWrappersForConfig(config, roundTripper)
	if err != nil {
		return nil, nil, err
	}
	return wrapper, spdy.NewUpgraderForStreaming(upgrader), nil
}

// notifyingUpgrader 在 SPDY 升级成功后通知调用方（此后的失败都属于已建立的会话），
// 并盯着 error 流是否收到过命令的退出状态。
type notifyingUpgrader struct {
	streamhttp.UpgradeRoundTripper
	onConnected func()
	statusSeen  *atomic.Bool
}

func (u *notifyingUpgrader) NewConnection(resp *http.Response) (streamhttp.Connection, error) {
	conn, err := u.UpgradeRoundTripper.NewConnection(resp)
	if err != nil {
		return nil, err
	}
	if u.onConnected != nil {
		u.onConnected()
	}
	return &statusTrackingConn{Connection: conn, statusSeen: u.statusSeen}, nil
}

// statusTrackingConn 包住 error 流，记下它是否读到过数据。
type statusTrackingConn struct {
	streamhttp.Connection
	statusSeen *atomic.Bool
}

func (c *statusTrackingConn) CreateStream(headers http.Header) (streamhttp.Stream, error) {
	stream, err := c.Connection.CreateStream(headers)
	if err != nil || headers.Get(corev1.StreamType) != corev1.StreamTypeError {
		return stream, err
	}
	return &statusTrackingStream{Stream: stream, statusSeen: c.statusSeen}, nil
}

type statusTrackingStream struct {
	streamhttp.Stream
	statusSeen *atomic.Bool
}

func (s *statusTrackingStream) Read(p []byte) (int, error) {
	n, err := s.Stream.Read(p)
	if n > 0 {
		s.statusSeen.Store(true)
	}
	return n, err
}

// ExitCode 取出命令以非 0 退出时的退出码。
func ExitCode(err error) (int, bool) {
	var exitErr utilexec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitStatus(), true
	}
	return 0, false
}

// sizeQueue 把尺寸通道适配成 client-go 的 TerminalSizeQueue；会话结束（ctx 取消）时返回 nil 让它停止。
type sizeQueue struct {
	ctx   context.Context
	sizes <-chan TerminalSize
}

func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size, ok := <-q.sizes:
		if !ok {
			return nil
		}
		return &remotecommand.TerminalSize{Width: size.Width, Height: size.Height}
	case <-q.ctx.Done():
		return nil
	}
}
