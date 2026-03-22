package main

import (
	"context"
	"embed"
	"log"

	"ops-cat/internal/repository/asset_repo"
	"ops-cat/internal/repository/group_repo"
	"ops-cat/internal/service/credential_svc"
	"ops-cat/migrations"

	"github.com/cago-frame/cago"
	"github.com/cago-frame/cago/configs"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/component"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	_ "ops-cat/internal/pkg/code"
	_ "github.com/cago-frame/cago/database/db/sqlite"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	ctx := context.Background()
	cfg, err := configs.NewConfig("ops-cat")
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 初始化 cago 组件（Registry 立即启动组件，不调用 Start 避免阻塞）
	cago.New(ctx, cfg).
		Registry(component.Core()).     // 日志
		Registry(component.Database()). // SQLite
		DisableLogger()

	// 初始化凭证加密服务（masterKey 后续可从配置/密钥链获取）
	credential_svc.SetDefault(credential_svc.New("ops-cat-default-master-key"))

	// 注册 Repository
	asset_repo.RegisterAsset(asset_repo.NewAsset())
	group_repo.RegisterGroup(group_repo.NewGroup())

	// 运行数据库迁移
	if err := migrations.RunMigrations(db.Default()); err != nil {
		log.Fatalf("数据库迁移失败: %v", err)
	}

	// 创建 Wails App
	app := NewApp()

	err = wails.Run(&options.App{
		Title:  "Ops Cat",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: app.startup,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatalf("Wails启动失败: %v", err)
	}
}
