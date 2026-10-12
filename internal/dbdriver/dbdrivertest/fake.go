// Package dbdrivertest 是驱动注册表的测试支撑：一个只在测试里注册的假驱动，和每个驱动
// 都必须通过的一致性测试（RunConformance）。
package dbdrivertest

import (
	"errors"
	"sync"
	"testing"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// ErrFakeHostRequired 是假驱动自己的规则报出的错误：见到它就说明走的确实是假驱动的规则，
// 而不是共享代码替它兜的某个内置驱动的规则。
var ErrFakeHostRequired = errors.New("fakedb requires host")

// FakeDefaultPort 是假驱动的默认端口，刻意不与任何内置驱动相同。
const FakeDefaultPort = 7777

// FakeOrder 是假驱动声明的展示顺序：排在所有内置驱动之后。
const FakeOrder = 900

// Fake 是一个不属于任何内置驱动的假驱动，用来证明加驱动只需要注册。它的规则刻意与内置
// 驱动不同：只要求 host，不要求 username；它的会话（FakeSession）也不是 database/sql 的
// 句柄，而是一张放在内存里的表——就像会话活在扩展里、宿主拿不到任何连接的驱动。
type Fake struct {
	Desc dbdriver.Descriptor
	// Columns / Rows 是假库里唯一的一张表：任何 SELECT 都返回它。
	Columns []string
	Rows    [][]any
	// OpenErr 非 nil 时 Open 以它失败，模拟连不上。
	OpenErr error
	// SessionPerAsset 为 true 时声明一条会话覆盖整个资产（像 SQLite）；默认一个库一条。
	SessionPerAsset bool

	mu       sync.Mutex
	sessions []*FakeSession
}

// NewFake 返回 id 为 "fakedb"、别名 "fake" 的假驱动。
func NewFake() *Fake {
	return &Fake{
		Desc: dbdriver.Descriptor{
			ID:          "fakedb",
			Name:        "FakeDB",
			Icon:        "database",
			Order:       FakeOrder,
			Aliases:     []string{"fake"},
			DefaultPort: FakeDefaultPort,
			Form: []dbdriver.FormTabFields{
				{Tab: dbdriver.TabConnection, Fields: []dbdriver.FormField{
					dbdriver.FieldHost, dbdriver.FieldPort, dbdriver.FieldPassword, dbdriver.FieldDatabase,
				}},
				{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{dbdriver.FieldReadOnly}},
			},
			Capabilities: []dbdriver.Capability{dbdriver.CapViews, dbdriver.CapReadOnly},
		},
		Columns: []string{"id", "name"},
		Rows:    [][]any{{1, "alice"}, {2, "bob"}, {3, "carol"}},
	}
}

// Register 注册驱动，并在测试结束时注销。
func Register(t testing.TB, d dbdriver.Driver) {
	t.Helper()
	dbdriver.Register(d)
	t.Cleanup(func() { dbdriver.Unregister(d.Descriptor().ID) })
}

func (f *Fake) Descriptor() dbdriver.Descriptor { return f.Desc }

func (f *Fake) ValidateConfig(_ *asset_entity.Asset, cfg *asset_entity.DatabaseConfig) error {
	if cfg.Host == "" {
		return ErrFakeHostRequired
	}
	return nil
}

func (f *Fake) CreateDefaults(args dbdriver.Args) map[string]any {
	if args.Int("port") != 0 {
		return nil
	}
	return map[string]any{"port": f.Desc.DefaultPort}
}

func (f *Fake) ValidateCreateArgs(args dbdriver.Args) error {
	if args.String("host") == "" {
		return ErrFakeHostRequired
	}
	return nil
}

func (f *Fake) ApplyCreateArgs(cfg *asset_entity.DatabaseConfig, args dbdriver.Args) {
	cfg.Host = args.String("host")
	cfg.Port = args.Int("port")
}

func (f *Fake) ApplyUpdateArgs(cfg *asset_entity.DatabaseConfig, args dbdriver.Args) {
	if args.Has("host") {
		cfg.Host = args.String("host")
	}
	if args.Has("port") {
		cfg.Port = args.Int("port")
	}
}

func (f *Fake) ConnectionView(cfg *asset_entity.DatabaseConfig) map[string]any {
	return map[string]any{"host": cfg.Host, "port": cfg.Port}
}
