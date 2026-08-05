// SSH Agent 产品连接路径的单一认证工厂：产品所有已保存资产连接路径（终端 / SFTP /
// 直连 / 跳板 / 代理链 / 连接池 / opsctl / AI / 隧道）都必须经过本文件把
// auth_type=agent 的资产解析成握手配置，不允许调用方自行构造 Agent 认证方法。
//
// 分层：internal/sshagent 提供传输 + 精确签名器选择 + MFA 挑战接口 + 主机密钥契约
// （任务 1-3）；本文件只负责把它们组装成可拨号的层配置，并保证：
//   - 来源在真正拨号时解析（Source 闭包），绝不提前打开 Agent 传输；
//   - 执行握手的组件拥有 Agent 传输：任何路径（失败 / 取消 / 成功）都在返回已建立
//     客户端前关闭；
//   - 主机密钥契约：显式 verifier 缺失即失败，绝不回退 InsecureIgnoreHostKey；
//   - 交互式调用方（桌面）经 MFA 适配器收结构化挑战；非交互新建连接需 MFA 时返回
//     ssh_agent_mfa_required，绝不显示隐藏提示。
package ssh_svc

import (
	"context"
	"net"
	"slices"

	"github.com/opskat/opskat/internal/pkg/sshtuning"
	"github.com/opskat/opskat/internal/sshagent"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// AgentConfig 描述一个 SSH 层的 Agent 认证配置。Source 是懒解析闭包：在真正拨号 /
// 握手发生时调用以读取已保存来源的端点，绝不提前打开 Agent 传输。
type AgentConfig struct {
	// Source 解析已保存来源 ID → 端点配置。由调用方（credential_resolver / app/ssh）
	// 提供，经 ssh_agent_svc 读取来源；ssh_svc 不导入来源仓库。
	Source func(ctx context.Context) (sshagent.Source, error)
	// Fingerprint 是资产保存的规范精确公钥指纹（大写 SHA256: 前缀）。
	Fingerprint string
	// MFA 是交互式挑战适配器；nil = 非交互（新建连接需 MFA 时返回 ssh_agent_mfa_required）。
	MFA sshagent.InteractiveCaller
}

// MakeAgentHostKeyCallback 构建 Agent 模式的主机密钥回调。verifyFn 缺失时返回 nil，
// 由 Agent 认证契约（sshagent.NewHostKeyContract）关闭失败——绝不回退到
// ssh.InsecureIgnoreHostKey。
func MakeAgentHostKeyCallback(host string, port int, verifyFn HostKeyVerifyFunc) ssh.HostKeyCallback {
	if verifyFn == nil {
		return nil
	}
	return MakeHostKeyCallback(host, port, verifyFn)
}

// connectCtx 返回连接配置携带的上下文；nil 时用 context.Background()。
func connectCtx(cfg ConnectConfig) context.Context {
	if cfg.Ctx != nil {
		return cfg.Ctx
	}
	return context.Background()
}

// agentAuthController 驱动一次 Agent 握手的认证序列：精确公钥是唯一第一因子，
// keyboard-interactive 至多一次、且只在公钥部分成功后提供；公钥被拒、重复部分成功、
// 第三因子或非预期方法序列全部关闭失败。语义与 internal/sshagent 的认证状态机
// 一致（任务 3），此处是产品层对已导出接口的重组（sshagent 传输包禁止改动）。
// 全部在 ssh.NewClientConn 的认证循环所在 goroutine 上执行，无需加锁。
type agentAuthController struct {
	ctx         context.Context
	publicKey   ssh.AuthMethod
	interactive sshagent.InteractiveCaller
	terminal    string
}

// choose 实现 ssh.ClientAuthCallback：返回下一个认证方法或以类型化错误终止握手。
func (m *agentAuthController) choose(ctx *ssh.ClientAuthContext) (ssh.AuthMethod, error) {
	partial := ctx.PartialSuccessMethods
	tried := ctx.TriedMethods

	switch {
	case len(partial) == 0:
		// 尚未部分成功：精确公钥是唯一第一因子。若已被拒，keyboard-interactive
		// 永远不会作为回退——停止提供方法。
		if slices.Contains(tried, "publickey") {
			return nil, nil
		}
		return m.publicKey, nil

	case len(partial) == 1 && partial[0] == "publickey":
		// 精确公钥部分成功：允许恰好一次 keyboard-interactive。失败的或重复的
		// 延续直接关闭握手，而不是继续协商更多因子。
		if slices.Contains(tried, "keyboard-interactive") {
			if m.terminal != "" {
				return nil, &sshagent.Error{Code: m.terminal, Message: "keyboard-interactive did not complete"}
			}
			return nil, &sshagent.Error{Code: sshagent.CodeMFAFailed, Message: "server rejected the keyboard-interactive answers"}
		}
		return m.keyboardInteractive(), nil

	default:
		return nil, &sshagent.Error{Code: sshagent.CodeAuthSequenceUnsupported, Message: "unexpected authentication method sequence"}
	}
}

// keyboardInteractive 构建唯一的 keyboard-interactive 方法。挑战适配器捕获 ctx，
// 取消的等待会终止握手；对零提示 / 多提示挑战绝不虚构答案，且绝不保留透传的答案。
func (m *agentAuthController) keyboardInteractive() ssh.AuthMethod {
	ctx := m.ctx
	return ssh.KeyboardInteractive(func(name, instruction string, prompts []string, echos []bool) ([]string, error) {
		if m.interactive == nil {
			m.terminal = sshagent.CodeMFARequired
			return nil, &sshagent.Error{Code: sshagent.CodeMFARequired, Message: "the server requires keyboard-interactive but no interactive caller is available"}
		}
		ch := sshagent.MFAChallenge{Name: name, Instruction: instruction, Prompts: prompts, Echo: echos}
		type challengeResult struct {
			answers []string
			err     error
		}
		resCh := make(chan challengeResult, 1)
		go func() {
			answers, err := m.interactive.SubmitChallenge(ctx, ch)
			resCh <- challengeResult{answers: answers, err: err}
		}()
		select {
		case res := <-resCh:
			if res.err != nil {
				if code, ok := sshagent.CodeOf(res.err); ok {
					m.terminal = code
					return nil, res.err
				}
				m.terminal = sshagent.CodeMFAFailed
				return nil, &sshagent.Error{Code: sshagent.CodeMFAFailed, Message: "challenge input or server verification failed"}
			}
			return res.answers, nil
		case <-ctx.Done():
			m.terminal = sshagent.CodeCancelled
			return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "MFA challenge was canceled"}
		}
	})
}

// AgentClientConn 在已建立的 raw 连接上完成一个 SSH 层的 Agent 认证握手。执行握手
// 的组件拥有 Agent 传输：来源在此时解析、传输在此时打开，且任何路径（来源解析失败、
// 选择失败、握手失败、取消、成功）都在返回已建立客户端前关闭传输。raw 连接由调用方
// 拥有：握手前失败时在此关闭，握手阶段由 ssh.NewClientConn 负责关闭。
func AgentClientConn(ctx context.Context, ac *AgentConfig, username string, hk ssh.HostKeyCallback, raw net.Conn, addr string) (*ssh.Client, error) {
	source, err := ac.Source(ctx)
	if err != nil {
		closeConnLog(ctx, raw)
		return nil, err
	}
	ag, err := sshagent.Open(ctx, source)
	if err != nil {
		closeConnLog(ctx, raw)
		return nil, err
	}
	// AuthMethod 在失败时自行关闭传输；成功时传输保持打开供握手使用。
	aa, err := ag.AuthMethod(ctx, ac.Fingerprint)
	if err != nil {
		closeConnLog(ctx, raw)
		return nil, err
	}

	// 主机密钥契约：显式 verifier 缺失（hk == nil）即关闭失败，绝不回退。
	contract := sshagent.NewHostKeyContract(hk)
	cc := &ssh.ClientConfig{
		User:            username,
		HostKeyCallback: contract.Callback(),
		Timeout:         sshtuning.Get().DialTimeoutOrDefault(),
	}
	cc.AuthCallback = (&agentAuthController{ctx: ctx, publicKey: aa.Method(), interactive: ac.MFA}).choose

	client, chans, reqs, err := ssh.NewClientConn(raw, addr, cc)
	// 握手结束：释放传输（成功与失败都关闭，避免任何已建立客户端持有 socket/pipe）。
	closeAgentLog(ctx, ag)
	if err != nil {
		if ctx.Err() != nil {
			return nil, &sshagent.Error{Code: sshagent.CodeCancelled, Message: "agent handshake was canceled"}
		}
		if _, ok := sshagent.CodeOf(err); ok {
			return nil, err
		}
		return nil, &sshagent.Error{Code: sshagent.CodePublicKeyFailed, Message: "the precise public key exchange did not complete"}
	}
	if !aa.Used() {
		_ = client.Close()
		return nil, &sshagent.Error{Code: sshagent.CodeAuthNotUsed, Message: "server accepted authentication without using the selected agent signer"}
	}
	return ssh.NewClient(client, chans, reqs), nil
}

func closeConnLog(ctx context.Context, conn net.Conn) {
	if err := conn.Close(); err != nil {
		logger.Ctx(ctx).Warn("close raw connection after agent handshake setup failure", zap.Error(err))
	}
}

func closeAgentLog(ctx context.Context, ag *sshagent.Agent) {
	if err := ag.Close(); err != nil {
		logger.Ctx(ctx).Warn("close ssh agent transport", zap.Error(err))
	}
}
