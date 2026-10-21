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
