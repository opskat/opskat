package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/opskat/opskat/internal/app/ai"
	"github.com/opskat/opskat/internal/app/etcd"
	"github.com/opskat/opskat/internal/app/extension"
	"github.com/opskat/opskat/internal/app/external_edit"
	"github.com/opskat/opskat/internal/app/k8s"
	"github.com/opskat/opskat/internal/app/kafka"
	"github.com/opskat/opskat/internal/app/local"
	"github.com/opskat/opskat/internal/app/opsctl"
	"github.com/opskat/opskat/internal/app/oss"
	"github.com/opskat/opskat/internal/app/query"
	quitapp "github.com/opskat/opskat/internal/app/quit"
	"github.com/opskat/opskat/internal/app/rdp"
	"github.com/opskat/opskat/internal/app/redis"
	"github.com/opskat/opskat/internal/app/serial"
	"github.com/opskat/opskat/internal/app/ssh"
	"github.com/opskat/opskat/internal/app/sshadapt"
	"github.com/opskat/opskat/internal/app/system"
	"github.com/opskat/opskat/internal/app/vnc"

	aitool "github.com/opskat/opskat/internal/ai/tool"
	"github.com/opskat/opskat/internal/assetconn"
	_ "github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/pkg/portable"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/audit_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/service/external_edit_svc"
	"github.com/opskat/opskat/internal/service/localterm_svc"
	"github.com/opskat/opskat/internal/service/serial_svc"
	"github.com/opskat/opskat/internal/service/sftp_svc"
	"github.com/opskat/opskat/internal/service/snippet_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/service/vnc_svc"
	"github.com/opskat/opskat/internal/sshpool"
	extpkg "github.com/opskat/opskat/pkg/extension"
	skillplugin "github.com/opskat/opskat/plugin"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"go.uber.org/zap"
)

//go:embed all:frontend/dist
var assets embed.FS

const (
	defaultWindowWidth  = 1440
	defaultWindowHeight = 900
	minWindowWidth      = 1000
	minWindowHeight     = 640
)

// Lifecycle 是 binder 必须实现的生命周期接口（Wails 不会自动调用 bound struct 的 Startup/Cleanup，
// 由 main.go 的 OnStartup / OnShutdown 显式遍历调用）。
type Lifecycle interface {
	Startup(ctx context.Context)
	Cleanup()
}

// resolveBootstrap reads optional env overrides used by the GUI e2e harness and
// the interactive verification sandbox (OPSKAT_MASTER_KEY mirrors opsctl's env
// var; OPSKAT_DATA_DIR mirrors its --data-dir flag) and returns the resolved
// data dir, bootstrap options, and whether the single-instance lock must be
// disabled (so a verification instance does not collide with a running app).
//
// OPSKAT_E2E=1 marks the process as a verification run, and a verification run
// must never boot on the user's real data directory — that is where the real
// asset inventory, encrypted credentials, master.key and audit log live. The
// data dir is therefore checked here rather than left to convention, because a
// documented "back it up first" does not survive an agent driving the app.
func resolveBootstrap() (dataDir string, opts bootstrap.Options, disableSingleInstance bool, err error) {
	dataDir = bootstrap.AppDataDir()
	if env := os.Getenv("OPSKAT_DATA_DIR"); env != "" {
		dataDir = env
	}
	opts = bootstrap.Options{DataDir: dataDir, MasterKey: os.Getenv("OPSKAT_MASTER_KEY")}
	disableSingleInstance = os.Getenv("OPSKAT_E2E") == "1"
	if disableSingleInstance {
		// 字符串相等挡住「未覆盖」与「原样填入」，SameDir 再用文件系统身份挡住
		// 尾斜杠 / . / .. / symlink / Windows 大小写这些等价写法。
		if dataDir == bootstrap.AppDataDir() || portable.SameDir(dataDir, bootstrap.AppDataDir()) {
			return "", bootstrap.Options{}, false, fmt.Errorf(
				"OPSKAT_E2E=1 标记这是一次验证运行，但 OPSKAT_DATA_DIR 指向真实数据目录 %q；"+
					"请把 OPSKAT_DATA_DIR 指向隔离目录（见 docs/VERIFICATION.md）", dataDir)
		}
	}
	return dataDir, opts, disableSingleInstance, nil
}

// singleInstanceBaseID 是非便携安装的单实例锁 id，历史值不可改：改了会让
// 升级前后的两个版本互不感知，同时开出两个窗口。
const singleInstanceBaseID = "com.opskat.desktop"

// singleInstanceID 返回单实例锁 id：便携模式下把便携目录路径哈希进去，
// 使便携版与已安装版、以及两个不同的便携目录互不抢锁。
//
// 否则常量 id 会让"已装了 OpsKat 的用户双击便携版 opskat.exe"变成静默失败：
// bootstrap.Init 先跑完（便携目录里日志、master.key、opskat.db 都已落盘），
// 随后 Wails 发现同名 mutex 已存在，把已安装的窗口弹到前台并 os.Exit(0)——
// 便携版没有窗口也没有报错。
//
// 取 sha256 前 4 字节（8 个十六进制字符）：够短，且只含 [0-9a-f]，
// 对 Windows 命名 mutex 与 Linux 侧由该 id 派生的 dbus 名都是安全字符。
func singleInstanceID(portableRoot string) string {
	if portableRoot == "" {
		return singleInstanceBaseID
	}
	sum := sha256.Sum256([]byte(portableRoot))
	return fmt.Sprintf("%s.%x", singleInstanceBaseID, sum[:4])
}

func main() {
	ctx := context.Background()

	// 初始化数据库、凭证、Repository、迁移（e2e 可经 env 覆盖数据目录/master key）
	dataDir, bootstrapOpts, disableSingleInstance, err := resolveBootstrap()
	if err != nil {
		log.Fatalf("启动参数无效: %v", err)
	}
	if err := bootstrap.Init(ctx, bootstrapOpts); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	// 加载应用配置
	if _, err := bootstrap.LoadConfig(dataDir); err != nil {
		log.Printf("加载配置失败: %v", err)
	}
	windowWidth, windowHeight := initialWindowSize(bootstrap.GetConfig())

	// 把持久化的 SSH/TCP 连接调优注入全局，使首个连接即采用用户配置。
	system.ApplySSHTuning(bootstrap.GetConfig())

	// 初始化日志（读取 DebugMode 配置决定 level；桌面应用需要文件日志）
	if err := bootstrap.InitLogger(); err != nil {
		log.Fatalf("初始化日志失败: %v", err)
	}

	// appCtx 在所有 binder 之间共享：cancel 后 wait loop 退出
	appCtx, cancelApp := context.WithCancel(ctx)

	authToken, err := bootstrap.GenerateAuthToken(dataDir)
	if err != nil {
		log.Printf("Failed to generate auth token: %v", err)
	}

	// 1. 共享基础设施
	sshMgr := ssh_svc.NewManager()
	sftpSvc := sftp_svc.NewService(sshMgr)
	// external edit 复用 sftp 通道读写远程文件，由 service 层把"全文读取阈值"
	// 通过 provider 反向注入给 sftp_svc：超过阈值的远程文件由 sftp 主动截断报错。
	sftpSvc.SetMaxReadFileSizeProvider(func() int64 {
		return external_edit_svc.MaxReadFileSizeBytesForConfig(bootstrap.GetConfig())
	})
	serialMgr := serial_svc.NewManager()
	localMgr := localterm_svc.NewManager()
	vncMgr := vnc_svc.NewManager(asset_repo.Asset())
	// serial / vnc 的管理器由 main 持有（binder 只拿到引用），所以在这里登记
	// 「资产被删除时断开它的会话」；ssh/rdp/kafka/query 等在各自 binder 里登记。
	assetconn.Register("serial", func(_ context.Context, assetID int64) error {
		serialMgr.CloseAsset(assetID)
		return nil
	})
	assetconn.Register("vnc", func(_ context.Context, assetID int64) error {
		vncMgr.CloseAsset(assetID)
		return nil
	})
	poolDialer := &sshadapt.PoolDialer{}
	pool := sshpool.NewPool(poolDialer, 5*time.Minute)
	proxyServer := sshpool.NewServer(pool, authToken)

	skillContent := system.SkillContent{
		SkillMD:               skillplugin.SkillMD,
		CommandsMD:            skillplugin.CommandsMD,
		InitMD:                skillplugin.InitMD,
		PluginJSON:            skillplugin.PluginJSON,
		MarketplaceJSON:       skillplugin.MarketplaceJSON,
		PluginMarketplaceJSON: skillplugin.PluginMarketplaceJSON,
	}

	// 2. 构造 binder（system 先建，其它持有它做 LangProvider/WindowActivator）
	sys := system.New(appCtx, skillContent)
	sshB := ssh.New(appCtx, sys, sshMgr, sftpSvc, pool)
	queryB := query.New(appCtx, sys, pool)
	redisB := redis.New(appCtx, sys, pool)
	rdpB := rdp.New(sys, pool)
	etcdB := etcd.New(appCtx, sys, pool)
	ossB := oss.New(appCtx, sys)
	kafkaB := kafka.New(appCtx, sys, pool)
	k8sB := k8s.New(appCtx, sys, pool)
	serialB := serial.New(appCtx, sys, serialMgr)
	localB := local.New(appCtx, sys, localMgr)
	vncB := vnc.New(appCtx, vncMgr)
	aiB := ai.New(appCtx, sys, pool)
	opsctlB := opsctl.New(appCtx, sys, sys, proxyServer)
	opsctlB.SetAuthToken(authToken)
	extB := extension.New(appCtx, sys, pool)
	externalEditEmitter := external_edit.NewEventEmitter()
	externalEditSvc, err := external_edit_svc.NewService(external_edit_svc.Options{
		DataDir:        bootstrap.AppDataDir(),
		ConfigProvider: bootstrap.GetConfig,
		ConfigSaver:    bootstrap.SaveConfig,
		Remote:         sftpSvc,
		FindSessions:   sshMgr.ListActiveSessionIDsByAsset,
		Assets:         asset_repo.Asset(),
		Audit:          audit_repo.Audit(),
		Emit:           externalEditEmitter.Emit,
	})
	if err != nil {
		logger.Default().Warn("init external edit service", zap.Error(err))
	}
	extEditB := external_edit.New(sys, externalEditSvc, externalEditEmitter)

	// 3. 注入跨 binder 依赖
	aiB.SetKafkaService(kafkaB.Service())
	aiB.SetSerialManager(serialMgr)
	aiB.SetWindowActivator(sys)

	binders := []Lifecycle{sys, sshB, queryB, redisB, rdpB, etcdB, kafkaB, k8sB, serialB, localB, vncB, aiB, opsctlB, extB, extEditB, ossB}
	var forceQuit atomic.Bool
	sys.SetConfirmQuitHandler(func() { forceQuit.Store(true) })

	appOptions := &options.App{
		Title:     "OpsKat",
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		Frameless: runtime.GOOS == "windows",
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: opsctl.NewExtensionAssetHandler(filepath.Join(dataDir, "extensions"), nil),
		},
		OnStartup: func(wctx context.Context) {
			wailsRuntime.WindowCenter(wctx)
			for _, b := range binders {
				b.Startup(wctx)
			}

			// AI provider 之后才能注入 extension service：extension 异步 init 完成后会调用回调
			initExtensionSystem(wctx, appCtx, dataDir, pool, extB, aiB, opsctlB)
		},
		// OnBeforeClose 在窗口真正关闭前触发：emit ai:flush-all 让前端落盘所有活跃会话。
		OnBeforeClose: func(wctx context.Context) bool {
			saveWindowSize(wctx)
			if forceQuit.Load() {
				return false
			}
			var sessions []quitapp.Session
			for _, item := range sshMgr.ActiveSessionDetails() {
				sessions = append(sessions, quitapp.Session{Kind: "terminal", SessionID: item.SessionID, AssetID: item.AssetID, StartedAt: item.StartedAt})
			}
			for _, item := range localMgr.ActiveSessions() {
				sessions = append(sessions, quitapp.Session{Kind: "terminal", SessionID: item.SessionID, AssetID: item.AssetID, StartedAt: item.StartedAt})
			}
			for _, item := range serialMgr.ActiveSessions() {
				sessions = append(sessions, quitapp.Session{Kind: "terminal", SessionID: item.SessionID, AssetID: item.AssetID, StartedAt: item.StartedAt})
			}
			for _, item := range rdp.ActiveSessions(rdpB) {
				sessions = append(sessions, quitapp.Session{Kind: "rdp", SessionID: item.SessionID, AssetID: item.AssetID, StartedAt: item.StartedAt})
			}
			for _, item := range vncMgr.ActiveSessions() {
				sessions = append(sessions, quitapp.Session{Kind: "vnc", SessionID: item.SessionID, AssetID: item.AssetID, StartedAt: item.StartedAt})
			}
			activities := quitapp.BuildSessionActivities(wctx, sessions, asset_repo.Asset().Find)
			for _, convID := range ai.ActiveTasks(aiB) {
				activities = append(activities, quitapp.Activity{Kind: "ai", Category: "running", RefID: convID})
			}
			for _, taskKind := range opsctl.ActiveTasks(opsctlB) {
				activities = append(activities, quitapp.Activity{Kind: "opsctl", Category: "running", Detail: taskKind})
			}
			if len(activities) > 0 {
				wailsRuntime.EventsEmit(wctx, "app:quit-confirm", map[string]any{"activities": activities})
				return true
			}
			aiB.DrainAIFlushAck()
			wailsRuntime.EventsEmit(wctx, "ai:flush-all")
			select {
			case <-aiB.WaitAIFlushAck():
			case <-time.After(2 * time.Second):
			}
			return false
		},
		OnShutdown: func(_ context.Context) {
			cancelApp() // 解除所有 wait loop
			// socket Stop 只做监听器/连接断开且不等待，先同步执行以保证在返回
			// Wails 前已向所有 opsctl 客户端广播关闭。
			opsctlB.Cleanup()
			// 用户已经确认退出后不再等待任何资源自然收尾。各 Cleanup 只负责
			// 广播取消和主动断开；放到独立 goroutine，避免异常远端或第三方库
			// 的 Close 阻塞 Wails 的进程退出。
			for i := len(binders) - 1; i >= 0; i-- {
				if binders[i] == opsctlB {
					continue
				}
				binder := binders[i]
				go binder.Cleanup()
			}
			go pool.Close()
		},
		Bind: []interface{}{
			sys, sshB, queryB, redisB, rdpB, etcdB, kafkaB, k8sB, serialB, localB, vncB, aiB, opsctlB, extB, extEditB, ossB,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
		},
	}
	// 便携模式下把 WebView2 的用户数据目录也收进便携目录：其中的 localStorage
	// 存着真实用户内容（SQL 编辑器文本、etcd 命令历史、打开的标签页），Wails 默认
	// 会写到 %APPDATA%\opskat.exe，既污染宿主机器（便携版承诺"不留痕迹"），
	// UI 状态也无法随文件夹迁移。只在便携模式下设置：无条件设置会让已安装用户的
	// WebView2 数据换一次位置，标签页/布局/主题被一次性清空。
	if portableRoot := portable.Dir(); portableRoot != "" {
		appOptions.Windows = &windows.Options{
			WebviewUserDataPath: filepath.Join(portableRoot, "webview2"),
			// Wails 只在 Windows 选项非 nil 时才下发缩放设置，且直接取字段值；
			// 保持 nil 时 WebView2 用自己的默认值（缩放开启）。这里显式置 true，
			// 否则零值 false 会把 Ctrl+滚轮缩放只在便携版上关掉。
			IsZoomControlEnabled: true,
		}
	}

	if !disableSingleInstance {
		appOptions.SingleInstanceLock = &options.SingleInstanceLock{
			UniqueId: singleInstanceID(portable.Dir()),
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				sys.OnSecondInstanceLaunch()
			},
		}
	}

	err = wails.Run(appOptions)
	if err != nil {
		log.Fatalf("Wails启动失败: %v", err)
	}
}

// initExtensionSystem 复刻原 App.Startup 里扩展系统的初始化路径：可通过 OPSKAT_EXTENSIONS=0 禁用。
func initExtensionSystem(
	wctx context.Context,
	appCtx context.Context,
	dataDir string,
	pool *sshpool.Pool,
	extB *extension.Extension,
	aiB *ai.AI,
	opsctlB *opsctl.Opsctl,
) {
	if os.Getenv("OPSKAT_EXTENSIONS") == "0" {
		logger.Default().Info("extension system disabled via OPSKAT_EXTENSIONS=0")
		return
	}

	extDir := filepath.Join(dataDir, "extensions")
	mgr := extpkg.NewManager(extDir, func(extName string) extpkg.HostProvider {
		return extpkg.NewDefaultHostProvider(extpkg.DefaultHostConfig{
			Logger:       logger.Default(),
			AssetConfigs: extB.NewAssetConfigGetter(),
			FileDialogs:  extB.NewFileDialogOpener(),
			KV:           extB.NewKVStore(extName),
			ActionEvents: extB.NewActionEventHandler(extName),
			TunnelDialer: extB.NewTunnelDialer(),
		})
	}, logger.Default())

	extSvc := extension_svc.New(
		mgr,
		extension_state_repo.ExtensionState(),
		extension_data_repo.ExtensionData(),
		asset_repo.Asset(),
		logger.Default(),
		func() { wailsRuntime.EventsEmit(wctx, "ext:reload", nil) },
		extension.SnippetExtensionHook{},
	)

	extB.SetService(extSvc)
	aiB.SetExtensionService(extSvc)
	opsctlB.SetExtToolExecutor(desktopExecExecutor{})
	opsctlB.SetExtDevInstaller(desktopExtDevInstaller{ext: extB})

	// 接入 snippet 分类注册表
	if svc := snippet_svc.Snippet(); svc != nil {
		svc.Registry().SetExtensionProvider(snippet_svc.ExtensionCategoryProviderFunc(func() []snippet_svc.ExtensionCategory {
			return extension.CollectExtensionCategories(mgr)
		}))
	}

	// 异步初始化扩展，避免阻塞 Startup（WASM 编译较慢）
	go func() {
		if err := extSvc.Init(appCtx); err != nil {
			logger.Default().Error("extension init failed", zap.Error(err))
		}
		// 扩展 Init 完成后刷新 snippet 分类表
		if svc := snippet_svc.Snippet(); svc != nil {
			svc.RefreshCategories()
		}
		wailsRuntime.EventsEmit(wctx, "ext:ready", nil)
	}()
}

// desktopExecExecutor 把统一 exec handler 交给 opsctl binder：opsctl 对扩展资产的命令
// 由桌面进程执行（WASM 运行时只在这里），走的仍然是同一个 handler。
type desktopExecExecutor struct{}

func (desktopExecExecutor) ExecuteExtTool(ctx context.Context, assetID int64, command string) (string, error) {
	return aitool.ExecOnAsset(ctx, assetID, command)
}

// desktopExtDevInstaller 把 `opsctl ext dev` 的安装请求交回扩展 binder，跑的是
// "从目录安装"那一条 extension_svc.Install。
type desktopExtDevInstaller struct{ ext *extension.Extension }

func (i desktopExtDevInstaller) InstallExtensionDir(ctx context.Context, sourceDir string) (string, string, error) {
	return extension.InstallExtensionDir(i.ext, ctx, sourceDir)
}

func initialWindowSize(cfg *bootstrap.AppConfig) (int, int) {
	width := defaultWindowWidth
	height := defaultWindowHeight
	if cfg != nil {
		if cfg.WindowWidth >= minWindowWidth {
			width = cfg.WindowWidth
		}
		if cfg.WindowHeight >= minWindowHeight {
			height = cfg.WindowHeight
		}
	}
	return width, height
}

func saveWindowSize(ctx context.Context) {
	if !wailsRuntime.WindowIsNormal(ctx) {
		return
	}

	width, height := wailsRuntime.WindowGetSize(ctx)
	if width < minWindowWidth || height < minWindowHeight {
		return
	}

	cfg := bootstrap.GetConfig()
	if cfg == nil {
		return
	}

	cfg.WindowWidth = width
	cfg.WindowHeight = height
	if err := bootstrap.SaveConfig(cfg); err != nil {
		log.Printf("保存窗口大小失败: %v", err)
	}
}
