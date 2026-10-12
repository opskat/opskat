package dbdrivertest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/dbdriver"
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
