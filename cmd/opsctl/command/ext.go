package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

var delegateExtExecFn = delegateExtExec

// extensionsDirFn 定位扩展目录。变量化是为了可测，与本包 execApprovalFn 等同一套路。
var extensionsDirFn = func() string { return filepath.Join(bootstrap.ResolvedDataDir(), "extensions") }

func cmdExt(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExtUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	switch args[0] {
	case "list":
		return cmdExtList()
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown ext subcommand %q\n\nRun 'opsctl ext --help' for usage.\n", args[0])
		return 1
	}
}

// installedExtension is the opsctl-side view of one extension directory.
type installedExtension struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	DisplayName string   `json:"displayName,omitempty"`
	Description string   `json:"description,omitempty"`
	Enabled     bool     `json:"enabled"`
	AssetTypes  []string `json:"assetTypes,omitempty"`
	Tools       []string `json:"tools,omitempty"`
}

// scanInstalledExtensions reads every extension directory through the real manifest
// parser and applies the enabled/disabled state the desktop app persists.
//
// It used to hand-roll a second manifest parser (an anonymous struct pulling four
// fields) that also ignored enabled state, so `opsctl ext list` happily listed
// extensions the user had switched off and silently accepted manifests the app itself
// would refuse. Both are the same mistake: a second, laxer reader of a contract that
// already has one owner.
func scanInstalledExtensions() ([]installedExtension, error) {
	extDir := extensionsDirFn()
	entries, err := os.ReadDir(extDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extensions directory: %w", err)
	}

	disabled := disabledExtensionNames()

	results := make([]installedExtension, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := extension.LoadManifestInfo(filepath.Join(extDir, entry.Name()))
		if err != nil {
			// A directory without a readable/valid manifest is not an extension; the
			// desktop app skips it the same way.
			continue
		}
		m := info.Manifest
		item := installedExtension{
			Name:        m.Name,
			Version:     m.Version,
			DisplayName: info.Translate("en", m.I18n.DisplayName),
			Description: info.Translate("en", m.I18n.Description),
			Enabled:     !disabled[m.Name],
		}
		for _, at := range m.AssetTypes {
			item.AssetTypes = append(item.AssetTypes, at.Type)
		}
		for _, t := range m.Tools {
			item.Tools = append(item.Tools, t.Name)
		}
		results = append(results, item)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

// disabledExtensionNames reads the persisted enabled/disabled state. An unreadable DB
// is reported as "nothing disabled" — the same default the desktop app applies to an
// extension with no state row — and logged, not silently swallowed into a wrong answer
// about a specific extension.
func disabledExtensionNames() map[string]bool {
	repo := extension_state_repo.ExtensionState()
	if repo == nil {
		return nil
	}
	states, err := repo.FindAll(context.Background())
	if err != nil {
		logger.Default().Warn("read extension state", zap.Error(err))
		return nil
	}
	out := make(map[string]bool, len(states))
	for _, s := range states {
		if !s.Enabled {
			out[s.Name] = true
		}
	}
	return out
}

// extensionAssetTypeOwners maps every enabled extension's asset types to its extension
// name. Disabled extensions are excluded: their asset types are not registered in the
// running app either, so a command against one must fail like any unknown type.
func extensionAssetTypeOwners() map[string]string {
	items, err := scanInstalledExtensions()
	if err != nil {
		logger.Default().Warn("scan installed extensions", zap.Error(err))
		return nil
	}
	owners := make(map[string]string)
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		for _, at := range item.AssetTypes {
			owners[at] = item.Name
		}
	}
	return owners
}

func cmdExtList() int {
	results, err := scanInstalledExtensions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if results == nil {
		results = []installedExtension{}
	}
	out, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: encode extension list: %v\n", err)
		return 1
	}
	fmt.Println(string(out))
	return 0
}

// delegateExtExec hands one exec on an extension asset to the running desktop app.
//
// The reason is execution location, not semantics: the WASM runtime, the extension's
// host capabilities and its decrypted asset config only exist inside the desktop
// process. The command string is exactly the one the user typed after `--`, and the app
// runs it through the same unified exec handler an AI session would, so policy, the
// approval dialog, grants and audit are identical.
func delegateExtExec(assetID int64, assetName, command, session string) (string, error) {
	dataDir := bootstrap.ResolvedDataDir()
	sockPath := approval.SocketPath(dataDir)

	token, err := bootstrap.ReadAuthToken(dataDir)
	if err != nil {
		logger.Default().Warn("read auth token", zap.Error(err))
	}

	resp, err := approval.RequestApprovalWithToken(sockPath, token, approval.ApprovalRequest{
		Type:      "ext_tool",
		AssetID:   assetID,
		AssetName: assetName,
		Command:   command,
		SessionID: session,
	})
	if err != nil {
		return "", err
	}
	if resp.ToolError != "" {
		return "", fmt.Errorf("%s", resp.ToolError)
	}
	return resp.ToolResult, nil
}

func printExtUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl ext <subcommand>

Subcommands:
  list    List installed extensions with their asset types, tools, and enabled state

To run an extension tool, use exec against one of the extension's assets:

  opsctl exec <asset> -- <tool> --flag=value

That is the same command built-in asset types use; the asset's type selects the
extension. Run 'opsctl help <asset>' to see the tool and flag reference.

Examples:
  opsctl ext list
  opsctl exec my-bucket -- list_objects --bucket=logs --maxKeys=100
`)
}
