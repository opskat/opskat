package extreg

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// PageSlots 是页面可以声明的插槽，由宿主定义。前端在 frontend/src/extension/slots.ts 里
// 另有一份 PAGE_SLOTS，TestPageSlotsMatchFrontend 对照两边。
var PageSlots = []string{"asset.connect"}

var urlLike = regexp.MustCompile(`(?i)^[a-z]+://`)

// pagesPoint 是前端页面的贡献点。页面由前端从扩展列表的清单里读出并加载，宿主进程这一侧
// 没有要接入的注册表；加载时在这里校验：每个页面必须声明宿主定义的插槽，入口脚本与样式的
// 路径必须在扩展目录内。
type pagesPoint struct{}

func (pagesPoint) Key() string { return extwire.KeyPages }

func (pagesPoint) Check(v extension.ExtensionView) error {
	var pages extwire.Pages
	if err := decodeContribution(v, extwire.KeyPages, &pages); err != nil {
		return err
	}
	if err := checkBundlePath("entry", pages.Entry); err != nil {
		return err
	}
	if err := checkBundlePath("styles", pages.Styles); err != nil {
		return err
	}
	for _, p := range pages.Pages {
		if p.Slot == "" {
			return fmt.Errorf("pages: page %q declares no slot (one of %s)", p.ID, strings.Join(PageSlots, ", "))
		}
		if !slices.Contains(PageSlots, p.Slot) {
			return fmt.Errorf("pages: page %q declares unknown slot %q (one of %s)", p.ID, p.Slot, strings.Join(PageSlots, ", "))
		}
	}
	return nil
}

// checkBundlePath 要求 path（入口或样式）是扩展目录内的相对路径；空表示没有声明。
func checkBundlePath(kind, path string) error {
	switch {
	case path == "":
		return nil
	case strings.Contains(path, ".."):
		return fmt.Errorf("pages: %s path contains traversal: %q", kind, path)
	case strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\`):
		return fmt.Errorf("pages: %s path must be relative: %q", kind, path)
	case urlLike.MatchString(path):
		return fmt.Errorf("pages: %s path must not be a URL: %q", kind, path)
	}
	return nil
}

func (pagesPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }
