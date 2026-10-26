package dbdrivertest

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/pkg/dbdriver"
)

func TestFakeDriverPassesConformance(t *testing.T) {
	fake := NewFake()
	Register(t, fake)
	RunConformance(t, fake, FakeSessions(fake))
}

// 一致性测试自己也得能失败：每种不完整的描述都要被点出来。
func TestConformanceReportsIncompleteDescriptors(t *testing.T) {
	for name, tt := range map[string]struct {
		mutate func(*dbdriver.Descriptor)
		want   string
	}{
		"missing display name": {func(d *dbdriver.Descriptor) { d.Name = "" }, "display name"},
		"missing icon":         {func(d *dbdriver.Descriptor) { d.Icon = "" }, "icon"},
		"undeclared order":     {func(d *dbdriver.Descriptor) { d.Order = 0 }, "order"},
		"id not a plain word":  {func(d *dbdriver.Descriptor) { d.ID = "Fake DB" }, "id"},
		"nil aliases":          {func(d *dbdriver.Descriptor) { d.Aliases = nil }, "aliases"},
		"alias repeats the id": {func(d *dbdriver.Descriptor) { d.Aliases = []string{"fakedb"} }, "alias"},
		"duplicate alias":      {func(d *dbdriver.Descriptor) { d.Aliases = []string{"fake", "fake"} }, "alias"},
		"no form":              {func(d *dbdriver.Descriptor) { d.Form = nil }, "form"},
		"unknown tab": {func(d *dbdriver.Descriptor) {
			d.Form = append(d.Form, dbdriver.FormTabFields{Tab: "extras", Fields: []dbdriver.FormField{dbdriver.FieldParams}})
		}, "extras"},
		"tab listed twice": {func(d *dbdriver.Descriptor) {
			d.Form = append(d.Form, dbdriver.FormTabFields{Tab: dbdriver.TabAdvanced, Fields: []dbdriver.FormField{dbdriver.FieldParams}})
		}, "advanced"},
		"tab without fields": {func(d *dbdriver.Descriptor) {
			d.Form = append(d.Form, dbdriver.FormTabFields{Tab: dbdriver.TabTLS})
		}, "tls"},
		"unknown field": {func(d *dbdriver.Descriptor) {
			d.Form[0].Fields = append(d.Form[0].Fields, "service_name")
		}, "service_name"},
		"field used twice": {func(d *dbdriver.Descriptor) {
			d.Form[1].Fields = append(d.Form[1].Fields, dbdriver.FieldHost)
		}, "host"},
		"port field without default port": {func(d *dbdriver.Descriptor) { d.DefaultPort = 0 }, "default port"},
		"default port without port field": {func(d *dbdriver.Descriptor) {
			d.Form[0].Fields = []dbdriver.FormField{dbdriver.FieldHost}
		}, "default port"},
		"read-only switch without the capability": {func(d *dbdriver.Descriptor) {
			d.Capabilities = []dbdriver.Capability{dbdriver.CapViews}
		}, "read_only"},
		"foreign key toggle without import": {func(d *dbdriver.Descriptor) { d.CanDisableForeignKeyChecks = true }, "canDisableForeignKeyChecks"},
		"exact row location without grid edit": {func(d *dbdriver.Descriptor) {
			d.LocatesRowWithoutPrimaryKey = true
		}, "locatesRowWithoutPrimaryKey"},
		"alter attributes without alter table": {func(d *dbdriver.Descriptor) {
			d.AlterTableAttributes = []dbdriver.AlterAttribute{dbdriver.AlterColumnType}
		}, "alterTableAttributes"},
		"unknown alter attribute": {func(d *dbdriver.Descriptor) {
			d.Capabilities = append(d.Capabilities, dbdriver.CapAlterTable)
			d.AlterTableAttributes = []dbdriver.AlterAttribute{"column_charset"}
		}, "column_charset"},
		"duplicate alter attribute": {func(d *dbdriver.Descriptor) {
			d.Capabilities = append(d.Capabilities, dbdriver.CapAlterTable)
			d.AlterTableAttributes = []dbdriver.AlterAttribute{dbdriver.AlterColumnType, dbdriver.AlterColumnType}
		}, "column_type"},
		"nil capabilities":     {func(d *dbdriver.Descriptor) { d.Capabilities = nil }, "capabilities"},
		"unknown capability":   {func(d *dbdriver.Descriptor) { d.Capabilities = append(d.Capabilities, "teleport") }, "teleport"},
		"duplicate capability": {func(d *dbdriver.Descriptor) { d.Capabilities = append(d.Capabilities, dbdriver.CapViews) }, "views"},
	} {
		t.Run(name, func(t *testing.T) {
			fake := NewFake()
			tt.mutate(&fake.Desc)
			problems := descriptorProblems(fake.Desc)
			require.NotEmpty(t, problems)
			assert.Contains(t, strings.Join(problems, "\n"), tt.want)
		})
	}
}

func TestConformanceRequiresTheDriverToBeRegistered(t *testing.T) {
	assert.NotEmpty(t, registrationProblems(NewFake()))
}

func TestFakeDriverPassesBrowseConformance(t *testing.T) {
	fake := NewFake()
	Register(t, fake)
	RunBrowseConformance(t, fake, FakeSessions(fake))
}

// lenientSession 回答任何对象类型的源码查询，不管驱动声明了什么。
type lenientSession struct{ dbdriver.Session }

func (lenientSession) ObjectSource(context.Context, string, string, string, string) (string, error) {
	return "", nil
}

// 浏览的一致性检查也得能失败：没声明的对象类型被放行时要点出来。
func TestConformanceReportsAnUndeclaredObjectTypeBeingServed(t *testing.T) {
	fake := NewFake() // 只声明了视图
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "fakedb", Host: "h"}})
	require.NoError(t, err)

	problems := sessionProblems(fake.Desc, lenientSession{session})
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "\n"), `ObjectSource("procedure")`)
	assert.NotContains(t, strings.Join(problems, "\n"), `ObjectSource("view")`)
}

// 建表语句的声明与行为必须一致：声明了查看建表语句的驱动不能拒绝它，没声明的不能放行。
func TestConformanceChecksTableDDLAgainstTheDeclaration(t *testing.T) {
	open := func(t *testing.T, fake *Fake) dbdriver.Session {
		t.Helper()
		session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "fakedb", Host: "h"}})
		require.NoError(t, err)
		return session
	}

	declared := NewFake()
	declared.Desc.Capabilities = append(declared.Desc.Capabilities, dbdriver.CapViewDDL)
	assert.Empty(t, browseProblems(declared.Desc, open(t, declared)), "a declared and served feature is conforming")

	// 声明了却被拒绝。
	refusing := NewFake()
	refusing.Desc.Capabilities = append(refusing.Desc.Capabilities, dbdriver.CapViewDDL)
	problems := browseProblems(refusing.Desc, ddlSession{Session: open(t, refusing), err: dbdriver.ErrUnsupported})
	assert.Contains(t, strings.Join(problems, "\n"), "TableDDL")

	// 声明了，表在，却给不出语句。
	problems = browseProblems(refusing.Desc, ddlSession{Session: open(t, refusing)})
	assert.Contains(t, strings.Join(problems, "\n"), "no statement")

	// 没声明却被放行。
	undeclared := NewFake()
	problems = sessionProblems(undeclared.Desc, ddlSession{Session: open(t, undeclared), ddl: "CREATE TABLE x ()"})
	assert.Contains(t, strings.Join(problems, "\n"), "TableDDL")
}

// 没声明能关闭外键检查的驱动，会话放行关闭外键检查时要点出来。
func TestConformanceReportsAnUndeclaredForeignKeyToggleBeingServed(t *testing.T) {
	fake := NewFake()
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &dbdriver.Config{Driver: "fakedb", Host: "h"}})
	require.NoError(t, err)

	problems := sessionProblems(fake.Desc, lenientBatchSession{session})
	assert.Contains(t, strings.Join(problems, "\n"), "ExecuteBatch")
}

// lenientBatchSession 执行任何批，不管驱动声明了什么。
type lenientBatchSession struct{ dbdriver.Session }

func (lenientBatchSession) ExecuteBatch(context.Context, dbdriver.BatchRequest) (*dbdriver.BatchResult, error) {
	return &dbdriver.BatchResult{Executed: []dbdriver.BatchStatement{}}, nil
}

// ddlSession 让 TableDDL 的回答由测试决定，不管驱动声明了什么。
type ddlSession struct {
	dbdriver.Session
	ddl string
	err error
}

func (s ddlSession) TableDDL(context.Context, string, string) (string, error) { return s.ddl, s.err }

// 列元数据必须带 comment（字符串）与 default（字符串或 null）两个键；缺了或类型不对要点出来。
func TestConformanceChecksColumnCommentAndDefaultShape(t *testing.T) {
	assert.Empty(t, columnShapeProblems([]byte(`{"name":"id","comment":"","default":null}`)))
	assert.Empty(t, columnShapeProblems([]byte(`{"name":"id","comment":"c","default":""}`)))

	problems := strings.Join(columnShapeProblems([]byte(`{"name":"id"}`)), "\n")
	assert.Contains(t, problems, "comment")
	assert.Contains(t, problems, "default")

	problems = strings.Join(columnShapeProblems([]byte(`{"comment":null,"default":1}`)), "\n")
	assert.Contains(t, problems, "comment")
	assert.Contains(t, problems, "default")
}
