package dbdrivertest

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
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
	session, err := fake.Open(context.Background(), dbdriver.OpenRequest{Config: &asset_entity.DatabaseConfig{Driver: "fakedb", Host: "h"}})
	require.NoError(t, err)

	problems := sessionProblems(fake.Desc, lenientSession{session})
	require.NotEmpty(t, problems)
	assert.Contains(t, strings.Join(problems, "\n"), `ObjectSource("procedure")`)
	assert.NotContains(t, strings.Join(problems, "\n"), `ObjectSource("view")`)
}
