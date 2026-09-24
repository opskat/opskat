package opsctl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/approval"
	"github.com/opskat/opskat/pkg/extension"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

// extDevApprovalType 是 ext dev 安装在 opsctl 审批弹窗里的类型；ApprovalKindFor 对它
// 给出 once——安装只能逐次确认，不能落成常驻授权。
const extDevApprovalType = "ext_dev_install"

// handleExtDevInstall 处理 `opsctl ext dev <dir>`：经用户在桌面端确认后，把一个
// 未打包的扩展目录装进当前进程。
//
// 这条通道存在的意义是让扩展开发者不必再跑第二套宿主（原 cmd/devserver）：安装走的
// 就是 ExtDevInstaller 背后那一个 extension_svc.Install，与用户在扩展页点"从目录
// 安装"逐字相同，装完的扩展由同一个 WASM 运行时、同一套能力面、同一份注册表承载。
// 重装即热重载——Install 会先 Unload 再加载并通知前端刷新。
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
	manifest, err := extension.ParseManifest(data)
	if err != nil {
		log.Warn("extension dev install refused", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}

	ctx := i18n.Ctx(o.ctx, o.lang.Lang())
	installedVersion, overwrites := o.extDevInstaller.InstalledExtensionVersion(ctx, manifest.Name)
	log = log.With(
		zap.String("extension", manifest.Name),
		zap.String("version", manifest.Version),
		zap.Bool("overwrites", overwrites),
		zap.String("installedVersion", installedVersion),
		zap.String("credentials", manifest.Capabilities.Credentials),
		zap.Bool("tunnel", manifest.Capabilities.Tunnel),
	)

	key := extDevApprovalKey{dir: sourceDir, name: manifest.Name, capabilities: capabilitiesFingerprint(manifest.Capabilities)}
	if o.extDevApprovals.has(key) {
		log.Info("extension dev install approved", zap.String("decision", "remembered"))
	} else {
		resp := o.extDevApprove(approval.ApprovalRequest{
			Type:    extDevApprovalType,
			Command: sourceDir,
			Detail:  extDevApprovalDetail(manifest, installedVersion, overwrites),
		})
		if !resp.Approved {
			log.Info("extension dev install denied", zap.String("reason", resp.Reason))
			return approval.ApprovalResponse{Approved: false, Reason: "extension dev install " + resp.Reason}
		}
		o.extDevApprovals.remember(key)
		log.Info("extension dev install approved", zap.String("decision", "user"))
	}

	name, version, err := o.extDevInstaller.InstallExtensionDir(ctx, staged)
	if err != nil {
		log.Error("extension dev install failed", zap.Error(err))
		return approval.ApprovalResponse{Approved: false, Reason: err.Error()}
	}
	log.Info("extension dev install completed")

	return approval.ApprovalResponse{Approved: true, Extension: name, Version: version}
}

// extDevApprovalDetail 是弹窗里除来源目录之外的全部内容：用户据此判断要不要放行。
func extDevApprovalDetail(m *extension.Manifest, installedVersion string, overwrites bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "extension: %s %s\n", m.Name, m.Version)
	if overwrites {
		fmt.Fprintf(&b, "overwrites installed: %s %s\n", m.Name, installedVersion)
	} else {
		b.WriteString("new install\n")
	}
	c := m.Capabilities
	b.WriteString("capabilities:")
	lines := 0
	capLine := func(label, value string) {
		fmt.Fprintf(&b, "\n  %s: %s", label, value)
		lines++
	}
	if len(c.FS.Read) > 0 {
		capLine("fs.read", strings.Join(c.FS.Read, ", "))
	}
	if len(c.FS.Write) > 0 {
		capLine("fs.write", strings.Join(c.FS.Write, ", "))
	}
	if len(c.HTTP.Allowlist) > 0 {
		capLine("http", strings.Join(c.HTTP.Allowlist, ", "))
	}
	if c.Credentials != "" {
		capLine("credentials", c.Credentials)
	}
	if c.Tunnel {
		capLine("tunnel", "true")
	}
	if lines == 0 {
		b.WriteString(" none")
	}
	return b.String()
}

// extDevApprovalKey 是一次 ext dev 确认覆盖的范围。能力也在键里：重建后能力变宽是
// 一个新的决定，不是一次重载。
type extDevApprovalKey struct {
	dir          string
	name         string
	capabilities string
}

func capabilitiesFingerprint(c extension.Capabilities) string {
	return fmt.Sprintf("%+v", c)
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
