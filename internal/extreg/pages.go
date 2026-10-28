package extreg

import (
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// pagesPoint 是前端页面的贡献点。页面由前端从扩展列表的清单里读出并加载（入口脚本与样式
// 路径的检查在前端加载器里），宿主进程这一侧没有要接入的注册表；它在这里，是为了让页面成为
// 一个已知的贡献，而不是一个未注册的键。
type pagesPoint struct{}

func (pagesPoint) Key() string { return extwire.KeyPages }

func (pagesPoint) Check(v extension.ExtensionView) error {
	var pages extwire.Pages
	return decodeContribution(v, extwire.KeyPages, &pages)
}

func (pagesPoint) Attach(extension.ExtensionView) (func(), error) { return func() {}, nil }
