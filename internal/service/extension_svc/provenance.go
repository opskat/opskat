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

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/opskat/opskat/internal/model/entity/extension_state_entity"
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

// Provenance 是一个已安装扩展的安装事实，只陈述不评判：哪些来源可以做什么由使用方决定
// （例如命令审核器注册表）。
type Provenance struct {
	// Recorded 表示有安装记录；没有时其余字段为零值。
	Recorded bool
	// Source 是记录的安装来源（InstallOrigin*）；迁移前的旧记录可能为空。
	Source string
	// PackageSHA256 是此刻从磁盘现算的包摘要（PackageSHA256）。
	PackageSHA256 string
	// DigestMatches 表示现算的包摘要与安装时记录的一致（记录里没有摘要时为假）。
	DigestMatches bool
}

// Provenance 回答 name 这个扩展的安装事实。包摘要每次从磁盘现算再与安装记录比对，
// 不运行扩展代码；没有安装记录不是错误（Recorded 为假），读不了记录或算不出摘要才是。
func (s *Service) Provenance(ctx context.Context, name string) (Provenance, error) {
	state, err := s.stateRepo.Find(ctx, name)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return Provenance{}, nil
	case err != nil:
		return Provenance{}, fmt.Errorf("extension %q: read install record: %w", name, err)
	}
	sum, err := s.digests.get(name, s.manager.ExtDir(name))
	if err != nil {
		return Provenance{}, fmt.Errorf("extension %q: %w", name, err)
	}
	return Provenance{
		Recorded:      true,
		Source:        state.Source,
		PackageSHA256: sum,
		DigestMatches: state.PackageSHA256 != "" && state.PackageSHA256 == sum,
	}, nil
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
