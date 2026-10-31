package extreg

import (
	"context"
	"fmt"
	"slices"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extwire"
)

// dbDriversPoint 是数据库驱动的贡献点：驱动描述的规则、与 manifest 声明的一致性与 Kerberos
// 权限；接入的是驱动注册表（internal/dbdriver，见 dbdriver.go 的 extensionDriver）。
type dbDriversPoint struct{ guest guestFunc }

func (dbDriversPoint) Key() string { return extwire.KeyDatabaseDrivers }

// Check validates every driver descriptor on its own (dbdriver validation: known
// fields, capabilities and tabs, driver fields clear of the built-in ones) and
// against the extension driver's network rules (fitsNetworkRules), that no id
// appears twice, and that describe()'s driver ids are exactly the ones the
// manifest declared: the manifest is what the user audited before running the code,
// the descriptor is what the code implements, and they may not disagree. It runs
// for an extension that describes no driver too — the manifest may still declare
// one. A driver that declares the Kerberos field group (dbdriver.FieldKerberos) asks
// the host for tickets, which the user audits as permissions.kerberos.
func (dbDriversPoint) Check(v extension.ExtensionView) error {
	var drivers []dbdriver.Descriptor
	if err := decodeContribution(v, extwire.KeyDatabaseDrivers, &drivers); err != nil {
		return err
	}
	m := v.Manifest()
	described := make(map[string]struct{}, len(drivers))
	for _, drv := range drivers {
		if err := drv.Validate(); err != nil {
			return fmt.Errorf("databaseDrivers: %w", err)
		}
		id := string(drv.ID)
		if err := fitsNetworkRules(drv); err != nil {
			return fmt.Errorf("databaseDrivers[%s]: %w", id, err)
		}
		if _, dup := described[id]; dup {
			return fmt.Errorf("databaseDrivers[%s]: duplicate driver %q", id, id)
		}
		described[id] = struct{}{}
		if !slices.Contains(m.Contributes.DatabaseDrivers, id) {
			return fmt.Errorf("databaseDrivers[%s]: describe() provides database driver %q that manifest contributes.databaseDrivers does not declare", id, id)
		}
		if drv.UsesField(dbdriver.FieldKerberos) && !m.Permissions.Kerberos {
			return fmt.Errorf("databaseDrivers[%s]: database driver %q declares the Kerberos field group (form field %q), which requires manifest permissions.kerberos",
				id, id, dbdriver.FieldKerberos)
		}
	}
	for _, id := range m.Contributes.DatabaseDrivers {
		if _, ok := described[id]; !ok {
			return fmt.Errorf("databaseDrivers[%s]: manifest declares database driver %q but describe() does not provide it", id, id)
		}
	}
	return nil
}

// Attach 把扩展的驱动注册进驱动注册表，全有或全无（registerDrivers）。有运行中的插件时驱动
// 背后是它；仅描述接入的驱动描述与专属字段可用，打开会话与切分语句报驱动不可用
// （notLoadedOpener）。撤销时驱动先移出注册表，再在 mu 之外作废（removeDrivers：结束驱动、
// 关闭插件的驱动、关闭它们打开过的会话）。
func (p dbDriversPoint) Attach(v extension.ExtensionView) (func(), error) {
	l, err := p.guest.loaded(v)
	if err != nil {
		return nil, err
	}
	var opener driverOpener = notLoadedOpener{ext: l.name}
	if l.plugin != nil {
		opener = l.plugin
	}
	mu.Lock()
	extDrivers, err := registerDrivers(l.name, l.manifest.DatabaseDrivers, opener, l.translate)
	mu.Unlock()
	if err != nil {
		// 注册与撞名之间已可能有会话打开：在 mu 之外作废。
		removeDrivers(extDrivers, opener)
		return nil, err
	}
	return func() {
		mu.Lock()
		released := releaseDrivers(extDrivers)
		mu.Unlock()
		removeDrivers(released, opener)
	}, nil
}

// notLoadedOpener 是仅描述接入里驱动背后的"插件"：驱动的描述与专属字段已可用，但这个进程没有
// 加载扩展，打开会话与切分语句都报驱动不可用。需要执行的调用方先激活扩展（撤销 + 完整接入）。
type notLoadedOpener struct{ ext string }

func (o notLoadedOpener) OpenDriverSession(_ context.Context, id string, _ dbdriver.OpenRequest) (dbdriver.Session, error) {
	return nil, o.unavailable(id)
}

func (o notLoadedOpener) ClassifyStatements(_ context.Context, id, _ string) ([]dbdriver.Statement, error) {
	return nil, o.unavailable(id)
}

// CloseDrivers：没有加载的扩展里没有在执行的调用。
func (notLoadedOpener) CloseDrivers() {}

func (o notLoadedOpener) unavailable(id string) error {
	return fmt.Errorf("%w: %q (its extension %q is not loaded in this process)", dbregistry.ErrDriverUnavailable, id, o.ext)
}

// DriverOwnerOf 回答已注册的数据库驱动 id 由哪个扩展提供；内置驱动与未注册的 id 报 false。
func DriverOwnerOf(id dbdriver.DriverID) (string, bool) {
	mu.Lock()
	defer mu.Unlock()
	ext, ok := driverOwner[id]
	return ext, ok
}
