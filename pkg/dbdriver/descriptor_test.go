package dbdriver

import (
	"encoding/json"
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

// 表设计器的细项声明：索引编辑写错了，描述就不合法，错误点名驱动 id 与出问题的地方。
func TestDescriptorValidateIndexEditing(t *testing.T) {
	valid := func() Descriptor {
		return Descriptor{ID: "extdb", Name: "ExtDB", IndexEditing: &IndexEditing{
			Types: []IndexTypeDecl{
				{ID: IndexTypePrimary, Label: "indexType.primary"},
				{ID: IndexTypeUnique, Label: "indexType.unique"},
				{ID: IndexTypeNormal, Label: "indexType.normal"},
				{ID: "bitmap", Label: "extdb.indexType.bitmap"},
			},
			AlterPrimaryKey: true, Descending: true, PrefixLength: true, Comments: true,
			NameScope: IndexScopeSchema,
			Options: []IndexOptionDecl{
				{Key: "method", Label: "indexOption.method", Type: FieldTypeEnum, Enum: []string{"btree", "hash"}},
				{Key: "clustered", Label: "indexOption.clustered", Type: FieldTypeSwitch},
			},
		}}
	}
	require.NoError(t, valid().Validate())
	require.NoError(t, Descriptor{ID: "extdb", IndexEditing: &IndexEditing{
		Types: []IndexTypeDecl{{ID: IndexTypeUnique, Label: "indexType.unique"}}, NameScope: IndexScopeTable,
	}}.Validate(), "只声明一个类型、没有选项也可以")

	for name, tc := range map[string]struct {
		mutate  func(*Descriptor)
		wantErr string
	}{
		"no type at all":          {func(d *Descriptor) { d.IndexEditing.Types = nil }, "no type"},
		"unknown name scope":      {func(d *Descriptor) { d.IndexEditing.NameScope = "tablespace" }, `"tablespace"`},
		"type without id":         {func(d *Descriptor) { d.IndexEditing.Types[2].ID = " " }, "empty id"},
		"type id with blanks":     {func(d *Descriptor) { d.IndexEditing.Types[3].ID = " bitmap" }, `" bitmap"`},
		"type without label":      {func(d *Descriptor) { d.IndexEditing.Types[1].Label = "" }, `"unique"`},
		"type listed twice":       {func(d *Descriptor) { d.IndexEditing.Types[3].ID = IndexTypeUnique }, "more than once"},
		"option without key":      {func(d *Descriptor) { d.IndexEditing.Options[0].Key = " " }, "empty key"},
		"option key listed twice": {func(d *Descriptor) { d.IndexEditing.Options[1].Key = "method" }, `"method"`},
		"option without label":    {func(d *Descriptor) { d.IndexEditing.Options[0].Label = " " }, `"method"`},
		"option of a wrong type":  {func(d *Descriptor) { d.IndexEditing.Options[0].Type = FieldTypeText }, `"method"`},
		"enum without values":     {func(d *Descriptor) { d.IndexEditing.Options[0].Enum = nil }, `"method"`},
		"enum with a blank value": {func(d *Descriptor) { d.IndexEditing.Options[0].Enum[1] = " " }, `"method"`},
		"switch with enum values": {func(d *Descriptor) { d.IndexEditing.Options[1].Enum = []string{"on"} }, "clustered"},
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

// 索引类型与选项的显示名同常用类型的分组名一样可以是扩展多语言里的键：Localized 一并翻译，不改原描述。
func TestDescriptorLocalizedTranslatesIndexEditing(t *testing.T) {
	d := Descriptor{ID: "extdb", IndexEditing: &IndexEditing{
		Types:     []IndexTypeDecl{{ID: "bitmap", Label: "extdb.indexType.bitmap"}},
		NameScope: IndexScopeTable,
		Options:   []IndexOptionDecl{{Key: "method", Label: "extdb.indexOption.method", Type: FieldTypeEnum, Enum: []string{"btree"}}},
	}}
	got := d.Localized(func(key string) string { return "<" + key + ">" })
	assert.Equal(t, &IndexEditing{
		Types:     []IndexTypeDecl{{ID: "bitmap", Label: "<extdb.indexType.bitmap>"}},
		NameScope: IndexScopeTable,
		Options:   []IndexOptionDecl{{Key: "method", Label: "<extdb.indexOption.method>", Type: FieldTypeEnum, Enum: []string{"btree"}}},
	}, got.IndexEditing)
	assert.Equal(t, "extdb.indexType.bitmap", d.IndexEditing.Types[0].Label, "原描述不变")
}

// declaredIndexEditing 是检查用的声明：主键、唯一、普通三个类型，主键可改、可降序、可注释，
// 方法枚举与聚集开关两个额外选项。
func declaredIndexEditing() *IndexEditing {
	return &IndexEditing{
		Types: []IndexTypeDecl{
			{ID: IndexTypePrimary, Label: "indexType.primary"},
			{ID: IndexTypeUnique, Label: "indexType.unique"},
			{ID: IndexTypeNormal, Label: "indexType.normal"},
		},
		AlterPrimaryKey: true, Descending: true, Comments: true, NameScope: IndexScopeTable,
		Options: []IndexOptionDecl{
			{Key: "method", Label: "indexOption.method", Type: FieldTypeEnum, Enum: []string{"btree", "hash"}},
			{Key: "clustered", Label: "indexOption.clustered", Type: FieldTypeSwitch},
		},
	}
}

func uniqueOn(columns ...IndexColumn) IndexDefinition {
	return IndexDefinition{Name: "ix", Type: IndexTypeUnique, Columns: columns}
}

// 要写出的索引只能用声明了的能力：类型、降序、前缀长度、注释与选项没声明的以 ErrUnsupported 拒绝，
// INCLUDE 列与表达式列读得出写不了；选项值不符合声明是普通错误。
func TestIndexEditingCheck(t *testing.T) {
	declared := declaredIndexEditing()
	withOptions := func(options map[string]any) IndexDefinition {
		index := uniqueOn(IndexColumn{Name: "id"})
		index.Options = options
		return index
	}

	t.Run("a driver that declares nothing takes the request as it always has", func(t *testing.T) {
		require.NoError(t, (*IndexEditing)(nil).Check())
		require.NoError(t, (&IndexEditing{}).Check())
	})
	t.Run("any index is unsupported without the declaration", func(t *testing.T) {
		for _, editing := range []*IndexEditing{nil, {}} {
			err := editing.Check(uniqueOn(IndexColumn{Name: "id"}))
			require.ErrorIs(t, err, ErrUnsupported)
			assert.Contains(t, err.Error(), `"unique"`)
		}
	})
	t.Run("a declared index is accepted", func(t *testing.T) {
		index := uniqueOn(IndexColumn{Name: "id", Descending: true}, IndexColumn{Name: "name"})
		index.Comment = "lookup"
		index.Options = map[string]any{"method": "hash", "clustered": true}
		require.NoError(t, declared.Check(index))
	})
	t.Run("undeclared features are unsupported", func(t *testing.T) {
		for name, index := range map[string]IndexDefinition{
			"undeclared type":       {Name: "ix", Type: IndexTypeFullText, Columns: []IndexColumn{{Name: "name"}}},
			"descending column":     uniqueOn(IndexColumn{Name: "id", Descending: true}),
			"prefix length":         uniqueOn(IndexColumn{Name: "name", PrefixLength: 12}),
			"index comment":         func() IndexDefinition { i := uniqueOn(IndexColumn{Name: "id"}); i.Comment = "why"; return i }(),
			"INCLUDE columns":       func() IndexDefinition { i := uniqueOn(IndexColumn{Name: "id"}); i.Include = []string{"name"}; return i }(),
			"expression column":     uniqueOn(IndexColumn{Name: "(a + b)", Expression: true}),
			"undeclared option key": withOptions(map[string]any{"compression": "zlib"}),
		} {
			plain := declaredIndexEditing()
			plain.Descending, plain.Comments = false, false
			err := plain.Check(index)
			require.ErrorIs(t, err, ErrUnsupported, name)
			assert.Contains(t, err.Error(), "ix", name)
		}
	})
	t.Run("a malformed option value is an error of its own", func(t *testing.T) {
		for name, index := range map[string]IndexDefinition{
			"enum value not offered":  withOptions(map[string]any{"method": "gin"}),
			"enum value not a string": withOptions(map[string]any{"method": true}),
			"switch not a boolean":    withOptions(map[string]any{"clustered": "yes"}),
		} {
			err := declared.Check(index)
			require.Error(t, err, name)
			assert.NotErrorIs(t, err, ErrUnsupported, name)
			assert.Contains(t, err.Error(), "ix", name)
		}
	})
}

// 改表请求里的索引在会话与宿主转给扩展之前按声明检查：没声明索引编辑时任何索引改动都不支持；
// 声明了时新建的索引按 Check 检查、新建主键还要声明了主键可改；已有的索引与现状的比较（只读、
// 主键、随删列）要读了表才知道，由会话做。
func TestIndexEditingCheckAlterIndexes(t *testing.T) {
	newUnique := AlterIndex{IndexDefinition: uniqueOn(IndexColumn{Name: "id"})}
	newPrimary := AlterIndex{IndexDefinition: IndexDefinition{Name: "pk", Type: IndexTypePrimary, Columns: []IndexColumn{{Name: "id"}}}}
	keptPrimary := AlterIndex{OriginalName: "pk", IndexDefinition: IndexDefinition{Name: "pk", Type: IndexTypePrimary, Columns: []IndexColumn{{Name: "id"}}}}

	t.Run("without the declaration any change is unsupported", func(t *testing.T) {
		require.NoError(t, (*IndexEditing)(nil).CheckAlterIndexes(nil, nil))
		for name, tc := range map[string]struct {
			indexes []AlterIndex
			drops   []string
		}{
			"new index":  {[]AlterIndex{newUnique}, nil},
			"kept index": {[]AlterIndex{keptPrimary}, nil},
			"drops":      {nil, []string{"ix"}},
		} {
			err := (*IndexEditing)(nil).CheckAlterIndexes(tc.indexes, tc.drops)
			require.ErrorIs(t, err, ErrUnsupported, name)
		}
	})
	t.Run("a declared driver takes new indexes and drops", func(t *testing.T) {
		require.NoError(t, declaredIndexEditing().CheckAlterIndexes([]AlterIndex{newUnique, keptPrimary}, []string{"old_ix"}))
	})
	t.Run("a new primary key needs the declaration", func(t *testing.T) {
		declared := declaredIndexEditing()
		declared.AlterPrimaryKey = false
		err := declared.CheckAlterIndexes([]AlterIndex{newPrimary}, nil)
		require.ErrorIs(t, err, ErrUnsupported)
		assert.Contains(t, err.Error(), "primary")
		require.NoError(t, declaredIndexEditing().CheckAlterIndexes([]AlterIndex{newPrimary}, nil))
	})
	t.Run("a new index is checked against the declaration", func(t *testing.T) {
		fullText := newUnique
		fullText.Type = IndexTypeFullText
		err := declaredIndexEditing().CheckAlterIndexes([]AlterIndex{fullText}, nil)
		require.ErrorIs(t, err, ErrUnsupported)
	})
}

// Equal 是改表请求比较索引、判断「原样交回」的规则：类型、名字、列（含降序与前缀）、INCLUDE 列、
// 选项与注释，文本去掉首尾空白；只读原因与不可删除标注是说明，不参与。
func TestIndexDefinitionEqual(t *testing.T) {
	base := func() IndexDefinition {
		return IndexDefinition{
			Name: "ix", Type: IndexTypeUnique,
			Columns: []IndexColumn{{Name: "id", Descending: true}, {Name: "name", PrefixLength: 8}},
			Include: []string{"a"}, Options: map[string]any{"method": "hash", "clustered": true}, Comment: "lookup",
		}
	}
	other := base()
	assert.True(t, base().Equal(other), "相同定义相等")

	for name, mutate := range map[string]func(*IndexDefinition){
		"name":          func(i *IndexDefinition) { i.Name = "other" },
		"type":          func(i *IndexDefinition) { i.Type = IndexTypeNormal },
		"column added":  func(i *IndexDefinition) { i.Columns = append(i.Columns, IndexColumn{Name: "a"}) },
		"column order":  func(i *IndexDefinition) { i.Columns[0], i.Columns[1] = i.Columns[1], i.Columns[0] },
		"descending":    func(i *IndexDefinition) { i.Columns[0].Descending = false },
		"prefix length": func(i *IndexDefinition) { i.Columns[1].PrefixLength = 0 },
		"expression":    func(i *IndexDefinition) { i.Columns[1].Expression = true },
		"include":       func(i *IndexDefinition) { i.Include = []string{"b"} },
		"option value":  func(i *IndexDefinition) { i.Options["method"] = "btree" },
		"option added":  func(i *IndexDefinition) { i.Options["other"] = "x" },
		"comment":       func(i *IndexDefinition) { i.Comment = "why" },
	} {
		changed := base()
		mutate(&changed)
		assert.False(t, base().Equal(changed), name)
	}

	// 文本去首尾空白；说明性的标注不参与。
	padded := base()
	padded.Name, padded.Comment = " ix ", " lookup "
	padded.Columns[0].Name = " id "
	assert.True(t, base().Equal(padded), "padded")
	annotated := base()
	annotated.ReadOnlyReason, annotated.Undroppable = "partial index", true
	assert.True(t, base().Equal(annotated), "annotated")
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

	// 建表的新列带注释：建表请求里的列注释与表注释、索引一并交给方言。
	withComment := NewColumn{Name: "memo", Type: "TEXT", Nullable: true, Comment: "free text"}
	assert.Equal(t, ColumnDefinition{Name: "memo", Type: "TEXT", Nullable: true, Comment: "free text"}, withComment.Definition())
}

// AlterIndex 是原有名加改完的定义：Definition 交回内嵌的定义，序列化时字段平铺（同 AlterColumn）。
func TestAlterIndexCarriesItsDefinition(t *testing.T) {
	index := AlterIndex{OriginalName: "ix", IndexDefinition: IndexDefinition{
		Name: "ix2", Type: IndexTypeUnique, Columns: []IndexColumn{{Name: "id", Descending: true}},
		Options: map[string]any{"method": "hash"}, Comment: "lookup",
	}}
	assert.Equal(t, IndexDefinition{
		Name: "ix2", Type: IndexTypeUnique, Columns: []IndexColumn{{Name: "id", Descending: true}},
		Options: map[string]any{"method": "hash"}, Comment: "lookup",
	}, index.Definition())

	raw, err := json.Marshal(index)
	require.NoError(t, err)
	assert.Equal(t, `{"originalName":"ix","name":"ix2","type":"unique","columns":[{"name":"id","descending":true}],"options":{"method":"hash"},"comment":"lookup"}`, string(raw))
}

// 表结构语句可回滚是可选声明：没声明的描述序列化时不带这个键（按旧版本构建的扩展、前端内置驱动
// 夹具看到的描述不变），声明了的带上它，前端据此知道保存是整批原子执行。
func TestDescriptorTransactionalDDLIsOmittedUnlessDeclared(t *testing.T) {
	raw, err := json.Marshal(Descriptor{ID: "extdb"})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "transactionalDDL")

	raw, err = json.Marshal(Descriptor{ID: "extdb", TransactionalDDL: true})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"transactionalDDL":true`)

	var decoded Descriptor
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.True(t, decoded.TransactionalDDL)
}
