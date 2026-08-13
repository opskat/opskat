package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

const maxPasswordStdinBytes = 1 << 20

type assetCreateRequest struct {
	asset          *asset_entity.Asset
	config         map[string]any
	credentialName string
}

type assetCreateParserDeps struct {
	stdin          io.Reader
	stderr         io.Writer
	readFile       func(string) ([]byte, error)
	resolveAssetID func(context.Context, string) (int64, error)
}

func parseAssetCreate(ctx context.Context, args []string, deps assetCreateParserDeps) (*assetCreateRequest, error) {
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
	passwordStdin := fs.Bool("password-stdin", false, "Read password from stdin")
	password := fs.String("password", "", "Password plaintext (unsafe argv path)")
	credentialName := fs.String("credential-name", "", "Name for a newly managed credential")
	agentSourceID := fs.Int64("agent-source-id", 0, "SSH Agent source ID")
	agentFingerprint := fs.String("agent-key-fingerprint", "", "SSH Agent key SHA256 fingerprint")
	fs.Usage = printCreateAssetUsage
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
	if visited["password-stdin"] && *passwordStdin {
		flagSecretSources++
	}
	if visited["password"] {
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
	if visited["password-stdin"] && *passwordStdin {
		secret, err := readPasswordStdin(deps.stdin)
		if err != nil {
			return nil, err
		}
		config[plaintextField] = secret
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
	return &assetCreateRequest{asset: asset, config: config, credentialName: *credentialName}, nil
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

func readPasswordStdin(reader io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxPasswordStdinBytes+1))
	if err != nil {
		return "", fmt.Errorf("read --password-stdin: %w", err)
	}
	if len(data) > maxPasswordStdinBytes {
		return "", fmt.Errorf("--password-stdin exceeds %d bytes", maxPasswordStdinBytes)
	}
	if strings.HasSuffix(string(data), "\r\n") {
		data = data[:len(data)-2]
	} else if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	if len(data) == 0 {
		return "", fmt.Errorf("--password-stdin read an empty secret")
	}
	return string(data), nil
}

func warnArgvPlaintext(stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, "Warning: plaintext supplied in argv may be exposed in shell history, process listings, and CI/automation logs; prefer --password-stdin or --credential-id."); err != nil {
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
