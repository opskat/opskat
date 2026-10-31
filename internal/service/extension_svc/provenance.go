package extension_svc

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/internal/service/command_review_svc"
)

// Where an installed package came from, as recorded in extension_state.source.
const (
	InstallOriginStore = "store" // 官方商店下载安装
	InstallOriginLocal = "local" // 用户从本地文件或目录安装
	InstallOriginDev   = "dev"   // opsctl ext dev 的开发安装
)

// PackageSHA256 是扩展目录 dir 的包摘要：目录里每个文件按相对路径排序，路径与内容各带长度
// 一起哈希。不只是 WASM 与 manifest.json——manifest 带着权限声明，locales / SKILL.md 决定宿主
// 显示什么，扩展还能读自己目录里的任何文件（permissions.fs.read ${EXT_DIR}/**），改了哪个都会
// 改变它的行为。安装只复制普通文件与目录，目录里出现别的类型（符号链接等）按无法计算失败。
func PackageSHA256(dir string) (string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("package file %s is not a regular file", p)
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("package digest: %w", err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, rel := range files {
		if err := hashPackageFile(h, dir, rel); err != nil {
			return "", fmt.Errorf("package digest: %w", err)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashPackageFile 把一个文件的相对路径与内容（各带长度前缀）写进 h。
func hashPackageFile(h io.Writer, dir, rel string) error {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel))) //nolint:gosec // 路径来自扩展目录的遍历
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(rel)))
	_, _ = h.Write(n[:])
	_, _ = io.WriteString(h, rel)
	binary.BigEndian.PutUint64(n[:], uint64(info.Size()))
	_, _ = h.Write(n[:])
	copied, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if copied != info.Size() {
		return fmt.Errorf("%s changed while being hashed", rel)
	}
	return nil
}

// reviewerEligible 判断这条安装记录能不能当审核器：记录的摘要必须与现算的 sum 一致；
// 商店安装一律合格且 signed；本地与开发安装在正式版不合格，其他构建合格但 signed 为假
// （未经签名）。来源为空（迁移前的旧记录）一律不合格。
func reviewerEligible(state *extension_state_entity.ExtensionState, sum string, app appversion.Info) (signed bool, err error) {
	if state == nil || state.PackageSHA256 == "" || state.PackageSHA256 != sum {
		return false, ineligible(command_review_svc.UnusableDigestMismatch,
			errors.New("command reviewer package digest does not match the installed package"))
	}
	switch state.Source {
	case InstallOriginStore:
		return true, nil
	case InstallOriginLocal, InstallOriginDev:
		if app.Kind == appversion.KindRelease {
			return false, ineligible(command_review_svc.UnusableStoreRequired,
				fmt.Errorf("command reviewer must be installed from the store on a release build (installed from %s)", state.Source))
		}
		return false, nil
	default:
		return false, ineligible(command_review_svc.UnusableNoInstallRecord, errors.New("command reviewer has no recorded install source"))
	}
}

func ineligible(code string, err error) error {
	return &command_review_svc.IneligibleError{Code: code, Err: err}
}

// ReviewerEligibility 回答 name 这个扩展此刻能不能当命令审核器，以及它是否经官方商店
// 签名。包摘要每次从磁盘现算再与安装记录比对，不运行扩展代码；不合格时的错误说明原因，
// 调用方按审核失败处理。
func (s *Service) ReviewerEligibility(ctx context.Context, name string) (signed bool, err error) {
	state, err := s.stateRepo.Find(ctx, name)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return false, fmt.Errorf("command reviewer %q: %w", name,
			ineligible(command_review_svc.UnusableNoInstallRecord, errors.New("no install record")))
	case err != nil:
		return false, fmt.Errorf("command reviewer %q: read install record: %w", name, err)
	}
	sum, err := PackageSHA256(s.manager.ExtDir(name))
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
