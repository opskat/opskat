// Package dbdriver 是宿主的数据库驱动注册表：驱动按公开契约（pkg/dbdriver）实现，在这里
// Register，共享代码按存进资产配置的驱动 id 经 Lookup / ByName / List 取用，不按驱动名分支。
//
// 内置驱动是本包的子包（mysql / postgresql / mssql / sqlite），在各自的 init() 里 Register，
// 由 builtin 一并链接；新增驱动 = 新增一个注册，不改这里也不改调用方。
package dbdriver

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// ErrDriverUnavailable 表示配置里的驱动 id 不在注册表中（驱动未内置，或提供它的扩展没有
// 加载）。Lookup 返回的错误包着它并点名驱动；调用方不得把这种资产当成别的驱动继续处理。
var ErrDriverUnavailable = errors.New("database driver unavailable")

var (
	mu      sync.RWMutex
	drivers = map[dbdriver.DriverID]dbdriver.Driver{}
	// names 把每个可断言的名字（id 与别名）映射到拥有它的驱动 id。
	names = map[string]dbdriver.DriverID{}
)

func init() {
	// 实体层在本包之下，Asset.Validate 里驱动自有的规则经这个挂钩反向接入。
	asset_entity.SetDatabaseDriverConfigValidator(validateConfig)
}

// validateConfig 跑配置所属驱动自己的规则。驱动没有注册时没有规则可跑，直接通过：这样的
// 资产连不上、查不了（Lookup 处报 ErrDriverUnavailable），但仍要能改名、移动、保存——与扩展
// 资产类型在扩展没加载时的做法一致（asset_entity.ValidateRegisteredConfig）。
func validateConfig(cfg *dbdriver.Config, route dbdriver.Route) error {
	mu.RLock()
	d, ok := drivers[cfg.Driver]
	mu.RUnlock()
	if !ok {
		return nil
	}
	return d.ValidateConfig(cfg, route)
}

// NameTakenError 是 TryRegister 拒绝一个驱动的原因：它的 id 或某个别名（Name）已经属于
// 已注册的驱动 Owner。
type NameTakenError struct {
	Name   string
	Driver dbdriver.DriverID
	Owner  dbdriver.DriverID
}

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("dbdriver: name %q of driver %q is already registered by driver %q", e.Name, e.Driver, e.Owner)
}

// Register 注册一个内置驱动（在 init() 里调用）。id 为空、或 id / 别名与已注册驱动的任何
// 名字相同时 panic：一个名字只能指向一个驱动，内置驱动撞名是编程错误，不该被后注册者
// 静默覆盖。
func Register(d dbdriver.Driver) {
	if err := TryRegister(d); err != nil {
		panic(err.Error())
	}
}

// TryRegister 注册一个运行期才出现的驱动（扩展提供的）。规则同 Register，但撞名不 panic：
// 返回 *NameTakenError（id 为空时是普通错误），驱动的任何名字都不注册。
func TryRegister(d dbdriver.Driver) error {
	desc := d.Descriptor()
	if desc.ID == "" {
		return errors.New("dbdriver: driver must declare an id")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range desc.Names() {
		if owner, exists := names[name]; exists {
			return &NameTakenError{Name: name, Driver: desc.ID, Owner: owner}
		}
	}
	drivers[desc.ID] = d
	for _, name := range desc.Names() {
		names[name] = desc.ID
	}
	return nil
}

// Unregister 移除一个驱动及它占用的全部名字。
func Unregister(id dbdriver.DriverID) {
	mu.Lock()
	defer mu.Unlock()
	delete(drivers, id)
	for name, owner := range names {
		if owner == id {
			delete(names, name)
		}
	}
}

// Lookup 按存进配置的驱动 id 取驱动（别名不是合法的 id）。未注册时返回包着
// ErrDriverUnavailable、点名该驱动的错误。
func Lookup(id dbdriver.DriverID) (dbdriver.Driver, error) {
	mu.RLock()
	d, ok := drivers[id]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered drivers: %s)", ErrDriverUnavailable, id, strings.Join(IDs(), ", "))
	}
	return d, nil
}

// ByName 按 id 或别名取驱动，用于解析调用方声明的类型名（--type postgres）。
func ByName(name string) (dbdriver.Driver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	id, ok := names[name]
	if !ok {
		return nil, false
	}
	return drivers[id], true
}

// List 返回全部已注册的驱动，按各自声明的 Descriptor.Order 排序（相同时按 id），与注册
// 先后无关。
func List() []dbdriver.Driver {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]dbdriver.Driver, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Descriptor(), out[j].Descriptor()
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
	return out
}

// Localizer 是描述里带着要翻译的文案的驱动（扩展驱动：专属字段的标签与说明是扩展多语言里的
// 键）。内置驱动的描述没有这样的文案，不实现它。
type Localizer interface {
	// LocalizedDescriptor 返回按 lang（前端的 i18next 语言，如 "zh-CN"）翻译后的描述。
	LocalizedDescriptor(lang string) dbdriver.Descriptor
}

// LocalizedDescriptor 是 d 交给界面的描述：实现了 Localizer 的按 lang 翻译，其余原样。
func LocalizedDescriptor(d dbdriver.Driver, lang string) dbdriver.Descriptor {
	if l, ok := d.(Localizer); ok {
		return l.LocalizedDescriptor(lang)
	}
	return d.Descriptor()
}

// IDs 返回全部已注册的驱动 id，顺序同 List；给报错与帮助文本列出可用驱动用。
func IDs() []string {
	list := List()
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, string(d.Descriptor().ID))
	}
	return out
}
