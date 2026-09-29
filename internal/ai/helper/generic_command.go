package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/pkg/authtmpl"
	"github.com/opskat/opskat/internal/pkg/executil"
	"github.com/opskat/opskat/internal/pkg/shellutil"
)

// 通用资产的本地命令执行方式（docs/specs/2026-09-28-generic-asset.md「本地命令」，Design
// decision 6）：
//
//   - 类型上配了命令模板：先按空白（`{{ }}` 内部除外）把模板源码拆成 token，逐个渲染
//     （splitTemplateArgv + renderCommandArgv），再把 exec 传入的参数按字面追加在后面
//     （不渲染），不经过 shell 直接用 os/exec 启动。拆分发生在渲染之前，字段值里的空格
//     不会拆出新参数。
//   - 类型上没配模板：exec 传入的就是整条 shell 命令，用系统默认 shell
//     （internal/pkg/shellutil）执行，与本地终端资产选的是同一个 shell。
//
// 两种情形都：在当前进程环境基础上追加渲染后的环境变量绑定；工作目录是调用方的当前
// 工作目录（os/exec 的 Cmd.Dir 留空即是）；stdin/stdout/stderr 透传（Stream）或捕获
// （Exec，供 AI 与审计使用）；退出码原样返回；程序不存在或无法启动时返回 error（由
// StreamExecFunc / ExecFunc 的契约转成 opsctl 的退出码 1），已完成、只是退出码非零的
// 运行不是错误。

func init() {
	RegisterGenericMode(custom_type_entity.ExecModeCommand, GenericMode{
		Canonicalize: canonicalizeCommandMode,
		Describe:     describeCommandMode,
		Exec:         execCommandModeForAI,
		Stream:       streamCommandMode,
	})
}

// canonicalizeCommandMode 是命令方式的 permission.CanonicalizeFunc：不管类型上是否配了
// 命令模板，策略匹配对象都是"exec 传入的内容"本身——有模板时是参数串，没模板时是整条
// shell 命令，两者在 spec 里都写作"原样用空格连接"。command 可能是 opsctl 按参数边界加
// 引号拼成的串，也可能是 AI 按同一约定写的字面量：cmdline.Words 把它还原成词（同时是
// argv 拆分点），再用普通空格重新拼接——这一步只去掉"为了在参数传输层保住边界"而加的
// 引号，不改变词本身的内容，因此一段用引号包住的完整 shell 命令（内部的管道、&& 等
// 元字符）在还原后原样保留，供无模板情形交给真正的 shell 解释。
func canonicalizeCommandMode(command string) (string, error) {
	argv, err := cmdline.Words(command)
	if err != nil {
		return "", err
	}
	return strings.Join(argv, " "), nil
}

// describeCommandMode 是命令方式的 permission.ApprovalDetailFunc：只展示"会启动的是什么"
// 这一类信息，绝不展示任何注入值。
//
// 没有命令模板时展示会用哪个 shell（这本身不是注入值，是本机配置）。有命令模板时只展示
// 模板**源码**里第一个 token——如果它不含 `{{`，就是一个静态程序名，直接给字面值；
// 一旦它含 `{{`（程序本身是按字段渲染出来的，字段可能是密钥），一律不渲染、只说明
// "模板化"，因为展示渲染结果就是展示注入值。
func describeCommandMode(_ context.Context, t *GenericTarget, _ string) (string, error) {
	ct := t.Type
	if ct.Command.Template == "" {
		return "Local command via " + shellutil.DefaultShell(), nil
	}
	tokens := splitTemplateArgv(ct.Command.Template)
	if len(tokens) == 0 || strings.Contains(tokens[0], "{{") {
		return "Local command (templated program)", nil
	}
	return "Local command: " + tokens[0], nil
}

// execCommandModeForAI 是命令方式的 AI exec 执行入口：捕获 stdout/stderr（AI 没有本地
// 终端可以透传），返回值同时是模型看到的内容与审计 result（内容含 exec 传入的输出和
// 退出码，绝不含渲染出的环境变量值或模板渲染出的密钥——那些只进子进程的环境/argv，
// 不会被我们自己写回文本）。
func execCommandModeForAI(ctx context.Context, t *GenericTarget, command string) (string, error) {
	argvWords, err := cmdline.Words(command)
	if err != nil {
		return "", err
	}
	inv, err := commandInvocationFor(t.Type, t.Values, argvWords)
	if err != nil {
		return "", err
	}
	var stdout, stderr bytes.Buffer
	exitCode, err := runCommandInvocation(ctx, inv, nil, &stdout, &stderr)
	if err != nil {
		return "", err
	}
	return formatCommandModeOutputForAI(exitCode, formatCommandOutput(stdout.String(), stderr.String())), nil
}

func formatCommandModeOutputForAI(exitCode int, output string) string {
	footer := fmt.Sprintf("(exit code %d)", exitCode)
	if output == "" {
		return footer
	}
	return strings.TrimRight(output, "\n") + "\n" + footer
}

// streamCommandMode 是命令方式的 opsctl 流式执行入口：argv 是 opsctl 保留边界的参数
// （有模板时是要追加的参数；没有模板时是整条命令的各个 argv 元素，用空格重新拼接还原成
// 一条 shell 命令——两者与 canonicalizeCommandMode 用的是同一条还原规则）。stdin/stdout/
// stderr 直接透传给子进程。
func streamCommandMode(ctx context.Context, t *GenericTarget, argv []string, stdio permission.Stdio) (permission.StreamResult, error) {
	inv, err := commandInvocationFor(t.Type, t.Values, argv)
	if err != nil {
		return permission.StreamResult{}, err
	}
	exitCode, err := runCommandInvocation(ctx, inv, stdio.Stdin, stdio.Stdout, stdio.Stderr)
	if err != nil {
		return permission.StreamResult{}, err
	}
	return permission.StreamResult{ExitCode: exitCode, AuditResult: fmt.Sprintf("exit %d", exitCode)}, nil
}

// commandInvocation 是即将启动的子进程：完整 argv 与完整环境变量列表（当前进程环境 +
// 渲染后的绑定）。
type commandInvocation struct {
	argv []string
	env  []string
}

// commandInvocationFor 按类型是否配了命令模板选择两种构造方式之一。execArgv 是 exec
// 传入、按字面处理的参数：有模板时追加在渲染后的 argv 末尾；没有模板时重新拼成整条
// shell 命令交给系统默认 shell。ct.Command 非空由 custom_type_entity.Validate 在保存时
// 保证，这里信任这条约束。
func commandInvocationFor(ct *custom_type_entity.CustomType, values map[string]string, execArgv []string) (*commandInvocation, error) {
	if ct.Command.Template != "" {
		return buildTemplateInvocation(ct, values, execArgv)
	}
	return buildShellInvocation(ct, values, strings.Join(execArgv, " "))
}

func buildTemplateInvocation(ct *custom_type_entity.CustomType, values map[string]string, execArgv []string) (*commandInvocation, error) {
	rc := authtmpl.NewRenderContext(values, authtmpl.WithClock(time.Now))
	base, err := renderCommandArgv(ct, rc)
	if err != nil {
		return nil, err
	}
	env, err := renderCommandEnv(ct, rc)
	if err != nil {
		return nil, err
	}
	argv := append(base, execArgv...) //nolint:gocritic // base is freshly allocated by renderCommandArgv, safe to grow in place
	if len(argv) == 0 {
		return nil, fmt.Errorf("custom type %q command template renders to an empty program", ct.Slug)
	}
	return &commandInvocation{argv: argv, env: append(os.Environ(), env...)}, nil
}

func buildShellInvocation(ct *custom_type_entity.CustomType, values map[string]string, rawCommand string) (*commandInvocation, error) {
	rc := authtmpl.NewRenderContext(values, authtmpl.WithClock(time.Now))
	env, err := renderCommandEnv(ct, rc)
	if err != nil {
		return nil, err
	}
	return &commandInvocation{argv: shellCommandArgv(rawCommand), env: append(os.Environ(), env...)}, nil
}

// renderCommandArgv 渲染命令模板：splitTemplateArgv 先按空白拆出 token（`{{ }}` 内部的
// 空白不算分隔符），再逐个 authtmpl.Parse + Render——分两步是因为渲染结果里的空格不该
// 被当成新的分隔符，只有模板源码里的空白才该被当成分隔符（spec「本地命令」）。
func renderCommandArgv(ct *custom_type_entity.CustomType, rc *authtmpl.RenderContext) ([]string, error) {
	fields := ct.FieldNames()
	tokens := splitTemplateArgv(ct.Command.Template)
	argv := make([]string, 0, len(tokens))
	for i, tok := range tokens {
		tmpl, err := authtmpl.Parse(tok, authtmpl.ParseOptions{Fields: fields})
		if err != nil {
			return nil, fmt.Errorf("custom type %q command template token %d: %w", ct.Slug, i, err)
		}
		rendered, err := tmpl.Render(rc)
		if err != nil {
			return nil, fmt.Errorf("render command template token %d of custom type %q: %w", i, ct.Slug, err)
		}
		argv = append(argv, rendered)
	}
	return argv, nil
}

// renderCommandEnv 渲染环境变量绑定：每条绑定的值是一个完整模板（不做 argv 式拆分，
// 一条绑定只产出一个环境变量值）。ct.Command 非空由 custom_type_entity.Validate 在保存
// 时保证（命令执行方式缺少配置会被拒绝），这里信任这条约束，不重复判空。
func renderCommandEnv(ct *custom_type_entity.CustomType, rc *authtmpl.RenderContext) ([]string, error) {
	if len(ct.Command.Env) == 0 {
		return nil, nil
	}
	fields := ct.FieldNames()
	env := make([]string, 0, len(ct.Command.Env))
	for i, e := range ct.Command.Env {
		tmpl, err := authtmpl.Parse(e.Value, authtmpl.ParseOptions{Fields: fields})
		if err != nil {
			return nil, fmt.Errorf("custom type %q env binding %d: %w", ct.Slug, i, err)
		}
		rendered, err := tmpl.Render(rc)
		if err != nil {
			return nil, fmt.Errorf("render env binding %d of custom type %q: %w", i, ct.Slug, err)
		}
		env = append(env, e.Name+"="+rendered)
	}
	return env, nil
}

// runCommandInvocation 启动子进程并等待结束：已启动、跑完的进程即便退出码非零也不是
// error（错误留给调用方按 exitCode 处理），只有没能跑起来（程序不存在、权限不足等）才
// 是 error——StreamExecFunc / ExecFunc 的契约要求"请求没有完成"用 error 表达。
func runCommandInvocation(ctx context.Context, inv *commandInvocation, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, inv.argv[0], inv.argv[1:]...) //nolint:gosec // argv comes from the custom type's own rendered template/shell choice plus literally-appended exec args; never shell-interpreted here
	executil.HideConsoleWindow(cmd)
	cmd.Env = inv.env
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}

// splitTemplateArgv 按空白把命令模板源码拆成 token：`{{` 到匹配的 `}}` 之间的内容（哪怕
// 含空白，例如字符串拼接 `{{ name + " " + env }}`）算一个不可再分的整体，其余部分按
// 任意长度的空白分隔。拆分在渲染之前发生，所以字段值里渲染出的空格永远不会拆出新参数——
// 只有模板源码里、`{{ }}` 之外的空白才是分隔符。
func splitTemplateArgv(src string) []string {
	var tokens []string
	var cur strings.Builder
	inExpr := false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(src)
	for i := 0; i < len(runes); {
		if !inExpr && i+1 < len(runes) && runes[i] == '{' && runes[i+1] == '{' {
			inExpr = true
			cur.WriteString("{{")
			i += 2
			continue
		}
		if inExpr && i+1 < len(runes) && runes[i] == '}' && runes[i+1] == '}' {
			inExpr = false
			cur.WriteString("}}")
			i += 2
			continue
		}
		c := runes[i]
		if !inExpr && (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
			flush()
			i++
			continue
		}
		cur.WriteRune(c)
		i++
	}
	flush()
	return tokens
}

// shellCommandArgv 用系统默认 shell 执行一整条命令；实际的 goos/shell 由
// buildShellCommandArgv 消费，拆成纯函数是为了能在任意平台上测试 Windows 分支。
func shellCommandArgv(command string) []string {
	return buildShellCommandArgv(runtime.GOOS, shellutil.DefaultShell(), command)
}

// buildShellCommandArgv 按 shell 的调用约定选择"执行一整条命令"的 flag：POSIX shell
// （包括 macOS/Linux 上 shellutil.DefaultShell() 可能选出的任何 shell）用 -c；Windows 上
// PowerShell / pwsh 用 -Command，其余（cmd.exe）用 /C。
//
// 这是一个不看 GOOS 编译标签的纯函数（真实调用点 shellCommandArgv 才是那道缝），只为了
// 能在任意平台上测试 windows 分支——因此不用 filepath.Base（它按*编译平台*的分隔符规则
// 解析路径：在非 Windows 构建上，`C:\a\b.exe` 里的反斜杠不是分隔符，Base 会把整条路径
// 当成文件名），而是自己找最后一个 `/` 或 `\` 之后的部分。
func buildShellCommandArgv(goos, shell, command string) []string {
	if goos != "windows" {
		return []string{shell, "-c", command}
	}
	base := strings.ToLower(windowsBaseName(shell))
	if strings.HasPrefix(base, "powershell") || strings.HasPrefix(base, "pwsh") {
		return []string{shell, "-Command", command}
	}
	return []string{shell, "/C", command}
}

// windowsBaseName 返回 Windows 路径的最后一段，同时认 `/` 和 `\` 作分隔符。
func windowsBaseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}
