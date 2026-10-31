package extension_svc

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
)

// Where an installed package came from, as recorded in extension_state.source.
const (
	InstallOriginStore = "store" // 官方商店下载安装
	InstallOriginLocal = "local" // 用户从本地文件或目录安装
	InstallOriginDev   = "dev"   // opsctl ext dev 的开发安装
)

// PackageSHA256 是已安装扩展的包摘要：WASM 与 manifest.json 一起哈希。manifest 带着
// 权限声明（网络白名单、tunnel、设置地址），只哈希 WASM 挡不住改了权限后沿用旧摘要。
func PackageSHA256(dir, binary string) (string, error) {
	wasm, err := os.ReadFile(filepath.Join(dir, binary)) //nolint:gosec // 路径来自已安装扩展目录
	if err != nil {
		return "", fmt.Errorf("read wasm for package digest: %w", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // 路径来自已安装扩展目录
	if err != nil {
		return "", fmt.Errorf("read manifest for package digest: %w", err)
	}
	return packageDigest(wasm, manifest), nil
}

func packageDigest(wasm, manifest []byte) string {
	h := sha256.New()
	_, _ = h.Write(wasm)
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(manifest)
	return hex.EncodeToString(h.Sum(nil))
}

// maxPackageFileBytes bounds one file read for a digest (the runtime caps a whole zip
// at its own limit, which a single entry cannot exceed).
const maxPackageFileBytes = 512 << 20

// sourceDigest 是安装来源（目录或 .zip）的包摘要，与装好后的 PackageSHA256 同一算法。
// Stage 把来源原样展开 / 复制，所以装入前就能算出装入后的摘要，安装记录可以在
// 切换文件之前写入、失败时恢复。
func sourceDigest(sourcePath string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(sourcePath), ".zip") {
		data, err := os.ReadFile(filepath.Join(sourcePath, "manifest.json")) //nolint:gosec // 用户选定的安装来源
		if err != nil {
			return "", fmt.Errorf("read manifest for package digest: %w", err)
		}
		manifest, err := extension.ParseManifest(data)
		if err != nil {
			return "", err
		}
		return PackageSHA256(sourcePath, manifest.Backend.Binary)
	}
	r, err := zip.OpenReader(sourcePath)
	if err != nil {
		return "", fmt.Errorf("open package for digest: %w", err)
	}
	defer func() { _ = r.Close() }()
	read := func(name string) ([]byte, error) {
		for _, f := range r.File {
			if filepath.Clean(f.Name) != name {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(io.LimitReader(rc, maxPackageFileBytes))
		}
		return nil, fmt.Errorf("package has no %s", name)
	}
	manifestData, err := read("manifest.json")
	if err != nil {
		return "", fmt.Errorf("read manifest for package digest: %w", err)
	}
	manifest, err := extension.ParseManifest(manifestData)
	if err != nil {
		return "", err
	}
	wasm, err := read(filepath.Clean(manifest.Backend.Binary))
	if err != nil {
		return "", fmt.Errorf("read wasm for package digest: %w", err)
	}
	return packageDigest(wasm, manifestData), nil
}

// reviewerEligible 判断这条安装记录能不能当审核器：记录的摘要必须与现算的 sum 一致；
// 商店安装一律合格且 signed；本地与开发安装在正式版不合格，其他构建合格但 signed 为假
// （未经签名）。来源为空（迁移前的旧记录）一律不合格。
func reviewerEligible(state *extension_state_entity.ExtensionState, sum string, app appversion.Info) (signed bool, err error) {
	if state == nil || state.PackageSHA256 == "" || state.PackageSHA256 != sum {
		return false, errors.New("command reviewer package digest does not match the installed package")
	}
	switch state.Source {
	case InstallOriginStore:
		return true, nil
	case InstallOriginLocal, InstallOriginDev:
		if app.Kind == appversion.KindRelease {
			return false, fmt.Errorf("command reviewer must be installed from the store on a release build (installed from %s)", state.Source)
		}
		return false, nil
	default:
		return false, errors.New("command reviewer has no recorded install source")
	}
}

// ReviewerEligibility 回答 name 这个扩展此刻能不能当命令审核器，以及它是否经官方商店
// 签名。包摘要每次从磁盘现算再与安装记录比对，不运行扩展代码；不合格时的错误说明原因，
// 调用方按审核失败处理。
func (s *Service) ReviewerEligibility(ctx context.Context, name string) (signed bool, err error) {
	state, err := s.stateRepo.Find(ctx, name)
	if err != nil {
		return false, fmt.Errorf("command reviewer %q has no install record: %w", name, err)
	}
	dir := s.manager.ExtDir(name)
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json")) //nolint:gosec // 路径来自已安装扩展目录
	if err != nil {
		return false, fmt.Errorf("command reviewer %q: read manifest: %w", name, err)
	}
	manifest, err := extension.ParseManifest(data)
	if err != nil {
		return false, fmt.Errorf("command reviewer %q: %w", name, err)
	}
	sum, err := PackageSHA256(dir, manifest.Backend.Binary)
	if err != nil {
		return false, fmt.Errorf("command reviewer %q: %w", name, err)
	}
	signed, err = reviewerEligible(state, sum, currentApp())
	if err != nil {
		logger.Ctx(ctx).Warn("command reviewer not eligible",
			zap.String("extension", name), zap.String("source", state.Source), zap.Error(err))
		return false, fmt.Errorf("command reviewer %q: %w", name, err)
	}
	return signed, nil
}

// recordProvenance 写入或覆盖安装来源与包摘要，同时设置启用状态，返回被覆盖的旧记录
// （之前没有时为 nil），安装失败时据此恢复。写库失败返回错误：装完却没有来源记录，
// 审核器资格就无从判断。
func (s *Service) recordProvenance(ctx context.Context, name, source, sum string) (*extension_state_entity.ExtensionState, error) {
	state, err := s.stateRepo.Find(ctx, name)
	if err != nil {
		if err := s.stateRepo.Create(ctx, &extension_state_entity.ExtensionState{
			Name: name, Enabled: true, Source: source, PackageSHA256: sum,
		}); err != nil {
			return nil, fmt.Errorf("create extension state: %w", err)
		}
		return nil, nil
	}
	prev := *state
	state.Enabled = true
	state.Source = source
	state.PackageSHA256 = sum
	if err := s.stateRepo.Update(ctx, state); err != nil {
		return nil, fmt.Errorf("update extension state: %w", err)
	}
	return &prev, nil
}

// restoreState 放回失败的安装覆盖掉的安装记录；prev 为 nil 表示之前没有记录。
func (s *Service) restoreState(ctx context.Context, name string, prev *extension_state_entity.ExtensionState) {
	var err error
	if prev == nil {
		err = s.stateRepo.Delete(ctx, name)
	} else {
		err = s.stateRepo.Update(ctx, prev)
	}
	if err != nil {
		s.logger.Error("restore extension state after failed install", zap.String("name", name), zap.Error(err))
	}
}
