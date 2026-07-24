package helper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"

	"github.com/opskat/opskat/internal/ai/cmdline"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/executil"
	"github.com/opskat/opskat/internal/service/credential_svc"
)

// K8sCommandPlan 是解析、补全默认 context/namespace 之后的 kubectl 调用计划。
// EffectiveCommand 用于权限检查展示实际执行的命令；Args 用于实际执行。
type K8sCommandPlan struct {
	Args             []string
	EffectiveCommand string
}

// BuildK8sCommandPlan 解析一条 kubectl 命令并按需注入默认 context/namespace。
// 供权限检查（需要 EffectiveCommand）与 ExecK8sOnAsset（需要 Args）共用。
func BuildK8sCommandPlan(rawCommand string, cfg *asset_entity.K8sConfig) (*K8sCommandPlan, error) {
	args, err := parseK8sCommandArgs(rawCommand)
	if err != nil {
		return nil, err
	}

	finalArgs := make([]string, 0, len(args)+4)
	if cfg != nil {
		if cfg.Context != "" && !hasLongFlag(args, "--context") {
			finalArgs = append(finalArgs, "--context", cfg.Context)
		}
		if cfg.Namespace != "" && !hasNamespaceFlag(args) {
			finalArgs = append(finalArgs, "--namespace", cfg.Namespace)
		}
	}
	finalArgs = append(finalArgs, args...)

	return &K8sCommandPlan{
		Args:             finalArgs,
		EffectiveCommand: "kubectl " + strings.Join(finalArgs, " "),
	}, nil
}

// ExecK8sOnAsset 是不含权限检查的纯执行入口，供统一 exec 使用。
// 统一 exec 的 canonicalizeK8sCommand（internal/ai/execimpl）先算一次同样的 plan，两条路径
// 共用同一执行体。command 是原始 kubectl 命令；scope 对 k8s 无意义，忽略。
//
// 注意：cfg 与 plan 在这里会被重新计算一次——调用方（canonicalizeK8sCommand）为了权限检查
// 已经算过一次 EffectiveCommand，但 ExecFunc 的签名只接受 (ctx, asset, command,
// scope），没有位置传入已算好的 plan，所以这里必须以同一份 BuildK8sCommandPlan
// 重新推导 Args。两处调用的是同一个函数，不构成第二份实现。
func ExecK8sOnAsset(ctx context.Context, asset *asset_entity.Asset, command, _ string) (string, error) {
	cfg, err := asset.GetK8sConfig()
	if err != nil {
		return "", fmt.Errorf("get k8s config: %w", err)
	}
	if cfg.Kubeconfig == "" {
		return "", fmt.Errorf("no kubeconfig configured for this k8s asset")
	}

	plan, err := BuildK8sCommandPlan(command, cfg)
	if err != nil {
		return "", err
	}

	kubeconfig, err := credential_svc.Default().Decrypt(cfg.Kubeconfig)
	if err != nil {
		return "", fmt.Errorf("decrypt kubeconfig: %w", err)
	}

	if asset.SSHTunnelID != 0 {
		return executeK8sCommandOverSSH(ctx, asset.SSHTunnelID, kubeconfig, plan.Args)
	}
	return ExecuteK8sCommandLocal(ctx, kubeconfig, plan.Args)
}

func parseK8sCommandArgs(command string) ([]string, error) {
	args, err := cmdline.Words(command)
	if err != nil {
		return nil, fmt.Errorf("invalid kubectl command: %w", err)
	}

	if isKubectlProgram(args[0]) {
		args = args[1:]
	}
	// args[0] is the subcommand once any leading "kubectl"/"kubectl.exe" is
	// stripped. cmdline.Words now preserves a deliberately quoted empty word
	// (e.g. `''`) instead of dropping it, so a command consisting solely of
	// that empty word — or starting with it — no longer trips len(args) == 0;
	// without this check it would fall through with an empty-string
	// subcommand, decrypt the kubeconfig, and spawn kubectl with argv [""].
	if len(args) == 0 || args[0] == "" {
		return nil, fmt.Errorf("missing kubectl subcommand")
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--kubeconfig":
			return nil, fmt.Errorf("do not pass --kubeconfig to exec; the asset kubeconfig is used automatically")
		case strings.HasPrefix(arg, "--kubeconfig="):
			return nil, fmt.Errorf("do not pass --kubeconfig to exec; the asset kubeconfig is used automatically")
		}
	}

	return args, nil
}

func isKubectlProgram(program string) bool {
	normalized := strings.ReplaceAll(program, "\\", "/")
	if idx := strings.LastIndex(normalized, "/"); idx >= 0 {
		normalized = normalized[idx+1:]
	}
	return strings.EqualFold(normalized, "kubectl") || strings.EqualFold(normalized, "kubectl.exe")
}

func hasLongFlag(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func hasNamespaceFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-n" || arg == "--namespace" || strings.HasPrefix(arg, "--namespace=") {
			return true
		}
	}
	return false
}

// ExecuteK8sCommandLocal 在本机执行 kubectl（要求本机已安装 kubectl）。
func ExecuteK8sCommandLocal(ctx context.Context, kubeconfig string, args []string) (string, error) {
	kubectlPath, env, err := resolveLocalExecutable("kubectl")
	if err != nil {
		return "", fmt.Errorf("kubectl not found on local machine: %w", err)
	}

	kubeconfigPath, err := writeTempKubeconfig(kubeconfig)
	if err != nil {
		return "", err
	}
	defer removeTempFile(kubeconfigPath)

	cmd := exec.CommandContext(ctx, kubectlPath, args...) //nolint:gosec
	executil.HideConsoleWindow(cmd)
	cmd.Env = append(env, "KUBECONFIG="+kubeconfigPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("kubectl command failed: %s", strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("kubectl command failed: %w", err)
	}

	return formatCommandOutput(stdout.String(), stderr.String()), nil
}

func resolveLocalExecutable(name string) (string, []string, error) {
	env := os.Environ()
	path, err := exec.LookPath(name)
	if err == nil {
		return path, env, nil
	}
	if path, dir, ok := findExecutableInDirs(name, localExecutableFallbackDirs()); ok {
		return path, envWithPrependedPathDirs(env, []string{dir}), nil
	}
	return "", nil, err
}

func localExecutableFallbackDirs() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/opt/homebrew/bin",
			"/usr/local/bin",
			"/opt/local/bin",
		}
	case "linux":
		return []string{
			"/usr/local/bin",
			"/usr/bin",
			"/bin",
			"/snap/bin",
			"/var/lib/snapd/snap/bin",
		}
	default:
		return nil
	}
}

func findExecutableInDirs(name string, dirs []string) (string, string, bool) {
	for _, dir := range dirs {
		for _, candidateName := range executableCandidateNames(name) {
			path := filepath.Join(dir, candidateName)
			info, err := os.Stat(path)
			if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return path, dir, true
			}
		}
	}
	return "", "", false
}

func executableCandidateNames(name string) []string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return []string{name + ".exe", name}
	}
	return []string{name}
}

func envWithPrependedPathDirs(env []string, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}

	const pathPrefix = "PATH="
	currentPath := ""
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if strings.HasPrefix(entry, pathPrefix) {
			currentPath = strings.TrimPrefix(entry, pathPrefix)
			continue
		}
		out = append(out, entry)
	}

	pathParts := make([]string, 0, len(dirs)+1)
	seen := make(map[string]struct{}, len(dirs)+8)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		pathParts = append(pathParts, dir)
	}
	for _, dir := range filepath.SplitList(currentPath) {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		pathParts = append(pathParts, dir)
	}

	return append(out, pathPrefix+strings.Join(pathParts, string(os.PathListSeparator)))
}

func executeK8sCommandOverSSH(ctx context.Context, sshAssetID int64, kubeconfig string, args []string) (string, error) {
	if strings.TrimSpace(kubeconfig) == "" {
		return "", fmt.Errorf("no kubeconfig configured for this k8s asset")
	}

	kubectlCmd := buildQuotedKubectlCommand(args)
	script := fmt.Sprintf(`tmp="${TMPDIR:-/tmp}/opskat-kubeconfig-$$"; umask 077; cat > "$tmp"; KUBECONFIG="$tmp" %s; status=$?; rm -f "$tmp"; exit $status`, kubectlCmd)
	return executeSSHCommandWithStdin(ctx, sshAssetID, "sh -lc "+quotePosixArg(script), strings.NewReader(kubeconfig))
}

func buildQuotedKubectlCommand(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "kubectl")
	for _, arg := range args {
		parts = append(parts, quotePosixArg(arg))
	}
	return strings.Join(parts, " ")
}

func quotePosixArg(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func writeTempKubeconfig(kubeconfig string) (string, error) {
	file, err := os.CreateTemp("", "opskat-kubeconfig-*")
	if err != nil {
		return "", fmt.Errorf("create temp kubeconfig: %w", err)
	}
	path := file.Name()
	if closeErr := file.Close(); closeErr != nil {
		removeTempFile(path)
		return "", fmt.Errorf("close temp kubeconfig: %w", closeErr)
	}
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		removeTempFile(path)
		return "", fmt.Errorf("write temp kubeconfig: %w", err)
	}
	return path, nil
}

func removeTempFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Default().Warn("remove temp kubeconfig", zap.String("path", path), zap.Error(err))
	}
}

func formatCommandOutput(stdout, stderr string) string {
	output := stdout
	if strings.TrimSpace(stderr) != "" {
		if output != "" && !strings.HasSuffix(output, "\n") {
			output += "\n"
		}
		output += "STDERR:\n" + stderr
	}
	return output
}

func executeSSHCommandWithStdin(ctx context.Context, assetID int64, command string, stdin io.Reader) (string, error) {
	client, cleanup, err := DialAssetSSH(ctx, assetID)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return runSSHCommandWithStdin(ctx, client, command, stdin)
}

func runSSHCommandWithStdin(ctx context.Context, client *ssh.Client, command string, stdin io.Reader) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer func() {
		if err := session.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH session", zap.Error(err))
		}
	}()

	if stdin != nil {
		session.Stdin = stdin
	}

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
				return "", fmt.Errorf("command failed: %s", strings.TrimSpace(stderr.String()))
			}
			return "", fmt.Errorf("command failed: %w", err)
		}
	case <-ctx.Done():
		if err := session.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH session on cancel", zap.Error(err))
		}
		if err := client.Close(); err != nil && !IsExpectedCloseErr(err) {
			logger.Default().Warn("close SSH client on cancel", zap.Error(err))
		}
		return "", ctx.Err()
	}

	return formatCommandOutput(stdout.String(), stderr.String()), nil
}
