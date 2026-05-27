package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/opskat/opskat/internal/app/ai"
	"github.com/opskat/opskat/internal/app/etcd"
	"github.com/opskat/opskat/internal/app/extension"
	"github.com/opskat/opskat/internal/app/external_edit"
	"github.com/opskat/opskat/internal/app/k8s"
	"github.com/opskat/opskat/internal/app/kafka"
	"github.com/opskat/opskat/internal/app/opsctl"
	"github.com/opskat/opskat/internal/app/query"
	"github.com/opskat/opskat/internal/app/redis"
	"github.com/opskat/opskat/internal/app/serial"
	"github.com/opskat/opskat/internal/app/ssh"
	"github.com/opskat/opskat/internal/app/sshadapt"
	"github.com/opskat/opskat/internal/app/system"

	aitool "github.com/opskat/opskat/internal/ai/tool"
	_ "github.com/opskat/opskat/internal/assettype"
	"github.com/opskat/opskat/internal/bootstrap"
	"github.com/opskat/opskat/internal/repository/asset_repo"
	"github.com/opskat/opskat/internal/repository/extension_data_repo"
	"github.com/opskat/opskat/internal/repository/extension_state_repo"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/internal/service/external_edit_svc"
	"github.com/opskat/opskat/internal/service/serial_svc"
	"github.com/opskat/opskat/internal/service/sftp_svc"
	"github.com/opskat/opskat/internal/service/snippet_svc"
	"github.com/opskat/opskat/internal/service/ssh_svc"
	"github.com/opskat/opskat/internal/sshpool"
	extpkg "github.com/opskat/opskat/pkg/extension"
	skillplugin "github.com/opskat/opskat/plugin"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
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

func main() {
	ctx := context.Background()

	// 初始化数据库、凭证、Repository、迁移
	dataDir := bootstrap.AppDataDir()
	if err := bootstrap.Init(ctx, bootstrap.Options{}); err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	// 加载应用配置
	if _, err := bootstrap.LoadConfig(dataDir); err != nil {
		log.Printf("加载配置失败: %v", err)
	}
	windowWidth, windowHeight := initialWindowSize(bootstrap.GetConfig())

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
	etcdB := etcd.New(appCtx, sys, pool)
	kafkaB := kafka.New(appCtx, sys, pool)
	k8sB := k8s.New(appCtx, sys, pool)
	serialB := serial.New(appCtx, sys, serialMgr)
	aiB := ai.New(appCtx, sys, pool)
	opsctlB := opsctl.New(appCtx, sys, sys, proxyServer)
	opsctlB.SetAuthToken(authToken)
	extB := extension.New(appCtx, sys, pool)
	extEditB := external_edit.New(appCtx, sys, sftpSvc, sshMgr)

	// 3. 注入跨 binder 依赖
	aiB.SetKafkaService(kafkaB.Service())
	aiB.SetSerialManager(serialMgr)
	aiB.SetWindowActivator(sys)

	binders := []Lifecycle{sys, sshB, queryB, redisB, etcdB, kafkaB, k8sB, serialB, aiB, opsctlB, extB, extEditB}

	err = wails.Run(&options.App{
		Title:     "OpsKat",
		Width:     windowWidth,
		Height:    windowHeight,
		MinWidth:  minWindowWidth,
		MinHeight: minWindowHeight,
		Frameless: runtime.GOOS == "windows",
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: opsctl.NewExtensionAssetHandler(filepath.Join(bootstrap.AppDataDir(), "extensions"), nil),
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
			for i := len(binders) - 1; i >= 0; i-- {
				binders[i].Cleanup()
			}
			pool.Close()
		},
		Bind: []interface{}{
			sys, sshB, queryB, redisB, etcdB, kafkaB, k8sB, serialB, aiB, opsctlB, extB, extEditB,
		},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "com.opskat.desktop",
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				sys.OnSecondInstanceLaunch()
			},
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
		},
	})
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
		zap.L().Info("extension system disabled via OPSKAT_EXTENSIONS=0")
		return
	}

	extDir := filepath.Join(dataDir, "extensions")
	mgr := extpkg.NewManager(extDir, func(extName string) extpkg.HostProvider {
		return extpkg.NewDefaultHostProvider(extpkg.DefaultHostConfig{
			Logger:       zap.L(),
			AssetConfigs: extB.NewAssetConfigGetter(),
			FileDialogs:  extB.NewFileDialogOpener(),
			KV:           extB.NewKVStore(extName),
			ActionEvents: extB.NewActionEventHandler(extName),
			TunnelDialer: extB.NewTunnelDialer(),
		})
	}, zap.L())

	extSvc := extension_svc.New(
		mgr,
		extension_state_repo.ExtensionState(),
		extension_data_repo.ExtensionData(),
		asset_repo.Asset(),
		zap.L(),
		func(b *extpkg.Bridge) { aitool.SetExecToolExecutor(b) },
		func() { wailsRuntime.EventsEmit(wctx, "ext:reload", nil) },
		extension.SnippetExtensionHook{},
	)

	extB.SetService(extSvc)
	aiB.SetExtensionService(extSvc)
	opsctlB.SetExtToolExecutor(&bridgeExtExecutor{bridge: extSvc.Bridge})

	// 接入 snippet 分类注册表
	if svc := snippet_svc.Snippet(); svc != nil {
		svc.Registry().SetExtensionProvider(snippet_svc.ExtensionCategoryProviderFunc(func() []snippet_svc.ExtensionCategory {
			return extension.CollectExtensionCategories(mgr)
		}))
	}

	// 异步初始化扩展，避免阻塞 Startup（WASM 编译较慢）
	go func() {
		if err := extSvc.Init(appCtx); err != nil {
			zap.L().Error("extension init failed", zap.Error(err))
		}
		// 扩展 Init 完成后刷新 snippet 分类表
		if svc := snippet_svc.Snippet(); svc != nil {
			svc.RefreshCategories()
		}
		wailsRuntime.EventsEmit(wctx, "ext:ready", nil)

		if err := extSvc.StartWatch(appCtx); err != nil {
			zap.L().Warn("extension watcher failed", zap.Error(err))
		}
	}()
}

// bridgeExtExecutor 把 extension_svc.Service.Bridge() 包装成 opsctl.ExtToolExecutor。
type bridgeExtExecutor struct {
	bridge func() *extpkg.Bridge
}

func (b *bridgeExtExecutor) ExecuteExtTool(ctx context.Context, extName, tool string, args []byte) ([]byte, error) {
	br := b.bridge()
	if br == nil {
		return nil, errExtNotInit
	}
	ext := br.FindExtensionByTool(extName, tool)
	if ext == nil || ext.Plugin == nil {
		return nil, errExtToolNotFound
	}
	return ext.Plugin.CallTool(ctx, tool, args)
}

var (
	errExtNotInit      = errExt("extension system not initialized")
	errExtToolNotFound = errExt("extension tool not found")
)

type errExt string

func (e errExt) Error() string { return string(e) }

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
