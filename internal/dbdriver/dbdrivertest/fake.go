// Package dbdrivertest 是驱动注册表的测试支撑：一个只在测试里注册的假驱动，和每个驱动
// 都必须通过的一致性测试（RunConformance）。
package dbdrivertest

import (
	"errors"
	"sync"
	"testing"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
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
	// Databases / Tables / Metadata / Objects / Sources 是假库在树的浏览里给出的内容：库名列表、
	// 各库的表名（按库名取）、任何表的元数据、任何库的对象列表、对象源码（键为 "类型:名字"）。
	Databases []string
	Tables    map[string][]string
	Metadata  *dbdriver.TableMetadata
	Objects   *dbdriver.DatabaseObjects
	Sources   map[string]string
	// BrowseErr 非 nil 时树的浏览操作以它失败，模拟数据库返回的错误。
	BrowseErr error
	// ColumnTypes / ColumnCategories / ColumnRules / PrimaryKeys 是那张表在打开时给出的列类型、列类别、
	// 列约束与主键。
	// 表浏览（打开、翻页、计数）不看筛选与排序，只把收到的请求记下来（FakeSession.TableCalls），
	// 照 Columns / Rows 原样分页返回。
	ColumnTypes      map[string]string
	ColumnCategories map[string]dbdriver.ColumnCategory
	ColumnRules      []dbdriver.TableColumnRule
	PrimaryKeys      []string
	// TableErr 非 nil 时表浏览操作以它失败：模拟数据库返回的错误，或（包着
	// dbdriver.ErrUnsupported 时）一个不提供表浏览的驱动。
	TableErr error
	// DDL 是假库给任何表的建表语句。会话只在驱动声明了 CapViewDDL 时给出它，否则以
	// ErrUnsupported 拒绝；TableErr 同样作用于它。
	DDL string
	// StatementErrors 模拟数据库拒绝的语句：ExecuteBatch 执行到原样在其中的语句时，以对应的错误
	// 记为失败。NewFake 已放进 ConformanceFailingStatement，一致性测试靠它检查整批的失败处理。
	StatementErrors map[string]error
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

		Databases: []string{"app", "reporting"},
		Tables:    map[string][]string{"app": {"users", "orders"}, "reporting": {"events", "v_users"}},
		Metadata: &dbdriver.TableMetadata{
			Columns: []dbdriver.ColumnMeta{
				{Name: "id", Type: "int", PrimaryKey: true, Comment: "primary key"},
				{Name: "name", Type: "text", Nullable: true, Default: new("anon")},
			},
			Indexes:     []dbdriver.IndexMeta{{Name: "pk_users", Columns: []string{"id"}, Unique: true}},
			ForeignKeys: []dbdriver.ForeignKeyMeta{},
		},
		Objects: &dbdriver.DatabaseObjects{
			Views: []dbdriver.ObjectMeta{{Name: "v_users", Type: dbdriver.ObjectView}},
		},
		Sources: map[string]string{"view:v_users": "SELECT id FROM users"},

		ColumnTypes:      map[string]string{"id": "int", "name": "text"},
		ColumnCategories: map[string]dbdriver.ColumnCategory{"id": dbdriver.CategoryNumber, "name": dbdriver.CategoryText},
		ColumnRules: []dbdriver.TableColumnRule{
			{Name: "id", AutoIncrement: true},
			{Name: "name", Nullable: true},
		},
		PrimaryKeys: []string{"id"},
		DDL:         "CREATE TABLE users (id int, name text)",

		StatementErrors: map[string]error{ConformanceFailingStatement: errors.New("fakedb: syntax error")},
	}
}

// Register 注册驱动，并在测试结束时注销。
func Register(t testing.TB, d dbdriver.Driver) {
	t.Helper()
	dbregistry.Register(d)
	t.Cleanup(func() { dbregistry.Unregister(d.Descriptor().ID) })
}

func (f *Fake) Descriptor() dbdriver.Descriptor { return f.Desc }

func (f *Fake) ValidateConfig(cfg *dbdriver.Config, _ dbdriver.Route) error {
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

func (f *Fake) ApplyCreateArgs(cfg *dbdriver.Config, args dbdriver.Args) {
	cfg.Host = args.String("host")
	cfg.Port = args.Int("port")
}

func (f *Fake) ApplyUpdateArgs(cfg *dbdriver.Config, args dbdriver.Args) {
	if args.Has("host") {
		cfg.Host = args.String("host")
	}
	if args.Has("port") {
		cfg.Port = args.Int("port")
	}
}

func (f *Fake) ConnectionView(cfg *dbdriver.Config) map[string]any {
	return map[string]any{"host": cfg.Host, "port": cfg.Port}
}
