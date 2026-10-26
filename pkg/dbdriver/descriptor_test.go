package dbdriver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func descriptorWithFields(fields ...DriverField) Descriptor {
	return Descriptor{ID: "extdb", Name: "ExtDB", DriverFields: fields}
}

func TestDescriptorValidateDriverFields(t *testing.T) {
	ok := DriverField{Key: "region", Type: FieldTypeText, Tab: TabAdvanced}
	cases := []struct {
		name    string
		fields  []DriverField
		wantErr string // empty: valid
	}{
		{"valid one of each type", []DriverField{
			{Key: "a", Type: FieldTypeText, Tab: TabConnection, Default: "x"},
			{Key: "b", Type: FieldTypeNumber, Tab: TabTunnel, Default: 3},
			{Key: "c", Type: FieldTypeSwitch, Tab: TabTLS, Default: true},
			{Key: "d", Type: FieldTypeEnum, Tab: TabAdvanced, Enum: []string{"p", "q"}, Default: "q"},
			{Key: "e", Type: FieldTypePassword, Tab: TabConnection, Required: true},
			{Key: "f", Type: FieldTypeTextarea, Tab: TabAdvanced},
		}, ""},
		{"key equals built-in form field", []DriverField{{Key: "host", Type: FieldTypeText, Tab: TabConnection}}, `"host"`},
		{"every built-in form field collides", []DriverField{{Key: string(FieldQueryTimeout), Type: FieldTypeText, Tab: TabConnection}}, "query_timeout_seconds"},
		{"empty key", []DriverField{{Type: FieldTypeText, Tab: TabConnection}}, "key"},
		{"duplicate key", []DriverField{ok, ok}, `"region"`},
		{"unknown type", []DriverField{{Key: "k", Type: "color", Tab: TabConnection}}, `"k"`},
		{"unknown tab", []DriverField{{Key: "k", Type: FieldTypeText, Tab: "extra"}}, `"k"`},
		{"enum without values", []DriverField{{Key: "k", Type: FieldTypeEnum, Tab: TabAdvanced}}, `"k"`},
		{"enum values on non-enum", []DriverField{{Key: "k", Type: FieldTypeText, Tab: TabAdvanced, Enum: []string{"a"}}}, `"k"`},
		{"default not in enum", []DriverField{{Key: "k", Type: FieldTypeEnum, Tab: TabAdvanced, Enum: []string{"a"}, Default: "z"}}, `"k"`},
		{"default wrong type", []DriverField{{Key: "k", Type: FieldTypeNumber, Tab: TabAdvanced, Default: "one"}}, `"k"`},
		{"password cannot have a default", []DriverField{{Key: "k", Type: FieldTypePassword, Tab: TabAdvanced, Default: "pw"}}, `"k"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := descriptorWithFields(tc.fields...).Validate()
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), "extdb", "错误点名驱动 id")
		})
	}
}

func TestDriverOptionsChecking(t *testing.T) {
	d := descriptorWithFields(
		DriverField{Key: "region", Type: FieldTypeText, Tab: TabConnection, Required: true},
		DriverField{Key: "retries", Type: FieldTypeNumber, Tab: TabAdvanced, Default: 3},
		DriverField{Key: "fast", Type: FieldTypeSwitch, Tab: TabAdvanced},
		DriverField{Key: "mode", Type: FieldTypeEnum, Tab: TabAdvanced, Enum: []string{"a", "b"}, Default: "a"},
		DriverField{Key: "token", Type: FieldTypePassword, Tab: TabConnection},
	)

	t.Run("unknown key is rejected and named", func(t *testing.T) {
		err := d.CheckDriverOptions(map[string]any{"regoin": "x"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"regoin"`)
	})
	t.Run("wrong value types are rejected and named", func(t *testing.T) {
		for key, bad := range map[string]any{"region": 1, "retries": "3", "fast": "yes", "mode": "c", "token": true} {
			err := d.CheckDriverOptions(map[string]any{key: bad})
			require.Error(t, err, key)
			assert.Contains(t, err.Error(), `"`+key+`"`)
		}
	})
	t.Run("accepts numbers decoded as int, int64 or float64", func(t *testing.T) {
		for _, n := range []any{1, int64(1), float64(1)} {
			require.NoError(t, d.CheckDriverOptions(map[string]any{"retries": n}))
		}
		assert.Error(t, d.CheckDriverOptions(map[string]any{"retries": 1.5}), "number 为整数")
	})
	t.Run("complete fills defaults and keeps given values", func(t *testing.T) {
		got, err := d.CompleteDriverOptions(map[string]any{"region": "eu", "mode": "b"})
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"region": "eu", "retries": 3, "mode": "b"}, got)
	})
	t.Run("complete rejects a missing required field", func(t *testing.T) {
		_, err := d.CompleteDriverOptions(map[string]any{"retries": 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"region"`)
		_, err = d.CompleteDriverOptions(map[string]any{"region": ""})
		require.Error(t, err, "必填的文本为空串算缺")
	})
	t.Run("a driver without driver fields rejects any key", func(t *testing.T) {
		err := Descriptor{ID: "plain"}.CheckDriverOptions(map[string]any{"x": 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"x"`)
	})
}

// 证书字段组（FieldTLSCert）只能声明在 TLS Tab；它自带启用 TLS 的开关，所以不与单独的 tls
// 开关同时声明。
func TestDescriptorValidateTLSCertGroup(t *testing.T) {
	withForm := func(tabs ...FormTabFields) Descriptor {
		return Descriptor{ID: "extdb", Name: "ExtDB", Form: tabs}
	}
	t.Run("declared on the TLS tab", func(t *testing.T) {
		d := withForm(FormTabFields{Tab: TabTLS, Fields: []FormField{FieldTLSCert}})
		require.NoError(t, d.Validate())
		assert.True(t, d.UsesField(FieldTLSCert))
	})
	t.Run("declared on another tab", func(t *testing.T) {
		err := withForm(FormTabFields{Tab: TabAdvanced, Fields: []FormField{FieldTLSCert}}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extdb")
		assert.Contains(t, err.Error(), string(FieldTLSCert))
	})
	t.Run("declared together with the separate tls switch", func(t *testing.T) {
		err := withForm(
			FormTabFields{Tab: TabAdvanced, Fields: []FormField{FieldTLS}},
			FormTabFields{Tab: TabTLS, Fields: []FormField{FieldTLSCert}},
		).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extdb")
		assert.Contains(t, err.Error(), `"tls"`)
	})
	t.Run("a driver field may not take the group's name", func(t *testing.T) {
		err := descriptorWithFields(DriverField{Key: string(FieldTLSCert), Type: FieldTypeText, Tab: TabTLS}).Validate()
		require.Error(t, err)
	})
}

// Kerberos 字段组（FieldKerberos）只能声明在连接 Tab：认证方式与用户名、口令同处。
func TestDescriptorValidateKerberosGroup(t *testing.T) {
	withForm := func(tabs ...FormTabFields) Descriptor {
		return Descriptor{ID: "extdb", Name: "ExtDB", Form: tabs}
	}
	t.Run("declared on the connection tab", func(t *testing.T) {
		d := withForm(FormTabFields{Tab: TabConnection, Fields: []FormField{FieldHost, FieldUsername, FieldPassword, FieldKerberos}})
		require.NoError(t, d.Validate())
		assert.True(t, d.UsesField(FieldKerberos))
	})
	t.Run("declared on another tab", func(t *testing.T) {
		err := withForm(FormTabFields{Tab: TabAdvanced, Fields: []FormField{FieldKerberos}}).Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "extdb")
		assert.Contains(t, err.Error(), string(FieldKerberos))
		assert.Contains(t, err.Error(), string(TabConnection))
	})
	t.Run("a driver field may not take the group's name", func(t *testing.T) {
		err := descriptorWithFields(DriverField{Key: string(FieldKerberos), Type: FieldTypeText, Tab: TabConnection}).Validate()
		require.Error(t, err)
	})
}

// 表设计器的细项声明：常用类型与名称折叠写错了，描述就不合法，错误点名驱动 id 与出问题的地方。
func TestDescriptorValidateColumnDeclarations(t *testing.T) {
	valid := func() Descriptor {
		return Descriptor{ID: "extdb", Name: "ExtDB", ColumnTypes: &ColumnTypes{
			Groups: []ColumnTypeGroup{
				{Label: "typeGroup.numeric", Types: []string{"INT", "BIGINT"}},
				{Label: "typeGroup.text", Types: []string{"VARCHAR(255)", "TEXT"}},
			},
			NewColumn: "VARCHAR(255)", PrimaryKey: "BIGINT",
		}, NameFolding: NameFoldUpper, ExplicitDefaults: true, AutoIncrement: true}
	}
	require.NoError(t, valid().Validate())
	require.NoError(t, Descriptor{ID: "extdb", ColumnTypes: &ColumnTypes{NewColumn: "TEXT"}}.Validate(), "只声明预填类型、没有下拉也可以")
	require.NoError(t, Descriptor{ID: "extdb", NameFolding: NameFoldLower}.Validate())

	for name, tc := range map[string]struct {
		mutate  func(*Descriptor)
		wantErr string
	}{
		"column types declaring nothing": {func(d *Descriptor) { d.ColumnTypes = &ColumnTypes{} }, "column types"},
		"group without label":            {func(d *Descriptor) { d.ColumnTypes.Groups[0].Label = " " }, "label"},
		"group without types":            {func(d *Descriptor) { d.ColumnTypes.Groups[1].Types = nil }, "typeGroup.text"},
		"blank type":                     {func(d *Descriptor) { d.ColumnTypes.Groups[0].Types[1] = "  " }, "typeGroup.numeric"},
		"type with surrounding blanks":   {func(d *Descriptor) { d.ColumnTypes.Groups[0].Types[1] = " BIGINT" }, `" BIGINT"`},
		"type listed twice":              {func(d *Descriptor) { d.ColumnTypes.Groups[1].Types[1] = "int" }, `"int"`},
		"blank new column type":          {func(d *Descriptor) { d.ColumnTypes.NewColumn = " " }, "new column"},
		"primary key type with blanks":   {func(d *Descriptor) { d.ColumnTypes.PrimaryKey = "BIGINT " }, "primary key"},
		"unknown name folding":           {func(d *Descriptor) { d.NameFolding = "title" }, `"title"`},
	} {
		t.Run(name, func(t *testing.T) {
			d := valid()
			tc.mutate(&d)
			err := d.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), "extdb", "错误点名驱动 id")
		})
	}
}

// 常用类型的分组名与专属字段的标签一样可以是扩展多语言里的键：Localized 一并翻译，不改原描述。
func TestDescriptorLocalizedTranslatesColumnTypeGroups(t *testing.T) {
	d := Descriptor{ID: "extdb", ColumnTypes: &ColumnTypes{
		Groups:    []ColumnTypeGroup{{Label: "types.numeric", Types: []string{"NUMBER"}}},
		NewColumn: "VARCHAR2(255)",
	}}
	got := d.Localized(func(key string) string { return "<" + key + ">" })
	assert.Equal(t, &ColumnTypes{
		Groups:    []ColumnTypeGroup{{Label: "<types.numeric>", Types: []string{"NUMBER"}}},
		NewColumn: "VARCHAR2(255)",
	}, got.ColumnTypes)
	assert.Equal(t, "types.numeric", d.ColumnTypes.Groups[0].Label, "原描述不变")
}

// 请求里的列只能用驱动声明了的能力：没声明的以 ErrUnsupported 拒绝；写法本身不合法的是普通错误。
func TestColumnFeaturesCheck(t *testing.T) {
	plain := ColumnDefinition{Name: "name", Type: "TEXT", DefaultValue: "anon"}
	withKind := func(kind DefaultKind, value string) ColumnDefinition {
		return ColumnDefinition{Name: "c", Type: "TEXT", DefaultKind: kind, DefaultValue: value}
	}
	autoIncrement := ColumnDefinition{Name: "id", Type: "INT", AutoIncrement: true}

	t.Run("a driver that declares nothing takes the request as it always has", func(t *testing.T) {
		require.NoError(t, ColumnFeatures{}.Check(plain))
	})
	t.Run("an undeclared default kind is unsupported", func(t *testing.T) {
		for _, kind := range DefaultKinds() {
			err := ColumnFeatures{AutoIncrement: true}.Check(plain, withKind(kind, ""))
			require.ErrorIs(t, err, ErrUnsupported, kind)
			assert.Contains(t, err.Error(), `"c"`)
		}
	})
	t.Run("an undeclared auto-increment is unsupported", func(t *testing.T) {
		err := ColumnFeatures{ExplicitDefaults: true}.Check(autoIncrement)
		require.ErrorIs(t, err, ErrUnsupported)
		assert.Contains(t, err.Error(), `"id"`)
	})
	t.Run("declared features are accepted", func(t *testing.T) {
		require.NoError(t, ColumnFeatures{ExplicitDefaults: true, AutoIncrement: true}.Check(
			plain, autoIncrement,
			withKind(DefaultKindNone, ""), withKind(DefaultKindNull, ""),
			withKind(DefaultKindValue, ""), withKind(DefaultKindValue, " it's "),
			withKind(DefaultKindExpression, "CURRENT_TIMESTAMP"),
		))
	})
	t.Run("a malformed default is an error of its own", func(t *testing.T) {
		declared := ColumnFeatures{ExplicitDefaults: true}
		for name, column := range map[string]ColumnDefinition{
			"unknown kind":      withKind("function", "now()"),
			"none with a value": withKind(DefaultKindNone, "x"),
			"null with a value": withKind(DefaultKindNull, "NULL"),
			"empty expression":  withKind(DefaultKindExpression, " "),
		} {
			err := declared.Check(column)
			require.Error(t, err, name)
			assert.NotErrorIs(t, err, ErrUnsupported, name)
			assert.Contains(t, err.Error(), `"c"`, name)
		}
	})
	t.Run("the descriptor's declarations are its column features", func(t *testing.T) {
		assert.Equal(t, ColumnFeatures{ExplicitDefaults: true}, Descriptor{ExplicitDefaults: true}.ColumnFeatures())
		assert.Equal(t, ColumnFeatures{AutoIncrement: true}, Descriptor{AutoIncrement: true}.ColumnFeatures())
	})
}

// 建表与改表请求里的列连同默认值写法与自增一起交给检查与方言。
func TestRequestDefinitionsCarryTheColumnFeatures(t *testing.T) {
	create := CreateTableRequest{Name: "t", Columns: []NewColumn{
		{Name: "id", Type: "INT", AutoIncrement: true},
		{Name: "at", Type: "TIMESTAMP", Nullable: true, DefaultKind: DefaultKindExpression, DefaultValue: "now()"},
	}}
	assert.Equal(t, []ColumnDefinition{
		{Name: "id", Type: "INT", AutoIncrement: true},
		{Name: "at", Type: "TIMESTAMP", Nullable: true, DefaultKind: DefaultKindExpression, DefaultValue: "now()"},
	}, create.Definitions())

	alter := AlterTableRequest{Columns: []AlterColumn{
		{OriginalName: "a", Name: "b", Type: "TEXT", DefaultKind: DefaultKindValue, DefaultValue: "x", Comment: "c"},
		{Name: "id", Type: "INT", AutoIncrement: true},
	}}
	assert.Equal(t, []ColumnDefinition{
		{Name: "b", Type: "TEXT", DefaultKind: DefaultKindValue, DefaultValue: "x", Comment: "c"},
		{Name: "id", Type: "INT", AutoIncrement: true},
	}, alter.Definitions())
}
