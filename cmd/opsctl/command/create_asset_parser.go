package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

type assetCreateRequest struct {
	asset  *asset_entity.Asset
	config map[string]any
}

type assetCreateParserDeps struct {
	stderr   io.Writer
	readFile func(string) ([]byte, error)
	// promptSecret 从终端无回显读取一行密码；nil 表示当前环境不可交互，裸写
	// --password 时应给出 NEEDS TTY 结构化拒绝而不是读取任何输入。
	promptSecret   func(prompt string) (string, error)
	resolveAssetID func(context.Context, string) (int64, error)
}

func parseAssetCreate(ctx context.Context, args []string, deps assetCreateParserDeps) (*assetCreateRequest, error) {
	if retired := retiredPasswordStdinFlag(args); retired != "" {
		return nil, fmt.Errorf("%s has been removed: use a bare --password to type it interactively in a terminal, or --password=<value>", retired)
	}
	args, promptPassword, bareFollowedBy := splitPasswordPrompt(args)
	fs := flag.NewFlagSet("create asset", flag.ContinueOnError)
	fs.SetOutput(deps.stderr)
	assetType := fs.String("type", "ssh", "Registered asset type")
	name := fs.String("name", "", "Display name for the asset (required)")
	configJSON := fs.String("config", "", "Generic asset config JSON object")
	configFile := fs.String("config-file", "", "Path to generic asset config JSON object")
	host := fs.String("host", "", "Hostname or IP address")
	port := fs.Int("port", 0, "Port number (type-owned default when omitted)")
	username := fs.String("username", "", "Login username")
	authType := fs.String("auth-type", "", "SSH auth method: password, key, or agent")
	driver := fs.String("driver", "", "Database driver")
	database := fs.String("database", "", "Default database name")
	readOnly := fs.Bool("read-only", false, "Enable read-only mode")
	sshAsset := fs.String("ssh-asset", "", "SSH asset name/ID for tunnel connection")
	kubeconfig := fs.String("kubeconfig", "", "Kubeconfig YAML content")
	kubeconfigFile := fs.String("kubeconfig-file", "", "Path to kubeconfig YAML file")
	k8sNamespace := fs.String("namespace", "", "Default Kubernetes namespace")
	k8sContext := fs.String("context", "", "Kubeconfig context name")
	groupID := fs.Int64("group-id", 0, "Group ID")
	description := fs.String("description", "", "Optional description")
	icon := fs.String("icon", "", "Icon name")
	credentialID := fs.Int64("credential-id", 0, "Existing managed credential ID")
	password := fs.String("password", "", "Password plaintext (unsafe argv path); bare --password reads it interactively")
	agentSourceID := fs.Int64("agent-source-id", 0, "SSH Agent source ID")
	agentFingerprint := fs.String("agent-key-fingerprint", "", "SSH Agent key SHA256 fingerprint")
	fs.Usage = printCreateAssetUsage
	// 预扫描把「下一个 token 以 - 开头」判成裸写，因此以 - 开头的密码值会落到这里。
	// 该 token 不是已注册 flag 时，Go 只会报 "flag provided but not defined"，对用户
	// 不可读——给出定向指引，且不回显该 token（它可能就是密码本身）。
	if promptPassword && bareFollowedBy != "" && fs.Lookup(flagLookupName(bareFollowedBy)) == nil {
		return nil, errors.New(`a bare --password must be followed by another flag or nothing; if the password starts with "-", write it as --password=<value>`)
	}
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() != 0 {
		return nil, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *name == "" {
		return nil, fmt.Errorf("--name is required")
	}
	visited := visitedFlags(fs)
	if visited["config"] && visited["config-file"] {
		return nil, fmt.Errorf("--config and --config-file are mutually exclusive")
	}

	config := map[string]any{}
	var configSource string
	if visited["config"] {
		configSource = "--config"
		if err := decodeJSONObject([]byte(*configJSON), configSource, &config); err != nil {
			return nil, err
		}
	}
	if visited["config-file"] {
		configSource = "--config-file"
		data, err := deps.readFile(*configFile)
		if err != nil {
			return nil, fmt.Errorf("read --config-file: %w", err)
		}
		if err := decodeJSONObject(data, configSource, &config); err != nil {
			return nil, err
		}
	}

	secretInConfig, configSecretSources := presentSecretFields(config)
	if configSecretSources > 1 {
		return nil, fmt.Errorf("credential and plaintext secret sources are mutually exclusive")
	}
	flagSecretSources := 0
	if visited["credential-id"] {
		flagSecretSources++
	}
	if visited["password"] {
		flagSecretSources++
	}
	if promptPassword {
		if visited["password"] {
			return nil, errors.New("--password may be given once: either with a value, or bare to type it interactively")
		}
		flagSecretSources++
	}
	if flagSecretSources > 1 || (flagSecretSources > 0 && secretInConfig != "") {
		return nil, fmt.Errorf("credential and plaintext secret sources are mutually exclusive")
	}

	if visited["password"] {
		if err := warnArgvPlaintext(deps.stderr); err != nil {
			return nil, err
		}
	}
	if configSource == "--config" && secretInConfig != "" {
		if err := warnArgvPlaintext(deps.stderr); err != nil {
			return nil, err
		}
	}
	if configSource == "--config-file" && secretInConfig != "" {
		if err := warnConfigFilePlaintext(deps.stderr); err != nil {
			return nil, err
		}
	}

	if visited["credential-id"] {
		config["credential_id"] = *credentialID
	}
	plaintextField := plaintextConfigField(*assetType)
	if visited["password"] {
		if *password == "" {
			return nil, fmt.Errorf("--password must not be empty")
		}
		config[plaintextField] = *password
	}

	legacy := map[string]any{
		"host":                  *host,
		"port":                  *port,
		"username":              *username,
		"auth-type":             *authType,
		"driver":                *driver,
		"database":              *database,
		"read-only":             *readOnly,
		"kubeconfig":            *kubeconfig,
		"namespace":             *k8sNamespace,
		"context":               *k8sContext,
		"agent-source-id":       *agentSourceID,
		"agent-key-fingerprint": *agentFingerprint,
	}
	configKeys := map[string]string{
		"auth-type": "auth_type", "read-only": "read_only",
		"agent-source-id": "agent_source_id", "agent-key-fingerprint": "agent_key_fingerprint",
	}
	for flagName, value := range legacy {
		if !visited[flagName] {
			continue
		}
		key := flagName
		if mapped := configKeys[flagName]; mapped != "" {
			key = mapped
		}
		config[key] = value
	}
	if visited["ssh-asset"] {
		id, err := deps.resolveAssetID(ctx, *sshAsset)
		if err != nil {
			return nil, fmt.Errorf("resolve --ssh-asset: %w", err)
		}
		config["ssh_asset_id"] = id
	}
	if visited["kubeconfig-file"] {
		data, err := deps.readFile(*kubeconfigFile)
		if err != nil {
			return nil, fmt.Errorf("read --kubeconfig-file: %w", err)
		}
		config["kubeconfig"] = string(data)
	}

	// 交互提示排在最后：--name / 互斥 / JSON / --ssh-asset / --kubeconfig-file 全部
	// 通过之后才问，参数写错时用户不必白输一遍密码。
	if promptPassword {
		if _, ok := assettype.Get(*assetType); !ok {
			return nil, fmt.Errorf("unsupported asset type %q (registered types: %s)",
				*assetType, strings.Join(assettype.RegisteredTypes(), ", "))
		}
		if deps.promptSecret == nil {
			return nil, needsTerminalForPassword(ctx)
		}
		secret, err := deps.promptSecret(passwordPrompt(ctx))
		if err != nil {
			return nil, fmt.Errorf("read --password: %w", err)
		}
		if secret == "" {
			return nil, fmt.Errorf("--password must not be empty")
		}
		config[plaintextField] = secret
	}

	asset := &asset_entity.Asset{Name: *name, Type: *assetType}
	if visited["group-id"] {
		asset.GroupID = *groupID
	}
	if visited["description"] {
		asset.Description = *description
	}
	if visited["icon"] {
		asset.Icon = *icon
	}
	return &assetCreateRequest{asset: asset, config: config}, nil
}

// passwordPrompt 是裸写 --password 的终端提示语。
func passwordPrompt(ctx context.Context) string {
	return policy.PolicyMsg(ctx, "Password: ", "密码：")
}

// needsTerminalForPassword 是裸写 --password 但环境不可交互时的结构化拒绝：退出码 3
// + stderr 首行 NEEDS TTY，与 policy 写类子命令同一形状——都是「只能由人在终端里
// 做」。正文给出两条出路：改用 --password=<value>，或把原命令交给人自己执行。
func needsTerminalForPassword(ctx context.Context) error {
	var sb strings.Builder
	sb.WriteString(policy.PolicyMsg(ctx,
		"a bare --password must be typed in an interactive terminal, but stdin or stderr is not one",
		"不带值的 --password 需要交互式终端，但当前 stdin 或 stderr 不是终端"))
	sb.WriteString("\n")
	sb.WriteString(policy.PolicyMsg(ctx,
		"Use --password=<value> instead, or run the command yourself in your terminal.",
		"请改用 --password=<value>，或在你自己的终端里执行该命令。"))
	if cmd := originCommandFromCtx(ctx); cmd != "" {
		fmt.Fprintf(&sb, "\n  %s", cmd)
	}
	return &structuredRefusal{marker: needsTTYMarker, body: sb.String()}
}

func plaintextConfigField(assetType string) string {
	if handler, ok := assettype.Get(assetType); ok {
		for _, field := range handler.AutomationContract().ConfigFields {
			if field == "password" || field == "secret_access_key" {
				return field
			}
		}
	}
	return "password"
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	return visited
}

func decodeJSONObject(data []byte, source string, out *map[string]any) error {
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid %s JSON: %w", source, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("invalid %s JSON: %w", source, err)
	}
	object, ok := value.(map[string]any)
	if !ok || object == nil {
		return fmt.Errorf("%s must contain a JSON object", source)
	}
	*out = normalizeJSONNumbers(object)
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return errors.New("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func normalizeJSONNumbers(config map[string]any) map[string]any {
	out := make(map[string]any, len(config))
	for key, value := range config {
		switch number := value.(type) {
		case json.Number:
			if integer, err := number.Int64(); err == nil {
				out[key] = integer
			} else if decimal, err := number.Float64(); err == nil {
				out[key] = decimal
			} else {
				out[key] = value
			}
		default:
			out[key] = value
		}
	}
	return out
}

func presentSecretFields(config map[string]any) (string, int) {
	first := ""
	count := 0
	addSource := func(field string) {
		if first == "" {
			first = field
		}
		count++
	}
	for _, field := range []string{"credential_id", "password", "secret_access_key"} {
		if value, ok := config[field]; ok && configValuePresent(value) {
			addSource(field)
		}
	}
	privateKeyPresent := configValuePresent(config["private_key"])
	passphrasePresent := configValuePresent(config["passphrase"])
	if privateKeyPresent {
		addSource("private_key")
	} else if passphrasePresent {
		addSource("passphrase")
	}
	return first, count
}

func configValuePresent(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return typed != ""
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return true
	}
}

// splitPasswordPrompt 在 flag 解析之前区分裸写与带值的 --password，并把裸写的
// token 摘走，让 --password 在 flag 层保持普通字符串 flag。
//
// 这一步不能交给 flag 包：Go 的可选值 flag（IsBoolFlag）只认 --password=<value>，
// 会把 --password <value> 的值解析成 "true" 并把真正的值丢进位置参数，破坏已发布
// 的空格形式；摘走裸写 token 之后 --password=true / --password true 也不再与裸写
// 混淆。裸写判据：--password 是最后一个 token，或它的下一个 token 以 "-" 开头。
// 第三个返回值是裸写后面那个以 "-" 开头的 token（没有则为空），供调用方区分
// 「后面确实是另一个 flag」与「密码值以 - 开头」。
func splitPasswordPrompt(args []string) (rest []string, prompt bool, bareFollowedBy string) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--password" || args[i] == "-password" {
			if i+1 >= len(args) {
				prompt = true
				continue
			}
			if strings.HasPrefix(args[i+1], "-") {
				prompt = true
				bareFollowedBy = args[i+1]
				continue
			}
		}
		rest = append(rest, args[i])
	}
	return rest, prompt, bareFollowedBy
}

// flagLookupName 把一个 argv token 还原成 flag 名，供 FlagSet.Lookup 判断它是否
// 是已注册 flag（--host=1.2.3.4 → host）。
func flagLookupName(arg string) string {
	name := strings.TrimLeft(arg, "-")
	if idx := strings.IndexByte(name, '='); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// retiredPasswordStdinFlag 认出已退役的 --password-stdin 并原样回报，好把沿用者
// 指回 --password——它在 v1.13.0 发布过并被技能文档推荐，落到 Go flag 的
// "flag provided but not defined" 上没有任何指引价值。
func retiredPasswordStdinFlag(args []string) string {
	for _, arg := range args {
		name := flagLookupName(arg)
		if name == "password-stdin" && strings.HasPrefix(arg, "-") {
			return "--password-stdin"
		}
	}
	return ""
}

func warnArgvPlaintext(stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, "Warning: plaintext supplied in argv may be exposed in shell history, process listings, and CI/automation logs; prefer bare --password to type it interactively, or --credential-id."); err != nil {
		return fmt.Errorf("write plaintext argv warning: %w", err)
	}
	return nil
}

func warnConfigFilePlaintext(stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, "Warning: plaintext config files require restrictive permissions; do not commit them, and remove them when no longer needed."); err != nil {
		return fmt.Errorf("write plaintext config file warning: %w", err)
	}
	return nil
}
