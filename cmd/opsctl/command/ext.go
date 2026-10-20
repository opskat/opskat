package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/bootstrap"
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/extreg"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// devInstallFn 变量化：让 cmdExtDev 的判断可测，不必真起一个桌面端。
var devInstallFn = requestExtDevInstall

// extregRegisterFn 是"仅描述注册"的接缝，同上一套路。
var extregRegisterFn = extreg.RegisterDescribeOnly

// extensionsDirFn 定位扩展目录。变量化是为了可测，与本包 execApprovalFn 等同一套路。
var extensionsDirFn = func() string { return filepath.Join(bootstrap.ResolvedDataDir(), "extensions") }

// activateExtensionFn 在本进程里完整激活一个扩展：加载它的 WASM，并把启动时的仅描述
// 注册换成完整注册——策略检查、执行器、命令规范化、配置校验都接上，之后它的资产与
// 内置类型走同一条 requireApproval → callHandler 路径。变量化同上一套路。
var activateExtensionFn = activateExtension

// extensionHost 是本进程的扩展宿主：第一次激活时才建，只装被激活的扩展
// （spec 决策 3：opsctl 只加载目标资产所属的扩展），由 releaseExtensions 在命令结束时释放。
var extensionHost struct {
	mu  sync.Mutex
	svc *extension_svc.Service
}

func activateExtension(ctx context.Context, extName string) error {
	extensionHost.mu.Lock()
	if extensionHost.svc == nil {
		extensionHost.svc = newExtensionHost()
	}
	svc := extensionHost.svc
	extensionHost.mu.Unlock()
	return svc.Activate(ctx, extName)
}

// newExtensionHost 用与桌面端同一套无界面宿主能力（extension_svc.Service.HostConfig：
// 资产配置与凭据、KV、资产连接路径）建扩展运行时；桌面窗口才有的两项里，文件对话框
// 换成 noFileDialog，action 事件不接（exec 只调工具，不跑 action）。
//
// 日志走 logger.Default()：opsctl 不初始化日志，它是 no-op，扩展的日志与 guest 的
// 标准输出 / 标准错误（pkg/extension guestLogWriter 也写它）因此永远不进 opsctl 的
// 标准输出——管道与脚本拿到的只有结果。
func newExtensionHost() *extension_svc.Service {
	var svc *extension_svc.Service // 工厂只在扩展加载时调用，那时已赋值
	mgr := extension.NewManager(extensionsDirFn(), func(extName string) extension.HostProvider {
		cfg := svc.HostConfig(extName)
		cfg.FileDialogs = noFileDialog{}
		return extension.NewDefaultHostProvider(cfg)
	}, logger.Default())
	svc = extension_svc.New(mgr,
		extension_state_repo.ExtensionState(),
		extension_data_repo.ExtensionData(),
		asset_repo.Asset(),
		logger.Default(), nil, nil)
	// 完整注册遇到声明了测试连接的类型就要 registrar，激活前接好。
	extreg.SetConnTestRegistrar(svc.ConnTestRegistrar())
	return svc
}

// releaseExtensions 撤掉本进程激活过的扩展并关闭它们的 WASM 运行时。root.go 在命令
// 结束时调用；没有激活过任何扩展时什么也不做。
func releaseExtensions(ctx context.Context) {
	extensionHost.mu.Lock()
	defer extensionHost.mu.Unlock()
	if extensionHost.svc == nil {
		return
	}
	for _, name := range extensionHost.svc.Bridge().ListNames() {
		extreg.Unregister(name)
	}
	extensionHost.svc.Close(ctx)
	extensionHost.svc = nil
}

// errNoFileDialog 是 opsctl 对"工具要弹文件对话框"的回答（spec 决策 6）：opsctl 没有
// 图形界面，工具要读的文件经它声明的 --<flag>-file 参数从命令行传入（opsctl help
// <asset> 的参数表列出了哪些参数可以这样传）。
var errNoFileDialog = errors.New("opsctl has no file dialog: pass the file through the tool's --<flag>-file parameter instead (see 'opsctl help <asset>')")

type noFileDialog struct{}

func (noFileDialog) FileDialog(string, extension.DialogOptions) (string, error) {
	return "", errNoFileDialog
}

// extensionOwnerOf 回答 assetType 由哪个已安装的扩展提供。已启用的扩展由启动时的
// registerExtensionAssetTypes 接进了资产类型注册表，直接问它；注册表不认识的类型再到
// 已安装的扩展里找——停用的扩展不注册，它的资产却仍在库里，必须认得出来，激活才能点名
// 说它已停用，而不是让 exec 报"类型不支持"。内置类型从不走到扫描。
func extensionOwnerOf(assetType string) (string, bool, error) {
	if name, ok := assettype.ExtensionOwnerOf(assetType); ok {
		return name, true, nil
	}
	if _, registered := assettype.Get(assetType); registered {
		return "", false, nil
	}
	scanned, err := scanExtensionDirs()
	if err != nil {
		return "", false, err
	}
	for _, ext := range scanned {
		for _, at := range ext.info.Manifest.AssetTypes {
			if at.Type == assetType {
				return ext.info.Name, true, nil
			}
		}
	}
	return "", false, nil
}

// driverExtensionOf 回答数据库资产的驱动由哪个已安装的扩展提供。内置驱动与非数据库资产
// 报 false。已启用的扩展由启动时的 registerExtensionAssetTypes 把驱动接进了驱动注册表，
// 直接问它；注册表不认识的驱动 id 再到已安装的扩展声明里找——停用的扩展不注册，它的资产
// 却仍在库里，必须认得出来，激活才能点名说它已停用。内置驱动先查：扩展不能占用内置 id，
// 磁盘上声明了同名驱动的 manifest 不会让 opsctl 改道。
func driverExtensionOf(asset *asset_entity.Asset) (extName string, driverID dbdriver.DriverID, ok bool, err error) {
	if !asset.IsDatabase() {
		return "", "", false, nil
	}
	cfg, err := asset.GetDatabaseConfig()
	if err != nil {
		return "", "", false, err
	}
	id := dbdriver.DriverID(cfg.Driver)
	if name, ok := extreg.DriverOwnerOf(id); ok {
		return name, id, true, nil
	}
	if _, lookupErr := dbregistry.Lookup(id); lookupErr == nil {
		return "", id, false, nil
	}
	scanned, err := scanExtensionDirs()
	if err != nil {
		return "", id, false, err
	}
	for _, ext := range scanned {
		if slices.Contains(ext.info.Manifest.Capabilities.DatabaseDrivers, string(id)) {
			return ext.info.Name, id, true, nil
		}
	}
	return "", id, false, nil
}

// activateDriverExtension 在 exec 前为扩展提供驱动的数据库资产激活该扩展：之后
// connpool.OpenDatabase 从驱动注册表拿到的是能执行的驱动，审批、审计与内置驱动同一条
// 路径。激活失败（扩展在桌面端被停用、加载失败）说成「驱动不可用」，点名驱动与扩展，
// 与查询面板、AI 对同一情形的说法一致。其他资产什么也不做。activate 是激活入口，
// batch 经它让同一扩展每批只激活一次。
func activateDriverExtension(ctx context.Context, asset *asset_entity.Asset, activate func(context.Context, string) error) error {
	extName, id, ok, err := driverExtensionOf(asset)
	if err != nil || !ok {
		return err
	}
	if err := activate(ctx, extName); err != nil {
		return fmt.Errorf("%w: %q, provided by extension %q: %w", dbregistry.ErrDriverUnavailable, id, extName, err)
	}
	return nil
}

func cmdExt(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExtUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}

	switch args[0] {
	case "list":
		if rejectExtraArgs(args[1:]) {
			return 1
		}
		return cmdExtList()
	case "dev":
		return cmdExtDev(args[1:])
	case "search":
		return cmdExtSearch(ctx, args[1:])
	case "install":
		return cmdExtInstall(ctx, args[1:])
	case "update":
		return cmdExtUpdate(ctx, args[1:])
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
// parser and applies the enabled/disabled state the desktop app persists, so it lists
// exactly what the app would load.
func scanInstalledExtensions() ([]installedExtension, error) {
	scanned, err := scanExtensionDirs()
	if err != nil {
		return nil, err
	}
	results := make([]installedExtension, 0, len(scanned))
	for _, ext := range scanned {
		m := ext.info.Manifest
		item := installedExtension{
			Name:        m.Name,
			Version:     m.Version,
			DisplayName: ext.info.Translate("en", m.I18n.DisplayName),
			Description: ext.info.Translate("en", m.I18n.Description),
			Enabled:     ext.enabled,
		}
		for _, at := range m.AssetTypes {
			item.AssetTypes = append(item.AssetTypes, at.Type)
		}
		for _, t := range m.Tools {
			item.Tools = append(item.Tools, t.Name)
		}
		results = append(results, item)
	}
	return results, nil
}

// scannedExtension is one extension directory read without a WASM runtime, plus the
// enabled/disabled state the desktop app persists.
type scannedExtension struct {
	info    *extension.ManifestInfo
	enabled bool
}

// scanExtensionDirs reads every extension directory through the real manifest parser,
// sorted by name. It is the single scan behind both `ext list` and the startup
// registration below, so the two cannot disagree about what is installed.
func scanExtensionDirs() ([]scannedExtension, error) {
	extDir := extensionsDirFn()
	entries, err := os.ReadDir(extDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read extensions directory: %w", err)
	}

	disabled := disabledExtensionNames()

	results := make([]scannedExtension, 0, len(entries))
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
		results = append(results, scannedExtension{info: info, enabled: !disabled[info.Name]})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].info.Name < results[j].info.Name })
	return results, nil
}

// registerExtensionAssetTypes wires every enabled extension's non-executing face into
// this process's registries, from the descriptor cached the last time any process
// loaded that extension (extension_describe). The cheap path compiles no WASM.
//
// Without it opsctl is blind to extension asset types while the app on the same
// machine is not: `create asset --type notebook` reports an unsupported type, `help
// notebook` lists only the built-ins and `policy show` on an extension asset refuses
// with "no permanent rule support". `exec` starts from this registration too: it then
// activates just the asset's own extension in full (activateExtensionFn), replacing
// that extension's describe-only entry with one that can execute.
//
// An enabled extension with no cached answer (the desktop app never loaded it on this
// machine) is described here first (describeExtensionFn), which also writes the cache
// for the next command; a disabled one is never loaded. Without the answer opsctl
// cannot tell which extension owns an asset type.
//
// Failures are reported and skipped rather than fatal: a single unregisterable
// extension (a name collision with a built-in type, a describe() answer this binary is
// too old to accept) must not take down every unrelated opsctl command.
func registerExtensionAssetTypes() {
	extensionDescribeFailures = nil
	scanned, err := scanExtensionDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		return
	}
	for _, ext := range scanned {
		if !ext.enabled {
			// A disabled extension is not registered in the running app either.
			continue
		}
		info := ext.info
		// 描述缓存里的答案至少声明一个资产类型或一个数据库驱动；两者皆无才是缓存缺失。
		if len(info.Manifest.AssetTypes) == 0 && len(info.Manifest.DatabaseDrivers) == 0 {
			if info, err = describeExtensionFn(context.Background(), ext.info.Dir); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: describe extension %q: %v\n", ext.info.Name, err)
				extensionDescribeFailures = append(extensionDescribeFailures, extensionDescribeFailure{name: ext.info.Name, err: err})
				continue
			}
		}
		if err := extregRegisterFn(info); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
		}
	}
}

// extensionDescribeFailure is one enabled extension whose describe() could not be
// obtained in this run, so none of its asset types got registered.
type extensionDescribeFailure struct {
	name string
	err  error
}

// extensionDescribeFailures is what the last registerExtensionAssetTypes could not
// describe. Recorded once there, at the producer; exec and batch read it to say why an
// asset type nobody claimed has no exec support (opsctl cannot tell which of these
// extensions owns the type — the description that would say is what failed).
var extensionDescribeFailures []extensionDescribeFailure

// describeExtensionFn 变量化同上一套路。
var describeExtensionFn = describeExtension

// describeExtension 补缓存缺失的扩展：加载它的 WASM 取 describe() 的答案（Manager 在
// 缓存缺失时顺手写入 extension_describe，供下一条命令用），随即释放。返回的清单带的是
// 这次加载拿到的答案，而不是从缓存重读——缓存写入失败只让下一条命令再描述一次，
// 不让这一条命令丢掉刚拿到的描述。它只取描述，不接入 extreg——真正执行由
// activateExtensionFn 在 exec 时完整激活。
func describeExtension(ctx context.Context, dir string) (*extension.ManifestInfo, error) {
	mgr := extension.NewManager(filepath.Dir(dir), func(string) extension.HostProvider {
		return extension.NewDefaultHostProvider(extension.DefaultHostConfig{})
	}, logger.Default())
	defer mgr.Close(ctx)
	described, err := mgr.LoadExtension(ctx, dir)
	if err != nil {
		return nil, err
	}
	info, err := extension.LoadManifestInfo(dir)
	if err != nil {
		return nil, err
	}
	info.Manifest = described
	return info, nil
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

// cmdExtDev 把一个未打包的扩展目录装进运行中的桌面应用。
//
// 扩展开发没有第二套宿主——安装、加载、注册全部发生在桌面进程里那一套代码上，
// opsctl 只送一个目录路径过去。重跑一次即热重载：Install 会先
// Unload 旧的再装新的并通知前端刷新，所以 `<build> && opsctl ext dev <dir>` 就是
// 开发回路。桌面端每次安装都会在 opsctl 审批弹窗里请用户确认；同一目录、同一扩展、
// 能力不变的重装在桌面进程存活期间免于重复确认（见 internal/app/opsctl/ext_dev.go）。
//
// 目标数据目录就是 opsctl 的 --data-dir / OPSKAT_DATA_DIR，指向 make dev-sandbox
// 那个隔离目录即可（docs/VERIFICATION.md）；不另造一套沙箱路径推导。
func cmdExtDev(args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printExtDevUsage()
		if len(args) > 0 {
			return 0
		}
		return 1
	}
	if rejectExtraArgs(args[1:]) {
		return 1
	}

	sourceDir, err := filepath.Abs(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resolve %s: %v\n", args[0], err)
		return 1
	}
	// 桌面进程按它自己的工作目录解析路径，所以这里必须先绝对化；顺带确认目录里确实
	// 有 manifest.json，好过让用户等一次 socket 往返才知道路径打错了。
	if _, err := os.Stat(filepath.Join(sourceDir, "manifest.json")); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s is not an extension directory (no manifest.json)\n", sourceDir)
		return 1
	}

	name, version, err := devInstallFn(sourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Printf("Installed %s %s from %s into the running app\n", name, version, sourceDir)
	return 0
}

// requestExtDevInstall 让运行中的桌面进程装这个目录。
//
// 安装改的是桌面端正在运行的那一份（注册表、已加载的 WASM 模块、前端页面），
// opsctl 自己装一份就等于第二套安装路径。
func requestExtDevInstall(sourceDir string) (string, string, error) {
	resp, err := requestDesktop(approval.ApprovalRequest{
		Type: "ext_dev_install",
		Path: sourceDir,
	})
	if err != nil {
		return "", "", fmt.Errorf("%w — start it with `make dev-sandbox` (docs/VERIFICATION.md)", err)
	}
	if !resp.Approved {
		return "", "", fmt.Errorf("%s", resp.Reason)
	}
	return resp.Extension, resp.Version, nil
}

// requestDesktop sends one request to the desktop app running on opsctl's data
// dir; a failure to reach it names that data dir, the usual reason being an app
// running on another one (or none at all).
func requestDesktop(req approval.ApprovalRequest) (approval.ApprovalResponse, error) {
	dataDir := bootstrap.ResolvedDataDir()
	token, err := bootstrap.ReadAuthToken(dataDir)
	if err != nil {
		logger.Default().Warn("read auth token", zap.Error(err))
	}
	resp, err := approval.RequestApprovalWithToken(approval.SocketPath(dataDir), token, req)
	if err != nil {
		return approval.ApprovalResponse{}, fmt.Errorf("%w on data dir %s", err, dataDir)
	}
	return resp, nil
}

func printExtDevUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl ext dev <dir>

Installs an unpacked extension directory into the running desktop app and
reloads it in place — the same install the app's "install from directory"
button performs, so a dev build loads exactly like a shipped one.

Re-run it after each build; that is the hot-reload loop:

  make build-ext EXT=notebook && opsctl ext dev "$PWD/extensions/notebook/dist"

Point --data-dir (or OPSKAT_DATA_DIR) at the verification sandbox the app is
running on — see docs/VERIFICATION.md.

The app asks you to confirm each install — source directory, extension, its
declared capabilities, and whether it replaces an installed one. Re-running the
same directory for the same extension with unchanged capabilities reloads
without asking again until the app restarts.
`)
}

func printExtUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  opsctl ext <subcommand>

Subcommands:
  list     List installed extensions with their asset types, tools, and enabled state
  search   List the official store with each extension's status (no confirmation)
  install  Install an extension from the official store (the app asks to confirm)
  update   Update one extension, or --all, from the official store (the app asks to confirm)
  dev      Install an unpacked extension directory into the running app (development)

To run an extension tool, use exec against one of the extension's assets:

  opsctl exec <asset> -- <tool> --flag=value

That is the same command built-in asset types use; the asset's type selects the
extension, which opsctl loads and runs itself, whether or not the desktop app is
running. A disabled extension is refused. Run 'opsctl help <asset>' to see the
tool and flag reference.

Examples:
  opsctl ext list
  opsctl ext search elastic
  opsctl ext install es
  opsctl ext update --all
  opsctl ext dev "$PWD/extensions/notebook/dist"
  opsctl exec my-notes -- note_list
`)
}
