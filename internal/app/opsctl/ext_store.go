package opsctl

import (
	"context"
	"fmt"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/service/extstore_svc"
	"github.com/opskat/opskat/pkg/extstore"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// ExtStore 把 `opsctl ext search / install / update` 交给桌面进程的扩展商店。
//
// 与 ExtDevInstaller 同一条理由——位置而非语义：已验签的索引、下载镜像、安装确认框
// 与扩展注册表都只在桌面进程里。安装走的就是设置 → 扩展 → 商店点「安装」跑的那一个
// extension.InstallFromStore，确认框也是同一个，所以这里不再叠一层 opsctl 审批弹窗。
type ExtStore interface {
	// ListStore 列出商店（显示文本按 lang），尚无已验签索引时先刷新。
	ListStore(ctx context.Context, lang string) ([]extstore_svc.Listing, error)
	// InstallFromStore 经应用内的安装确认安装 / 更新到商店提供的版本；ctx 结束即关闭确认框。
	InstallFromStore(ctx context.Context, name string) (installed, version string, err error)
}

// SetExtStore main.go 注入扩展商店。
func (o *Opsctl) SetExtStore(s ExtStore) { o.extStore = s }

// handleExtStoreSearch 处理 `opsctl ext search`：只读，不弹窗、不拉起窗口。
func (o *Opsctl) handleExtStoreSearch(ctx context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
	log := logger.Ctx(o.ctx).With(zap.String("lang", req.Lang))
	log.Info("opsctl extension store search requested")
	if o.extStore == nil {
		log.Warn("opsctl extension store search refused", zap.String("reason", "extension store not initialized"))
		return approval.ApprovalResponse{Approved: false, Reason: "extension store not initialized"}
	}
	listings, err := o.extStore.ListStore(ctx, req.Lang)
	if err != nil {
		log.Error("opsctl extension store search failed", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	entries := make([]approval.ExtStoreEntry, 0, len(listings))
	for _, l := range listings {
		entries = append(entries, storeEntry(l, req.Lang))
	}
	log.Info("opsctl extension store search completed", zap.Int("extensions", len(entries)))
	return approval.ApprovalResponse{Approved: true, StoreExtensions: entries}
}

// storeEntry 把一张商店卡片译成 opsctl 的线上格式。
func storeEntry(l extstore_svc.Listing, lang string) approval.ExtStoreEntry {
	e := approval.ExtStoreEntry{
		Name:        l.Name,
		DisplayName: l.DisplayName,
		Description: l.Description,
		Latest:      l.Latest,
		Installed:   l.InstalledVersion,
	}
	switch l.Action {
	case extstore.ActionInstall:
		e.Status = approval.ExtStoreStatusInstall
	case extstore.ActionUpdate:
		e.Status = approval.ExtStoreStatusUpdate
	case extstore.ActionInstalled:
		e.Status = approval.ExtStoreStatusInstalled
	case extstore.ActionUnavailable:
		e.Status = approval.ExtStoreStatusUnavailable
		e.Reason = unavailableReason(l.Unavailable, lang)
	}
	return e
}

// unavailableReason 说明这一版 OpsKat 缺什么；补救总是更新 OpsKat。
func unavailableReason(u *extstore_svc.Unavailable, lang string) string {
	switch u.Reason {
	case extstore_svc.ReasonHostABI:
		return fmt.Sprintf(i18n.Pick(lang, "需要 hostABI %s，请更新 OpsKat", "needs hostABI %s; update OpsKat"), u.HostABI)
	case extstore_svc.ReasonMinAppVersion:
		return fmt.Sprintf(i18n.Pick(lang, "需要 OpsKat %s 或更高版本", "needs OpsKat %s or newer"), u.MinAppVersion)
	default:
		return fmt.Sprintf(i18n.Pick(lang, "不支持来源类型 %s，请更新 OpsKat", "source type %s is not supported; update OpsKat"), u.SourceType)
	}
}

// handleExtStoreInstall 处理 `opsctl ext install / update` 的一次安装：先把窗口拉到
// 前台（确认框不会自己拉起窗口），再走商店安装——确认、下载、sha256 校验、安装。
// ctx 是这次请求：opsctl 退出即结束，确认框随之关闭。
func (o *Opsctl) handleExtStoreInstall(ctx context.Context, req approval.ApprovalRequest) approval.ApprovalResponse {
	log := logger.Ctx(o.ctx).With(zap.String("extension", req.Extension), zap.String("source", opsctlAuditSource))
	log.Info("opsctl extension store install requested")
	if o.extStore == nil {
		log.Warn("opsctl extension store install refused", zap.String("reason", "extension store not initialized"))
		return approval.ApprovalResponse{Approved: false, Reason: "extension store not initialized"}
	}
	if req.Extension == "" {
		log.Warn("opsctl extension store install refused", zap.String("reason", "no extension named"))
		return approval.ApprovalResponse{Approved: false, Reason: "no extension named"}
	}

	if o.window != nil {
		o.window.ActivateWindow()
	}
	res := extstore_svc.InstallOutcome(o.extStore.InstallFromStore(ctx, req.Extension))
	switch {
	case res.Canceled:
		log.Info("opsctl extension store install denied")
		return approval.ApprovalResponse{Approved: false, ErrorKind: approval.ExtStoreInstallCanceled,
			Reason: "the install was declined in the app"}
	case res.Error != nil && res.Error.Kind == extstore_svc.InstallErrInstalled:
		log.Info("opsctl extension store install skipped: already current")
		return approval.ApprovalResponse{Approved: false, ErrorKind: approval.ExtStoreInstallUpToDate, Reason: res.Error.Message}
	case res.Error != nil:
		log.Error("opsctl extension store install failed",
			zap.String("kind", string(res.Error.Kind)), zap.String("error", res.Error.Message))
		return approval.ApprovalResponse{Approved: false, ErrorKind: string(res.Error.Kind), Reason: res.Error.Message}
	}
	log.Info("opsctl extension store install completed", zap.String("version", res.Version))
	return approval.ApprovalResponse{Approved: true, Extension: res.Name, Version: res.Version}
}
