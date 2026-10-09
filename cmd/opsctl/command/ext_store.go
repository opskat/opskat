package command

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/approval"
)

// storeSearchFn / storeInstallFn are the desktop round trips behind ext search /
// install / update, variables for the same reason devInstallFn is.
var (
	storeSearchFn  = requestStoreSearch
	storeInstallFn = requestStoreInstall
)

// requestStoreSearch asks the running desktop app for the official store. The
// verified index, the download mirror and the installed set only exist there, so
// opsctl reads the store through the app instead of fetching a second copy. It is
// read-only: no dialog is shown.
func requestStoreSearch(lang string) ([]approval.ExtStoreEntry, error) {
	resp, err := requestDesktop(approval.ApprovalRequest{Type: approval.TypeExtStoreSearch, Lang: lang})
	if err != nil {
		return nil, err
	}
	if !resp.Approved {
		return nil, fmt.Errorf("%s", resp.Reason)
	}
	return resp.StoreExtensions, nil
}

// requestStoreInstall asks the running desktop app to install name from the store.
// The app shows its install confirm — the same one the store page's Install button
// shows — and that confirm is the user's approval; the response says what landed
// or, in ErrorKind, why nothing did.
func requestStoreInstall(name string) (approval.ApprovalResponse, error) {
	return requestDesktop(approval.ApprovalRequest{Type: approval.TypeExtStoreInstall, Extension: name})
}

// storeLang is the index language tag display names are asked for in, following
// the same locale the rest of opsctl's messages follow.
func storeLang(ctx context.Context) string {
	if policy.IsZh(ctx) {
		return "zh-CN"
	}
	return "en"
}

// cmdExtSearch lists the official store: name, display name, newest compatible
// version, installed version and status.
func cmdExtSearch(ctx context.Context, args []string) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printExtStoreUsage()
		return 0
	}
	if len(args) > 1 && rejectExtraArgs(args[1:]) {
		return 1
	}
	entries, err := storeSearchFn(storeLang(ctx))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if len(args) == 1 {
		entries = matchStore(entries, args[0])
	}
	if len(entries) == 0 {
		fmt.Println(policy.PolicyMsg(ctx, "No extensions found.", "没有找到扩展。"))
		return 0
	}

	var sb strings.Builder
	w := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, policy.PolicyMsg(ctx, //nolint:errcheck // 终端呈现尽力而为
		"NAME\tDISPLAY NAME\tLATEST\tINSTALLED\tSTATUS",
		"名称\t显示名\t最新兼容版本\t已安装\t状态"))
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", //nolint:errcheck // 终端呈现尽力而为
			e.Name, e.DisplayName, nonEmpty(e.Latest), nonEmpty(e.Installed), storeStatus(ctx, e))
	}
	if err := w.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Print(sb.String())
	return 0
}

// matchStore keeps the entries whose name, display name or description contains
// keyword, case-insensitively — the fields the store page's search matches.
func matchStore(entries []approval.ExtStoreEntry, keyword string) []approval.ExtStoreEntry {
	kw := strings.ToLower(keyword)
	var out []approval.ExtStoreEntry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), kw) ||
			strings.Contains(strings.ToLower(e.DisplayName), kw) ||
			strings.Contains(strings.ToLower(e.Description), kw) {
			out = append(out, e)
		}
	}
	return out
}

func storeStatus(ctx context.Context, e approval.ExtStoreEntry) string {
	switch e.Status {
	case approval.ExtStoreStatusInstall:
		return policy.PolicyMsg(ctx, "not installed", "未安装")
	case approval.ExtStoreStatusUpdate:
		return policy.PolicyMsg(ctx, "update available", "可更新")
	case approval.ExtStoreStatusInstalled:
		return policy.PolicyMsg(ctx, "installed", "已安装")
	default:
		return policy.PolicyFmt(ctx, "incompatible: %s", "不兼容：%s", e.Reason)
	}
}

// findStoreEntry looks name up in the store; a name the index lacks is an error.
func findStoreEntry(ctx context.Context, name string) (approval.ExtStoreEntry, error) {
	entries, err := storeSearchFn(storeLang(ctx))
	if err != nil {
		return approval.ExtStoreEntry{}, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return approval.ExtStoreEntry{}, fmt.Errorf("extension %q is not in the store index", name)
}

// cmdExtInstall installs the newest compatible version of one extension through
// the desktop app's install confirm. An installed version at or above it is
// reported and left alone.
func cmdExtInstall(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExtStoreUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if rejectExtraArgs(args[1:]) {
		return 1
	}
	name := args[0]
	e, err := findStoreEntry(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	switch e.Status {
	case approval.ExtStoreStatusInstalled:
		fmt.Println(policy.PolicyFmt(ctx,
			"%s %s is already installed (newest compatible: %s); not reinstalled.",
			"%s %s 已安装（最新兼容版本：%s），不重复安装。",
			name, e.Installed, nonEmpty(e.Latest)))
		return 0
	case approval.ExtStoreStatusUnavailable:
		fmt.Fprintf(os.Stderr, "Error: %s cannot be installed: %s\n", name, e.Reason)
		return 1
	}
	if !installFromStore(ctx, name) {
		return 1
	}
	return 0
}

// cmdExtUpdate updates one installed extension, or with --all every extension
// with an update, each through its own desktop confirm.
func cmdExtUpdate(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExtStoreUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if rejectExtraArgs(args[1:]) {
		return 1
	}
	if args[0] == "--all" {
		return updateAllFromStore(ctx)
	}

	name := args[0]
	e, err := findStoreEntry(ctx, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	if e.Installed == "" {
		fmt.Fprintf(os.Stderr, "Error: %s is not installed — install it with `opsctl ext install %s`\n", name, name)
		return 1
	}
	if e.Status != approval.ExtStoreStatusUpdate {
		fmt.Println(policy.PolicyFmt(ctx,
			"%s %s: no update available.",
			"%s %s：没有可用更新。",
			name, e.Installed))
		return 0
	}
	if !installFromStore(ctx, name) {
		return 1
	}
	return 0
}

func updateAllFromStore(ctx context.Context) int {
	entries, err := storeSearchFn(storeLang(ctx))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	var updatable []string
	for _, e := range entries {
		if e.Status == approval.ExtStoreStatusUpdate {
			updatable = append(updatable, e.Name)
		}
	}
	if len(updatable) == 0 {
		fmt.Println(policy.PolicyMsg(ctx, "No updates available.", "没有可用更新。"))
		return 0
	}
	// One request per extension: the app confirms each update on its own, and a
	// declined or failed one does not stop the rest.
	code := 0
	for _, name := range updatable {
		if !installFromStore(ctx, name) {
			code = 1
		}
	}
	return code
}

// installFromStore has the desktop app install name and reports the outcome; it
// returns false when nothing was installed for a reason the caller must fail on.
func installFromStore(ctx context.Context, name string) bool {
	resp, err := storeInstallFn(name)
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "Error: %s: %v\n", name, err)
		return false
	case resp.Approved:
		fmt.Println(policy.PolicyFmt(ctx, "Installed %s %s", "已安装 %s %s", resp.Extension, resp.Version))
		return true
	case resp.ErrorKind == approval.ExtStoreInstallUpToDate:
		// Another install got there first; the extension is current.
		fmt.Println(resp.Reason)
		return true
	case resp.ErrorKind == approval.ExtStoreInstallCanceled:
		fmt.Fprintf(os.Stderr, "Error: %s: %s\n", name, resp.Reason)
		return false
	default:
		fmt.Fprintf(os.Stderr, "Error: %s: %s (%s)\n", name, resp.Reason, resp.ErrorKind)
		return false
	}
}

func printExtStoreUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl ext search [keyword]
  opsctl ext install <name>
  opsctl ext update <name> | --all

Browse and install extensions from the official store through the running
desktop app, which holds the signature-verified store index.

  search   List the store: name, display name, newest compatible version,
           installed version and status (not installed / installed /
           update available / incompatible and why). The keyword matches
           name, display name and description. Asks for nothing.
  install  Install the newest compatible version. An installed version at
           or above it is reported and left alone.
  update   Update one installed extension, or with --all every extension
           with an update, to the newest compatible version.

The app asks you to confirm each install or update — with --all, each
extension separately — and verifies the package's signed sha256 before
installing. A declined confirm, a failed download or verification, an
incompatible extension or an app that is not running exits non-zero with
the reason.
`)
}
