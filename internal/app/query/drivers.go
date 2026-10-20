package query

import (
	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ListDatabaseDrivers 返回全部已注册数据库驱动的描述，按各驱动声明的展示顺序排列。
// 前端的驱动下拉、表单字段与功能入口都以它为准，不再各自写死驱动名。
func (q *Query) ListDatabaseDrivers() []dbdriver.Descriptor {
	drivers := dbregistry.List()
	out := make([]dbdriver.Descriptor, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d.Descriptor())
	}
	return out
}
