package extreg

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 扩展提供的数据库驱动：注册进驱动注册表（internal/dbdriver）的是 extensionDriver，它的会话
// 经 Plugin.OpenDriverSession 活在扩展里。共享代码（connpool、查询面板、AI）照常按资产配置的
// 驱动 id Lookup、Open，拿到的请求就是宿主替内置驱动构造的那一份。
//
// 扩展被停用、升级或卸载时（Unregister），驱动先移出注册表，再作废它打开过的每一条会话：
// 执行中与排队的操作被取消，之后的操作——包括还缓存在查询面板、AI 里的那些会话上的——都报
// 点名该驱动的 dbregistry.ErrDriverUnavailable，而不是扩展实例关闭后各种各样的报错。重新
// 启用注册的是新的 extensionDriver，与作废的会话无关。

// driverOpener 是扩展驱动用到的插件能力（*extension.Plugin 满足它）。
type driverOpener interface {
	OpenDriverSession(ctx context.Context, id string, req dbdriver.OpenRequest) (dbdriver.Session, error)
}

// driverOwner 记录每个已注册的扩展驱动 id 归哪个扩展，撞名时据此点名占用者。调用方持有 mu。
var driverOwner = make(map[dbdriver.DriverID]string) // driver id → extension name

// extensionDriver 是一个扩展驱动在宿主注册表里的条目。配置规则是主机 + 端口 + 用户名一类的
// 网络驱动的通用规则；一条会话只连一个库。
type extensionDriver struct {
	dbdriver.NetworkRules
	desc   dbdriver.Descriptor
	ext    string
	plugin driverOpener
	// translate 按语言解析提供驱动的扩展的多语言键（Extension.Translate）。
	translate translateFunc

	// life 在驱动被移除时取消，取消原因是 unavailable()；每个操作的 ctx 都挂在它下面。
	life context.Context
	end  context.CancelCauseFunc

	mu       sync.Mutex
	sessions map[*extensionSession]struct{}
}

// translateFunc 按语言解析扩展多语言里的键，没有翻译时返回键本身。
type translateFunc func(lang, key string) string

func newExtensionDriver(ext string, desc dbdriver.Descriptor, plugin driverOpener, translate translateFunc) *extensionDriver {
	life, end := context.WithCancelCause(context.Background())
	return &extensionDriver{
		NetworkRules: dbdriver.NetworkRules{DefaultPort: desc.DefaultPort},
		desc:         desc,
		ext:          ext,
		plugin:       plugin,
		translate:    translate,
		life:         life,
		end:          end,
		sessions:     make(map[*extensionSession]struct{}),
	}
}

func (d *extensionDriver) Descriptor() dbdriver.Descriptor { return d.desc }

// LocalizedDescriptor 让专属字段的标签与说明走扩展自己的多语言（dbregistry.Localizer）。
func (d *extensionDriver) LocalizedDescriptor(lang string) dbdriver.Descriptor {
	return d.desc.Localized(func(key string) string { return d.translate(lang, key) })
}

func (d *extensionDriver) SessionPerDatabase() bool { return true }

func (d *extensionDriver) Open(ctx context.Context, req dbdriver.OpenRequest) (dbdriver.Session, error) {
	ctx, done := d.operation(ctx)
	defer done()
	if err := d.removed(); err != nil {
		return nil, err
	}
	inner, err := d.plugin.OpenDriverSession(ctx, string(d.desc.ID), req)
	if err != nil {
		if gone := d.removed(); gone != nil {
			return nil, gone
		}
		return nil, err
	}
	s := &extensionSession{driver: d, inner: inner}
	d.mu.Lock()
	defer d.mu.Unlock()
	if gone := d.removed(); gone != nil {
		s.closeInner()
		return nil, gone
	}
	d.sessions[s] = struct{}{}
	return s, nil
}

// unavailable 是驱动移除后每个操作的错误：点名驱动与提供它的扩展。
func (d *extensionDriver) unavailable() error {
	return fmt.Errorf("%w: %q (its extension %q is no longer loaded)", dbregistry.ErrDriverUnavailable, d.desc.ID, d.ext)
}

// removed 在驱动已被移除时返回 unavailable()。
func (d *extensionDriver) removed() error {
	if d.life.Err() != nil {
		return context.Cause(d.life)
	}
	return nil
}

// operation 给一个操作派生 ctx：调用方取消或驱动被移除时都结束。done 释放它。
func (d *extensionDriver) operation(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(d.life, func() { cancel(context.Cause(d.life)) })
	return ctx, func() {
		stop()
		cancel(nil)
	}
}

// remove 作废驱动：取消执行中与排队的操作，关闭它打开过的全部会话。驱动已经移出注册表，
// 不会再有新会话。
func (d *extensionDriver) remove() {
	d.mu.Lock()
	d.end(d.unavailable())
	sessions := d.sessions
	d.sessions = make(map[*extensionSession]struct{})
	d.mu.Unlock()
	for s := range sessions {
		s.closeInner()
	}
	logger.Default().Info("extension database driver removed",
		zap.String("extension", d.ext), zap.String("driver", string(d.desc.ID)), zap.Int("sessions", len(sessions)))
}

// registerDrivers 把 ext 提供的驱动注册进驱动注册表，全有或全无：任何一个与内置驱动或别的
// 扩展的驱动撞名（id 或别名），已注册的这几个撤回，返回点名冲突名字与占用者的错误。调用方
// 持有 mu。
func registerDrivers(ext string, descs []dbdriver.Descriptor, plugin driverOpener, translate translateFunc) ([]*extensionDriver, error) {
	done := make([]*extensionDriver, 0, len(descs))
	for _, desc := range descs {
		d := newExtensionDriver(ext, desc, plugin, translate)
		if err := dbregistry.TryRegister(d); err != nil {
			removeDrivers(releaseDrivers(done))
			return nil, fmt.Errorf("extension %q: database driver %q: %w", ext, desc.ID, describeDriverClash(err))
		}
		driverOwner[desc.ID] = ext
		done = append(done, d)
	}
	return done, nil
}

// describeDriverClash 把注册表的撞名错误换成点名占用者的说法：内置驱动，还是哪个扩展的驱动。
// 调用方持有 mu。
func describeDriverClash(err error) error {
	var taken *dbregistry.NameTakenError
	if !errors.As(err, &taken) {
		return err
	}
	if ext, ok := driverOwner[taken.Owner]; ok {
		return fmt.Errorf("name %q is already taken by driver %q of extension %q", taken.Name, taken.Owner, ext)
	}
	return fmt.Errorf("name %q is already taken by built-in driver %q", taken.Name, taken.Owner)
}

// releaseDrivers 把驱动移出注册表，之后没有新会话能打开。调用方持有 mu；返回值交给
// removeDrivers 作废会话。
func releaseDrivers(drivers []*extensionDriver) []*extensionDriver {
	for _, d := range drivers {
		dbregistry.Unregister(d.desc.ID)
		delete(driverOwner, d.desc.ID)
	}
	return drivers
}

// removeDrivers 作废已移出注册表的驱动的会话。关闭会话要等扩展里执行中的调用停下，所以不在
// mu 里做。
func removeDrivers(drivers []*extensionDriver) {
	for _, d := range drivers {
		d.remove()
	}
}
