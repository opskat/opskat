package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/pkg/shellutil"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

func TestSplitTemplateArgv(t *testing.T) {
	tests := []struct {
		name, template string
		want           []string
	}{
		{"simple", "aws --region {{region}} --output json", []string{"aws", "--region", "{{region}}", "--output", "json"}},
		{"adjacent, no space", "--region={{region}}", []string{"--region={{region}}"}},
		{"whitespace inside expr is not a delimiter",
			`aws --tag {{ name + " " + env }}`, []string{"aws", "--tag", `{{ name + " " + env }}`}},
		{"extra whitespace collapses", "  aws   s3  ", []string{"aws", "s3"}},
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"unterminated expr swallows the rest, including whitespace", "aws {{unterminated rest", []string{"aws", "{{unterminated rest"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitTemplateArgv(tt.template))
		})
	}
}

func TestBuildShellCommandArgv(t *testing.T) {
	tests := []struct {
		name, goos, shell, command string
		want                       []string
	}{
		{"unix uses -c", "linux", "/bin/sh", "ls -la | grep foo", []string{"/bin/sh", "-c", "ls -la | grep foo"}},
		{"darwin uses -c", "darwin", "/bin/zsh", "echo hi", []string{"/bin/zsh", "-c", "echo hi"}},
		{"windows cmd.exe uses /C", "windows", `C:\Windows\System32\cmd.exe`, "dir", []string{`C:\Windows\System32\cmd.exe`, "/C", "dir"}},
		{"windows pwsh uses -Command", "windows", `C:\pwsh\pwsh.exe`, "Get-Item .", []string{`C:\pwsh\pwsh.exe`, "-Command", "Get-Item ."}},
		{"windows powershell (any case) uses -Command", "windows", `C:\Windows\PowerShell.exe`, "dir", []string{`C:\Windows\PowerShell.exe`, "-Command", "dir"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildShellCommandArgv(tt.goos, tt.shell, tt.command))
		})
	}
}

func TestCanonicalizeCommandMode(t *testing.T) {
	tests := []struct {
		name, command, want string
		wantErr             bool
	}{
		{"simple words", "s3 ls s3://bucket", "s3 ls s3://bucket", false},
		{"quoted arg unwraps and stays one word", `s3 cp 'my file.txt' dst`, "s3 cp my file.txt dst", false},
		{"whole shell command wrapped as one quoted word unwraps to itself",
			`'ls -la | grep foo'`, "ls -la | grep foo", false},
		{"an unquoted pipe at this layer is rejected", "ls -la | grep foo", "", true},
		{"empty command errors", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := canonicalizeCommandMode(tt.command)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDescribeCommandMode_NoTemplateShowsShellNotAnyInjectedValue(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "shell-box", Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{},
	}))
	asset := genericAsset(t, "shell-box", nil)

	detail, err := DescribeGenericCommand(ctx, asset, "echo hi")
	require.NoError(t, err)
	assert.Contains(t, detail, shellutil.DefaultShell())
}

func TestDescribeCommandMode_TemplateShowsStaticProgramNotRenderedSecret(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "profile"}, {Name: "token", Secret: true}},
		Command: &custom_type_entity.CommandConfig{Template: "aws --profile {{profile}} --token {{token}}"},
	}))
	asset := genericAsset(t, "cli", map[string]string{"profile": "prod", "token": testSecret})

	detail, err := DescribeGenericCommand(ctx, asset, "s3 ls")
	require.NoError(t, err)
	assert.Equal(t, "Local command: aws", detail)
	assert.NotContains(t, detail, "prod")
	assert.NotContains(t, detail, testSecret)

	// argv[0] itself templated: showing the rendered value could leak a secret field,
	// so the static (unrendered) token must not be shown as if it were a literal program.
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "dyn", Slug: "dyn", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "token", Secret: true}},
		Command: &custom_type_entity.CommandConfig{Template: "{{token}}"},
	}))
	dynAsset := genericAsset(t, "dyn", map[string]string{"token": testSecret})
	detail, err = DescribeGenericCommand(ctx, dynAsset, "anything")
	require.NoError(t, err)
	assert.NotContains(t, detail, testSecret)
}

func TestGenericCommand_MissingRequiredFieldBlocksExecutionBeforeAnyProcess(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli-missing", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "profile", Required: true}},
		Command: &custom_type_entity.CommandConfig{Template: "/does/not/exist --profile {{profile}}"},
	}))
	asset := genericAsset(t, "cli-missing", nil)

	_, err := ExecGenericOnAsset(ctx, asset, "s3 ls", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing required field")
	assert.NotContains(t, err.Error(), "/does/not/exist", "must fail on the missing field, before ever touching the program")
}
