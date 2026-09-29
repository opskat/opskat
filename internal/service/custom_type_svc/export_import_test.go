package custom_type_svc

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/model/entity/policy"
)

func TestExportType_RoundTrip(t *testing.T) {
	ct := grafanaType()
	ct.Icon = "server"
	ct.Usage = "先 opsctl help grafana"
	ct.DefaultPolicy = &policy.CommandPolicy{AllowList: []string{"GET *"}}
	// 数据库字段不该影响导出内容。
	ct.ID = 42
	ct.Createtime = 1
	ct.Updatetime = 2

	data, err := ExportType(ct)
	require.NoError(t, err)

	got, err := ParseImportFile(data)
	require.NoError(t, err)
	assert.Equal(t, ct.Name, got.Name)
	assert.Equal(t, ct.Slug, got.Slug)
	assert.Equal(t, ct.Icon, got.Icon)
	assert.Equal(t, ct.ExecMode, got.ExecMode)
	assert.Equal(t, ct.Fields, got.Fields)
	assert.Equal(t, ct.HTTP, got.HTTP)
	assert.Equal(t, ct.Usage, got.Usage)
	assert.Equal(t, ct.DefaultPolicy, got.DefaultPolicy)
	assert.Zero(t, got.ID)
	assert.Zero(t, got.Createtime)
	assert.Zero(t, got.Updatetime)
}

func TestExportType_NoDBFieldsOrValues(t *testing.T) {
	ct := grafanaType()
	ct.ID = 42
	ct.Createtime = 1
	ct.Updatetime = 2

	data, err := ExportType(ct)
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &m))
	for _, key := range []string{"id", "createtime", "updatetime", "values", "credential_id"} {
		_, present := m[key]
		assert.False(t, present, "exported file must not contain %q", key)
	}
	assert.Contains(t, m, "format")
	assert.Contains(t, m, "version")
}

func TestParseImportFile_UnknownFormat(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"format": "something-else", "version": ExportFormatVersion})
	require.NoError(t, err)

	_, err = ParseImportFile(raw)
	require.Error(t, err)
	var verr *custom_type_entity.ValidationError
	require.True(t, errors.As(err, &verr))
	assert.Equal(t, []custom_type_entity.Issue{
		{Path: "format", Code: "format_unknown", Params: map[string]string{"format": "something-else"}},
	}, verr.Issues)
}

func TestParseImportFile_UnknownVersion(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"format": ExportFormat, "version": 999})
	require.NoError(t, err)

	_, err = ParseImportFile(raw)
	require.Error(t, err)
	var verr *custom_type_entity.ValidationError
	require.True(t, errors.As(err, &verr))
	assert.Equal(t, []custom_type_entity.Issue{
		{Path: "format", Code: "version_unsupported", Params: map[string]string{"version": "999", "supported": "1"}},
	}, verr.Issues)
}

func TestParseImportFile_UnregisteredAuthType(t *testing.T) {
	ct := grafanaType()
	ct.HTTP.Auth[0].Type = "totally-unregistered-auth-type"
	data, err := ExportType(ct)
	require.NoError(t, err)

	_, err = ParseImportFile(data)
	require.Error(t, err)
	var verr *custom_type_entity.ValidationError
	require.True(t, errors.As(err, &verr))
	found := false
	for _, is := range verr.Issues {
		if is.Path == "http.auth[0].type" {
			found = true
			assert.Equal(t, "auth_type_unknown", is.Code)
		}
	}
	assert.True(t, found, "expected an issue on http.auth[0].type, got %+v", verr.Issues)
}

func TestParseImportFile_UnknownFunction(t *testing.T) {
	ct := grafanaType()
	ct.ExecMode = custom_type_entity.ExecModeCommand
	ct.HTTP = nil
	ct.Command = &custom_type_entity.CommandConfig{Template: `mycli --token {{unknownfunc(token)}}`}
	data, err := ExportType(ct)
	require.NoError(t, err)

	_, err = ParseImportFile(data)
	require.Error(t, err)
	var verr *custom_type_entity.ValidationError
	require.True(t, errors.As(err, &verr))
	found := false
	for _, is := range verr.Issues {
		if is.Path == "command.template" {
			found = true
			assert.Equal(t, "template.unknown_function", is.Code)
		}
	}
	assert.True(t, found, "expected an issue on command.template, got %+v", verr.Issues)
}

func TestParseImportFile_MalformedJSON(t *testing.T) {
	_, err := ParseImportFile([]byte("not json"))
	require.Error(t, err)
	var verr *custom_type_entity.ValidationError
	assert.False(t, errors.As(err, &verr), "malformed JSON should not surface as a structured validation error")
}
