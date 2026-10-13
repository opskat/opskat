package dbdriver

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ErrDriverUnavailable 表示配置里的驱动 id 不在注册表中（驱动未内置，或提供它的扩展没有
// 加载）。Lookup 返回的错误包着它并点名驱动；调用方不得把这种资产当成别的驱动继续处理。
var ErrDriverUnavailable = errors.New("database driver unavailable")

var (
	mu      sync.RWMutex
	drivers = map[asset_entity.DatabaseDriver]Driver{}
	// names 把每个可断言的名字（id 与别名）映射到拥有它的驱动 id。
	names = map[string]asset_entity.DatabaseDriver{}
)

func init() {
	// 实体层在本包之下，Asset.Validate 里驱动自有的规则经这个挂钩反向接入。
	asset_entity.SetDatabaseDriverConfigValidator(validateConfig)
}

// validateConfig 跑配置所属驱动自己的规则。驱动没有注册时没有规则可跑，直接通过：这样的
// 资产连不上、查不了（Lookup 处报 ErrDriverUnavailable），但仍要能改名、移动、保存——与扩展
// 资产类型在扩展没加载时的做法一致（asset_entity.ValidateRegisteredConfig）。
func validateConfig(a *asset_entity.Asset, cfg *asset_entity.DatabaseConfig) error {
	mu.RLock()
	d, ok := drivers[cfg.Driver]
	mu.RUnlock()
	if !ok {
		return nil
	}
	return d.ValidateConfig(a, cfg)
}

// Register 注册一个驱动。id 为空、或 id / 别名与已注册驱动的任何名字相同时 panic：
// 一个名字只能指向一个驱动，撞名是注册方的编程错误，不该被后注册者静默覆盖。
func Register(d Driver) {
	desc := d.Descriptor()
	if desc.ID == "" {
		panic("dbdriver: driver must declare an id")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range desc.Names() {
		if owner, exists := names[name]; exists {
			panic(fmt.Sprintf("dbdriver: name %q of driver %q is already registered by driver %q", name, desc.ID, owner))
		}
	}
	drivers[desc.ID] = d
	for _, name := range desc.Names() {
		names[name] = desc.ID
	}
}

// Unregister 移除一个驱动及它占用的全部名字。
func Unregister(id asset_entity.DatabaseDriver) {
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
func Lookup(id asset_entity.DatabaseDriver) (Driver, error) {
	mu.RLock()
	d, ok := drivers[id]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q (registered drivers: %s)", ErrDriverUnavailable, id, strings.Join(IDs(), ", "))
	}
	return d, nil
}

// ByName 按 id 或别名取驱动，用于解析调用方声明的类型名（--type postgres）。
func ByName(name string) (Driver, bool) {
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
func List() []Driver {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Driver, 0, len(drivers))
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

// IDs 返回全部已注册的驱动 id，顺序同 List；给报错与帮助文本列出可用驱动用。
func IDs() []string {
	list := List()
	out := make([]string, 0, len(list))
	for _, d := range list {
		out = append(out, string(d.Descriptor().ID))
	}
	return out
}
