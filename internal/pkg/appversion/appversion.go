// Package appversion is the one place that knows what version the running app is
// and how versions order: update checks and extension compatibility both ask here.
package appversion

import (
	"strconv"
	"strings"

	"github.com/cago-frame/cago/configs"

	"github.com/opskat/opskat/internal/buildinfo"
)

// Kind says how much a version number can be trusted to order releases.
type Kind string

const (
	// KindRelease is an official build: its version number is a real release.
	KindRelease Kind = "release"
	// KindNightly is a nightly build: its number is the base it was cut from.
	KindNightly Kind = "nightly"
	// KindDev is a build without a commit id (wails dev, the sandbox): the version
	// number is just the Makefile default and says nothing.
	KindDev Kind = "dev"
)

// Info is the running app's version and kind.
type Info struct {
	Version string
	Kind    Kind
}

// Current reports the running app.
func Current() Info {
	version := strings.TrimPrefix(configs.Version, "v")
	return Info{Version: version, Kind: KindOf(version, buildinfo.CommitID)}
}

// KindOf classifies a build. A missing commit id means a development build
// whatever the version string claims; otherwise the version string decides
// between nightly and release.
func KindOf(version, commitID string) Kind {
	switch {
	case commitID == "":
		return KindDev
	case IsNightly(version):
		return KindNightly
	default:
		return KindRelease
	}
}

// IsNightly 判断是否为 nightly 版本
func IsNightly(version string) bool {
	return strings.Contains(version, "nightly.") || strings.HasPrefix(version, "nightly-")
}

// Compare 比较两个版本号，支持预发布后缀
// 如 "1.0.0" vs "1.0.0-beta.1"，"1.0.0-beta.1" vs "1.0.0-beta.2"
// 返回: >0 表示 a 更新, <0 表示 b 更新, 0 表示相同
func Compare(a, b string) int {
	a, b = strings.TrimPrefix(a, "v"), strings.TrimPrefix(b, "v")
	aBase, aPre := splitPreRelease(a)
	bBase, bPre := splitPreRelease(b)

	result := compareBase(aBase, bBase)
	if result != 0 {
		return result
	}

	// 同基础版本: 无预发布 > 有预发布 (stable > beta)
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre != "" && bPre == "" {
		return -1
	}
	if aPre == "" && bPre == "" {
		return 0
	}

	return comparePreRelease(aPre, bPre)
}

// splitPreRelease 分离基础版本和预发布后缀
// "1.0.0-beta.1" -> ("1.0.0", "beta.1")
func splitPreRelease(v string) (string, string) {
	idx := strings.Index(v, "-")
	if idx < 0 {
		return v, ""
	}
	return v[:idx], v[idx+1:]
}

// compareBase 比较基础版本号 (如 "1.0.0" vs "0.2.0")
func compareBase(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var aNum, bNum int
		if i < len(aParts) {
			aNum, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bNum, _ = strconv.Atoi(bParts[i])
		}
		if aNum != bNum {
			return aNum - bNum
		}
	}
	return 0
}

// comparePreRelease 比较预发布标识符
// "beta.1" vs "beta.2", "beta.1" vs "rc.1"
func comparePreRelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")

	maxLen := len(aParts)
	if len(bParts) > maxLen {
		maxLen = len(bParts)
	}

	for i := 0; i < maxLen; i++ {
		var ap, bp string
		if i < len(aParts) {
			ap = aParts[i]
		}
		if i < len(bParts) {
			bp = bParts[i]
		}

		aNum, aErr := strconv.Atoi(ap)
		bNum, bErr := strconv.Atoi(bp)
		if aErr == nil && bErr == nil {
			if aNum != bNum {
				return aNum - bNum
			}
		} else if ap != bp {
			return strings.Compare(ap, bp)
		}
	}
	return 0
}
