package opsctl

import (
	"os"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// handleExtDevInstall 处理 `opsctl ext dev <dir>`：把一个未打包的扩展目录装进
// 当前进程。
//
// 这条通道存在的意义是让扩展开发者不必再跑第二套宿主（原 cmd/devserver）：安装走的
// 就是 ExtDevInstaller 背后那一个 extension_svc.Install，与用户在扩展页点"从目录
// 安装"逐字相同，装完的扩展由同一个 WASM 运行时、同一套能力面、同一份注册表承载。
// 重装即热重载——Install 会先 Unload 再加载并通知前端刷新。
func (o *Opsctl) handleExtDevInstall(req approval.ApprovalRequest) approval.ApprovalResponse {
	log := logger.Ctx(o.ctx).With(zap.String("source", req.Path))

	// 生产环境不开这条通道。它装的是未经审阅的 WASM，manifest 要什么能力就有什么能力；
	// 门禁必须落在干活的这一侧——socket 一旦可达，客户端那道检查什么也拦不住。
	if os.Getenv("OPSKAT_ENV") == "production" {
		log.Warn("extension dev install refused in production")
		return approval.ApprovalResponse{
			Approved: false,
			Reason:   "extension dev install is refused when OPSKAT_ENV=production",
		}
	}
	if o.extDevInstaller == nil {
		return approval.ApprovalResponse{Approved: false, Reason: "extension system not initialized"}
	}
	if req.Path == "" {
		return approval.ApprovalResponse{Approved: false, Reason: "extension source directory is required"}
	}

	log.Info("extension dev install started")
	name, version, err := o.extDevInstaller.InstallExtensionDir(i18n.Ctx(o.ctx, o.lang.Lang()), req.Path)
	if err != nil {
		log.Error("extension dev install failed", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	log.Info("extension dev install completed",
		zap.String("extension", name), zap.String("version", version))

	return approval.ApprovalResponse{Approved: true, Extension: name, Version: version}
}
