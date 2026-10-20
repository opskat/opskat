package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/opskat/opskat/internal/ai/helper"
	"github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/service/credential_resolver"
	"github.com/opskat/opskat/internal/sshagent"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/term"
)

// MFA 应答来源（spec 2026-09-22-opsctl-mfa）：新建 SSH 连接遇到 keyboard-interactive
// 挑战时依次尝试 --mfa-code / OPSKAT_MFA_CODE → 可交互终端提示 → 运行中的桌面端
// 对话框（approval.sock）→ 结构化拒绝（退出码 3 + NEEDS MFA）。与审批人选择同一
// 顺序。答案只用于当次握手，绝不记录。
const (
	needsMFAMarker = "NEEDS MFA"
	mfaCodeEnv     = "OPSKAT_MFA_CODE"
)

// resolveMFACode 取全局 --mfa-code，未给时回落到环境变量（参数优先）。
func resolveMFACode(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return os.Getenv(mfaCodeEnv)
}

type mfaCodeKeyType struct{}

// withMFACode 由 CLI 边界把解析好的验证码挂到 ctx，供需要拨号的命令取用。
func withMFACode(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, mfaCodeKeyType{}, code)
}

// withMFA 为 ssh/exec/cp/batch 对 SSH 资产的拨号接上应答来源：目标资产及其连接路径上
// 的 SSH 跳板（credential_resolver.WithMFA，只经 DialAssetSSH 生效——cp 的对象存储端点等
// 其它类型经隧道拨号不应答跳板）。可交互判据与审批一致（stdin 与 stderr 双 TTY）。
func withMFA(ctx context.Context) context.Context {
	ctx, src := withMFASources(ctx)
	return credential_resolver.WithMFA(ctx, src.newCaller)
}

// withConnectionPathMFA 为扩展资产的执行接上同一套应答来源：扩展经资产连接路径拨号，
// 路径上要 MFA 的 SSH 跳板由它应答（credential_resolver.WithConnectionPathMFA）。
func withConnectionPathMFA(ctx context.Context) context.Context {
	ctx, src := withMFASources(ctx)
	return credential_resolver.WithConnectionPathMFA(ctx, src.newCaller)
}

// withMFASources 组出一次执行的应答来源，并把记录「没有应答来源」拒绝的 mfaRefusals
// 挂到 ctx（mfaRefusalOf 据此认回拒绝）。
func withMFASources(ctx context.Context) (context.Context, mfaSources) {
	code, _ := ctx.Value(mfaCodeKeyType{}).(string)
	dataDir := bootstrap.ResolvedDataDir()
	token, err := bootstrap.ReadAuthToken(dataDir)
	if err != nil {
		logger.Default().Warn("read auth token", zap.Error(err))
	}
	src := mfaSources{
		code:        code,
		interactive: isInteractive(stdinIsTerminal(), stderrIsTerminal()),
		terminal:    func() (*os.File, io.Writer) { return os.Stdin, os.Stderr },
		desktop:     desktopMFASender(approval.SocketPath(dataDir), token),
		refusals:    &mfaRefusals{},
	}
	return context.WithValue(ctx, mfaRefusalsKeyType{}, src.refusals), src
}

type mfaRefusalsKeyType struct{}

// mfaRefusals 记下一次 withMFA / withConnectionPathMFA 的应答方因「没有应答来源」给出的拒绝。
type mfaRefusals struct {
	last atomic.Pointer[noMFASourceError]
}

// mfaRefusalOf 让 ctx 的应答方在本次调用里给出过的「没有应答来源」拒绝仍决定 err 的
// 上报方式（NEEDS MFA）。扩展工具经 WASM 客体拨号：宿主的类型化错误交给客体后只剩
// 文本，回到 opsctl 时 needsMFA 已认不出它。err 为 nil、已可识别，或应答方没有拒绝过
// 时原样返回。
func mfaRefusalOf(ctx context.Context, err error) error {
	if err == nil || needsMFA(err) {
		return err
	}
	refusals, _ := ctx.Value(mfaRefusalsKeyType{}).(*mfaRefusals)
	if refusals == nil {
		return err
	}
	if refusal := refusals.last.Load(); refusal != nil {
		return &relayedMFARefusal{err: err, refusal: refusal}
	}
	return err
}

// relayMFARefusals 返回只含 toolName 的 handler 表，其失败经 mfaRefusalOf 上报。
// handlers 里没有 toolName 时原样返回，由调用方照常报未知工具。
func relayMFARefusals(handlers map[string]tool.ToolHandlerFunc, toolName string) map[string]tool.ToolHandlerFunc {
	handler, found := handlers[toolName]
	if !found {
		return handlers
	}
	return map[string]tool.ToolHandlerFunc{toolName: func(ctx context.Context, params map[string]any) (string, error) {
		result, err := handler(ctx, params)
		return result, mfaRefusalOf(ctx, err)
	}}
}

// relayedMFARefusal 是带上应答方拒绝的 handler 错误：文本仍是 handler 的原文。
type relayedMFARefusal struct {
	err     error
	refusal *noMFASourceError
}

func (e *relayedMFARefusal) Error() string   { return e.err.Error() }
func (e *relayedMFARefusal) Unwrap() []error { return []error{e.err, e.refusal} }

// mfaSources 是一个 opsctl 进程的应答来源配置；newCaller 为每条新建连接产出一个
// 持有「本连接内」状态的应答方。
type mfaSources struct {
	code        string
	interactive bool
	terminal    func() (*os.File, io.Writer)
	desktop     func(ctx context.Context, req approval.ApprovalRequest) (approval.ApprovalResponse, error)
	// refusals 非 nil 时记下「没有应答来源」的拒绝（见 mfaRefusalOf）。
	refusals *mfaRefusals
}

func (s mfaSources) newCaller(assetID int64) sshagent.InteractiveCaller {
	return &mfaCaller{src: s, assetID: assetID}
}

// desktopMFASender 经 approval.sock 请运行中的桌面端弹出 MFA 对话框代答。桌面端
// 不可达、或请求途中退出，都以错误返回，由调用方落到 NEEDS MFA。
func desktopMFASender(sockPath, token string) func(context.Context, approval.ApprovalRequest) (approval.ApprovalResponse, error) {
	return func(ctx context.Context, req approval.ApprovalRequest) (approval.ApprovalResponse, error) {
		if err := dialApprovalSocket(sockPath); err != nil {
			return approval.ApprovalResponse{}, err
		}
		if asset, err := asset_repo.Asset().Find(ctx, req.AssetID); err == nil {
			req.AssetName = asset.Name
		} else {
			logger.Default().Warn("resolve asset name for desktop MFA dialog", zap.Int64("assetID", req.AssetID), zap.Error(err))
		}
		return approval.RequestApprovalWithToken(sockPath, token, req)
	}
}

// terminalMFAMu 串行化本进程内的终端 MFA 提示：stdin/stderr 是进程级的单一资源。
var terminalMFAMu sync.Mutex

type mfaCaller struct {
	src     mfaSources
	assetID int64
	// codeUsed：验证码在本连接内已作答过一轮，不再复用。
	codeUsed bool
}

// SubmitChallenge 实现 sshagent.InteractiveCaller。给了验证码时只用它（被拒即失败，
// 不改走其它来源）；否则可交互走终端，不可交互时请运行中的桌面端弹窗；都不行返回
// ssh_agent_mfa_required，由命令层映射成 NEEDS MFA。
func (c *mfaCaller) SubmitChallenge(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	if c.src.code != "" {
		if c.codeUsed || len(ch.Prompts) != 1 {
			return nil, &sshagent.Error{Code: sshagent.CodeMFAFailed, Message: "--mfa-code answers only a single one-prompt challenge; " +
				"complete this server's challenge in an interactive terminal or the desktop app"}
		}
		c.codeUsed = true
		return []string{c.src.code}, nil
	}
	if c.src.interactive {
		// 终端提示期间把 Ctrl-C 转成取消：放弃等待并恢复终端回显，而不是带着
		// 关闭的回显直接退出进程。
		sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		// 终端只有一个：batch 里多个资产并发拨号时，挑战逐个呈现、逐个读答案。
		terminalMFAMu.Lock()
		defer terminalMFAMu.Unlock()
		in, out := c.src.terminal()
		answers, err := (&terminalMFACaller{out: out, in: in}).SubmitChallenge(sigCtx, ch)
		if errors.Is(err, context.Canceled) {
			return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "MFA prompt was canceled"}
		}
		return answers, err
	}
	if c.src.desktop != nil {
		resp, err := c.src.desktop(ctx, approval.ApprovalRequest{
			Type:    "mfa",
			AssetID: c.assetID,
			MFA:     &approval.MFAChallenge{Name: ch.Name, Instruction: ch.Instruction, Prompts: ch.Prompts, Echo: ch.Echo},
		})
		switch {
		case err != nil:
			logger.Default().Warn("desktop MFA unavailable", zap.Int64("assetID", c.assetID), zap.Error(err))
		case resp.Approved:
			return resp.MFAAnswers, nil
		case resp.Reason == approval.MFACanceledReason:
			return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "MFA was canceled in the desktop app"}
		default:
			return nil, fmt.Errorf("desktop app refused the MFA request: %s", resp.Reason)
		}
	}
	refusal := &noMFASourceError{typed: &sshagent.Error{Code: sshagent.CodeMFARequired, Message: "the server requires MFA but no answer source is available"}}
	if c.src.refusals != nil {
		c.src.refusals.last.Store(refusal)
	}
	return nil, refusal
}

// noMFASourceError 是 opsctl 应答方在「没有参数、不可交互、桌面端也未运行」时给出的
// 拒绝。仅它映射成 NEEDS MFA：未接应答方的拨号（如数据库经 SSH 隧道）同样报
// ssh_agent_mfa_required，但 --mfa-code 对它无效，不能指引调用方去要码。
type noMFASourceError struct{ typed *sshagent.Error }

func (e *noMFASourceError) Error() string { return e.typed.Error() }
func (e *noMFASourceError) Unwrap() error { return e.typed }

// needsMFA 判断 err 是否是 opsctl 应答方的「无应答来源」拒绝。
func needsMFA(err error) bool {
	var e *noMFASourceError
	return errors.As(err, &e)
}

// writeRemoteFailure 上报远程操作的错误：需要 MFA 却没有应答来源时是结构化拒绝
// （stderr 首行 NEEDS MFA、退出码 3）；redis 集群缺节点（helper.RedisNodeRequiredError）
// 是普通错误但要追加 opsctl 侧的 --scope 用法（该错误类型本身是 AI/opsctl/桌面控制台
// 共用的 helper 级文案，只列主节点，不认识 CLI 的 --scope 标志）；其余错误照旧
// "Error: " + 退出码 1。
func writeRemoteFailure(w io.Writer, err error) int {
	if needsMFA(err) {
		err = &structuredRefusal{marker: needsMFAMarker, body: "The server requires multi-factor authentication (MFA) and opsctl has no way to answer it here.\n" +
			"Retry with the code: set " + mfaCodeEnv + "=<code> (preferred; --mfa-code <code> also works but is visible in shell history and process lists),\n" +
			"or open the OpsKat desktop app, or run the command in an interactive terminal."}
	}
	var nodeErr *helper.RedisNodeRequiredError
	if errors.As(err, &nodeErr) {
		err = fmt.Errorf("%w\nUsage: --scope <host:port>", err)
	}
	return writeApprovalFailure(w, err)
}

// terminalMFACaller 是 opsctl 可交互时的 MFA 挑战适配器（挑战帧约定）：把
// 服务器结构化挑战（名称/说明/逐条提示与回显标记）按原样呈现到 out，从终端按
// 服务器顺序读取答案并返回。非回显提示用 ReadPassword 隐藏输入；答案只存在于当前
// 请求，返回后立即丢弃。取消（ctx.Done）时不读取并立即返回，不残留等待。
type terminalMFACaller struct {
	out io.Writer // 挑战呈现出口（stderr）
	in  *os.File  // 答案读取入口（stdin）
}

// SubmitChallenge 实现 sshagent.InteractiveCaller。
func (c *terminalMFACaller) SubmitChallenge(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	type answerResult struct {
		answers []string
		err     error
	}
	// 隐藏输入期间终端处于关闭回显状态；取消时读协程仍阻塞在 ReadPassword 里、
	// 不会自行恢复，所以先存下当前状态，取消时由这里恢复。
	var saved *term.State
	if fd := int(c.in.Fd()); term.IsTerminal(fd) {
		st, err := term.GetState(fd)
		if err != nil {
			logger.Default().Warn("save terminal state before MFA prompt", zap.Error(err))
		}
		saved = st
	}
	resCh := make(chan answerResult, 1)
	go func() {
		answers, err := c.presentAndRead(ctx, ch)
		resCh <- answerResult{answers: answers, err: err}
	}()
	select {
	case res := <-resCh:
		return res.answers, res.err
	case <-ctx.Done():
		if saved != nil {
			if err := term.Restore(int(c.in.Fd()), saved); err != nil {
				logger.Default().Warn("restore terminal state after canceled MFA prompt", zap.Error(err))
			}
		}
		return nil, ctx.Err()
	}
}

// presentAndRead 呈现挑战并逐条读取答案。
func (c *terminalMFACaller) presentAndRead(ctx context.Context, ch sshagent.MFAChallenge) ([]string, error) {
	// 挑战呈现是面向终端用户的输出，写入失败不值得中断认证——与包内
	// fmt.Fprintf(os.Stderr, ...) 同一语义（errcheck 默认豁免 os.Stderr 字面量，
	// 这里经 io.Writer 字段所以需要显式豁免）。
	fmt.Fprintln(c.out)                       //nolint:errcheck // 终端呈现尽力而为
	fmt.Fprintln(c.out, "SSH MFA challenge:") //nolint:errcheck // 终端呈现尽力而为
	if ch.Name != "" {
		fmt.Fprintf(c.out, "  name: %s\n", ch.Name) //nolint:errcheck // 终端呈现尽力而为
	}
	if ch.Instruction != "" {
		fmt.Fprintf(c.out, "  instruction: %s\n", ch.Instruction) //nolint:errcheck // 终端呈现尽力而为
	}
	answers := make([]string, len(ch.Prompts))
	for i, prompt := range ch.Prompts {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		echo := true
		if ch.Echo != nil && i < len(ch.Echo) {
			echo = ch.Echo[i]
		}
		fmt.Fprintf(c.out, "  %s", prompt) //nolint:errcheck // 终端呈现尽力而为；提示原文自带分隔符（如 "OTP: "）
		val, err := readMFAAnswer(c.in, echo)
		fmt.Fprintln(c.out) //nolint:errcheck // 终端呈现尽力而为
		if err != nil {
			return nil, fmt.Errorf("read MFA answer: %w", err)
		}
		answers[i] = val
	}
	return answers, nil
}

// readMFAAnswer 从终端读取一行答案；echo=false 时隐藏输入。term.ReadPassword 在
// 非终端 fd 上会因 ioctl 失败而报错，故先用 IsTerminal 分流：非终端（测试/管道）
// 直接按字节读行。两条读取路径都按字节进行，避免缓冲器提前吞掉同一轮挑战的后续
// 答案。
func readMFAAnswer(in *os.File, echo bool) (string, error) {
	if !echo && term.IsTerminal(int(in.Fd())) {
		b, err := term.ReadPassword(int(in.Fd()))
		return string(b), err
	}
	var buf []byte
	tmp := make([]byte, 1)
	for {
		n, err := in.Read(tmp)
		if n > 0 {
			if tmp[0] == '\n' {
				break
			}
			buf = append(buf, tmp[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return "", err
		}
	}
	return strings.TrimRight(string(buf), "\r"), nil
}
