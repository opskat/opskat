package opsctl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/service/extension_svc"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extmanifest"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// extDevApprovalType 是 ext dev 安装在 opsctl 审批弹窗里的类型；ApprovalKindFor 对它
// 给出 once——安装只能逐次确认，不能落成常驻授权。
const extDevApprovalType = "ext_dev_install"

// currentApp is the app version the compatibility rule is judged against; a
// variable so tests can stand in other builds.
var currentApp = appversion.Current

// handleExtDevInstall 处理 `opsctl ext dev <dir>`：经用户在桌面端确认后，把一个
// 未打包的扩展目录装进当前进程。
//
// 这条通道让扩展开发不需要第二套宿主：安装走的就是 ExtDevInstaller 背后那一个
// extension_svc.Install——扩展页"从目录安装"用的同一条暂存 → 确认 → 提交路径，装完的
// 扩展由同一个 WASM 运行时、同一套能力面、同一份注册表承载。确认也是同一份：Install
// 描述的 InstallConfirm（已装版本、权限、新增项）交给这里的确认函数，只是展示在 opsctl
// 审批弹窗里而不是应用内的安装确认框；提交时 Install 同样复查已装版本仍是确认时的
// From，弹窗开着时有别的安装落地就拒绝。重装即热重载——Install 会先 Unload 再加载
// 并通知前端刷新。
//
// 它装进来并启用的是未经审阅的 WASM，manifest 要什么能力就有什么能力，还能覆盖同名的
// 已装扩展；而任何持有 socket token 的本地进程（包括经本地 shell 跑 opsctl 的 AI）
// 都能发这条请求。所以这里不看环境变量之类发行包里根本不存在的开关，而是每次都让
// 用户在桌面端的 opsctl 审批弹窗里确认：来源目录、扩展名/版本、声明的能力、会不会
// 覆盖已装扩展。为了让"构建后重跑即热重载"的回路可用，同一绝对目录 + 同一扩展名 +
// 同一组能力的确认只在本进程内存里记住；换目录、换扩展、能力变了都重新询问。
func (o *Opsctl) handleExtDevInstall(req approval.ApprovalRequest) approval.ApprovalResponse {
	log := logger.Ctx(o.ctx).With(zap.String("source", req.Path))
	log.Info("extension dev install requested")

	if o.extDevInstaller == nil {
		log.Warn("extension dev install refused", zap.String("reason", "extension system not initialized"))
		return approval.ApprovalResponse{Approved: false, Reason: "extension system not initialized"}
	}
	// opsctl 已把路径绝对化；相对路径会按桌面进程自己的工作目录解析，装到别处去。
	if req.Path == "" || !filepath.IsAbs(req.Path) {
		log.Warn("extension dev install refused", zap.String("reason", "source directory must be an absolute path"))
		return approval.ApprovalResponse{Approved: false, Reason: "extension source directory must be an absolute path"}
	}
	sourceDir := filepath.Clean(req.Path)

	// 先拍一份快照再给用户看：确认的是快照里的 manifest，装的也是这份快照。否则弹窗
	// 开着的时候改写源目录（比如加上 credentials:read），装进去的就不是用户批的那个。
	staged, err := os.MkdirTemp("", "opskat-ext-dev-*")
	if err != nil {
		log.Error("extension dev install failed", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: fmt.Sprintf("stage extension: %v", err)}
	}
	defer func() {
		if err := os.RemoveAll(staged); err != nil {
			log.Warn("remove staged extension dir", zap.String("dir", staged), zap.Error(err))
		}
	}()
	if err := os.CopyFS(staged, os.DirFS(sourceDir)); err != nil {
		log.Warn("extension dev install refused", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: fmt.Sprintf("read extension directory: %v", err)}
	}
	data, err := os.ReadFile(filepath.Join(staged, "manifest.json")) //nolint:gosec // our own staging copy
	if err != nil {
		log.Warn("extension dev install refused", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: fmt.Sprintf("read manifest: %v", err)}
	}
	// 同一条兼容规则：不兼容的构建在弹窗之前就拒绝，并说明需要更新 OpsKat。
	if err := extmanifest.CheckManifestJSON(data, currentApp()); err != nil {
		log.Warn("extension dev install refused", zap.Error(err))
		var inc *extmanifest.IncompatibleError
		if errors.As(err, &inc) {
			return approval.ApprovalResponse{Approved: false, Reason: extension_svc.IncompatibleMessage(i18n.Ctx(o.ctx, o.lang.Lang()), inc)}
		}
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	manifest, err := extension.ParseManifest(data)
	if err != nil {
		log.Warn("extension dev install refused", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}

	log = log.With(
		zap.String("extension", manifest.Name),
		zap.String("version", manifest.Version),
		zap.String("credentials", manifest.Permissions.Credentials),
		zap.Bool("allowPrivate", manifest.Permissions.Network.AllowPrivate),
		zap.Bool("assetEndpoint", manifest.Permissions.Network.AssetEndpoint),
	)

	// The approval is Install's confirm: it sees the InstallConfirm the service
	// built for the staged snapshot, and Install commits only what it approved.
	var denied *approval.ApprovalResponse
	confirm := func(_ context.Context, c extension_svc.InstallConfirm) error {
		log := log.With(zap.Bool("overwrites", c.From != ""), zap.String("installedVersion", c.From))
		key := extDevApprovalKey{dir: sourceDir, name: c.Name, grants: grantsFingerprint(c.Capabilities)}
		if o.extDevApprovals.has(key) {
			log.Info("extension dev install approved", zap.String("decision", "remembered"))
			return nil
		}
		resp := o.extDevApprove(approval.ApprovalRequest{
			Type:    extDevApprovalType,
			Command: sourceDir,
			Detail:  extDevApprovalDetail(c, o.lang.Lang()),
		})
		if !resp.Approved {
			log.Info("extension dev install denied", zap.String("reason", resp.Reason))
			denied = &resp
			return extension_svc.ErrInstallCanceled
		}
		o.extDevApprovals.remember(key)
		log.Info("extension dev install approved", zap.String("decision", "user"))
		return nil
	}

	name, version, err := o.extDevInstaller.InstallExtensionDir(i18n.Ctx(o.ctx, o.lang.Lang()), staged, confirm)
	if denied != nil {
		return approval.ApprovalResponse{Approved: false, Reason: "extension dev install " + denied.Reason}
	}
	if err != nil {
		log.Error("extension dev install failed", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	log.Info("extension dev install completed")

	return approval.ApprovalResponse{Approved: true, Extension: name, Version: version}
}

// extDevApprovalDetail 是弹窗里除来源目录之外的全部内容：用户据此判断要不要放行。
// credentials: read 会把扩展资产里保存的每个密码以明文交给它，所以放在第一行醒目提示，
// 而不是混在权限列表里。全部内容取自 extension_svc 描述这次安装的 c：From 非空即覆盖
// 已装版本，c.Added（全新安装时为空）里的项标为新增。
func extDevApprovalDetail(c extension_svc.InstallConfirm, lang string) string {
	var b strings.Builder
	grants := c.Capabilities
	if slices.ContainsFunc(grants, func(g extension_svc.CapabilityGrant) bool {
		return g.Kind == extension_svc.CapCredentials && g.Value == extmanifest.CredentialAccessRead
	}) {
		b.WriteString(i18n.Pick(lang,
			"警告：credentials: read —— 该扩展能以明文读取其资产里保存的全部密码\n",
			"WARNING: credentials: read — this extension can read every password stored in its assets in plaintext\n"))
	}
	if slices.ContainsFunc(grants, func(g extension_svc.CapabilityGrant) bool { return g.Kind == extension_svc.CapDatabaseDriver }) {
		b.WriteString(i18n.Pick(lang,
			"警告：databaseDrivers —— 使用这些驱动的数据库资产，其口令与 TLS 客户端私钥会以明文交给该扩展\n",
			"WARNING: databaseDrivers — database assets using these drivers hand their passwords and TLS client private keys to this extension in plaintext\n"))
	}
	fmt.Fprintf(&b, "extension: %s %s\n", c.Name, c.To)
	if c.From != "" {
		fmt.Fprintf(&b, "overwrites installed: %s %s\n", c.Name, c.From)
	} else {
		b.WriteString("new install\n")
	}
	added := c.Added
	newMark := i18n.Pick(lang, "新增", "new")
	riskMark := i18n.Pick(lang, "高风险", "HIGH RISK")
	b.WriteString("permissions:")
	lines := 0
	// One line per kind, in the order CapabilityGrants lists them (the desktop
	// dialog's order); a kind's values share the line.
	for i := 0; i < len(grants); {
		kind := grants[i].Kind
		var values []string
		highRisk := false
		for ; i < len(grants) && grants[i].Kind == kind; i++ {
			g := grants[i]
			v := g.Value
			if v == "" {
				v = "true"
			}
			if slices.Contains(added, g) {
				v += " (" + newMark + ")"
			}
			values = append(values, v)
			highRisk = highRisk || g.HighRisk
		}
		line := strings.Join(values, ", ")
		if highRisk {
			line += " [" + riskMark + "]"
		}
		line += extDevGrantExplanation(kind, lang)
		fmt.Fprintf(&b, "\n  %s: %s", extDevGrantLabel(kind), line)
		lines++
	}
	if lines == 0 {
		b.WriteString(" none")
	}
	return b.String()
}

// extDevGrantLabel 是授权类别在 manifest 里的写法，ext dev 文案按它列出。授权类别本身就按
// manifest 的写法命名，只有驱动 id 一项一个授权、键名是复数。
func extDevGrantLabel(kind string) string {
	if kind == extension_svc.CapDatabaseDriver {
		return "databaseDrivers"
	}
	return kind
}

// extDevGrantExplanation 说明那些光看键名不懂含义的高风险权限。
func extDevGrantExplanation(kind, lang string) string {
	switch kind {
	case extension_svc.CapAssetRoute:
		return i18n.Pick(lang,
			" —— 带资产的调用可以经该资产的连接路径连到任意地址，不只是资产里填写的地址",
			" — a call on an asset may dial any address along that asset's connection path, not just the address typed into the asset")
	case extension_svc.CapKerberos:
		return i18n.Pick(lang,
			" —— 可以用资产里保存的 Kerberos 凭据申请票据",
			" — may fetch Kerberos tickets with the Kerberos credentials stored in the asset")
	}
	return ""
}

// extDevApprovalKey 是一次 ext dev 确认覆盖的范围。权限与驱动 id 也在键里：重建后
// 要的更多是一个新的决定，不是一次重载。
type extDevApprovalKey struct {
	dir    string
	name   string
	grants string
}

func grantsFingerprint(grants []extension_svc.CapabilityGrant) string {
	return fmt.Sprintf("%+v", grants)
}

// extDevApprovalMemory 只活在内存里：桌面进程退出即失效，下一次运行重新询问。
type extDevApprovalMemory struct {
	mu       sync.Mutex
	approved map[extDevApprovalKey]struct{}
}

func (m *extDevApprovalMemory) has(key extDevApprovalKey) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.approved[key]
	return ok
}

func (m *extDevApprovalMemory) remember(key extDevApprovalKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.approved == nil {
		m.approved = make(map[extDevApprovalKey]struct{})
	}
	m.approved[key] = struct{}{}
}
