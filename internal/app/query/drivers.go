package query

import (
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ListDatabaseDrivers 返回全部已注册数据库驱动的描述，按各驱动声明的展示顺序排列，扩展驱动
// 专属字段的标签与说明按 lang（前端的 i18next 语言，如 "zh-CN"）翻译。前端的驱动下拉、表单
// 字段与功能入口都以它为准，不再各自写死驱动名。语言由调用方给出而不读 System.SetLanguage 的
// 镜像：前端在切换语言时重新取，与 SetLanguage 并发，镜像未必已是新语言（同
// ListInstalledExtensions）。
func (q *Query) ListDatabaseDrivers(lang string) []dbdriver.Descriptor {
	drivers := dbregistry.List()
	out := make([]dbdriver.Descriptor, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, dbregistry.LocalizedDescriptor(d, lang))
	}
	return out
}
