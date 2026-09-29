//go:build !windows

package helper

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/custom_type_entity"
	"github.com/opskat/opskat/internal/service/custom_type_svc"
)

// writeArgvEnvScript 写一个可执行脚本：先打印每个 argv（用 <> 包裹，argv 边界肉眼可辨），
// 再打印 "ENV <name>=<value>"（只挑测试关心的变量名，避免继承的环境变量把断言弄脏），
// 再打印当前工作目录，最后按 exitCode 退出。本文件的 `!windows` 编译约束已经把这份
// unix shell 脚本 fixture 排除在 Windows 构建之外，这里不需要再判一次 runtime.GOOS。
func writeArgvEnvScript(t *testing.T, exitCode int, envNames ...string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(`for a in "$@"; do printf '<%s>\n' "$a"; done` + "\n")
	for _, name := range envNames {
		fmt.Fprintf(&b, `printf 'ENV %s=%%s\n' "$%s"`+"\n", name, name)
	}
	b.WriteString("printf 'PWD %s\\n' \"$(pwd)\"\n")
	fmt.Fprintf(&b, "exit %d\n", exitCode)

	path := filepath.Join(t.TempDir(), "argv-env.sh")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o755))
	return path
}

func TestGenericCommand_TemplateSplitsArgvBeforeRenderingAndAppendsExecArgsLiterally(t *testing.T) {
	ctx := setupGenericDB(t)
	script := writeArgvEnvScript(t, 0)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "region"}},
		Command: &custom_type_entity.CommandConfig{Template: script + " --region {{region}} --output json"},
	}))
	// region 的值里带空格：拆分发生在渲染之前，所以它必须落在同一个 argv 元素里。
	asset := genericAsset(t, "cli", map[string]string{"region": "ap east 1"})

	var stdout, stderr bytes.Buffer
	res, err := StreamGenericOnAsset(ctx, asset, []string{"s3", "ls", "{{not-a-template}}"},
		permission.Stdio{Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)

	out := stdout.String()
	assert.Contains(t, out, "<--region>\n<ap east 1>\n<--output>\n<json>\n",
		"the field value's internal space must not become a new argv element")
	assert.Contains(t, out, "<s3>\n<ls>\n<{{not-a-template}}>\n",
		"exec args are appended literally, verbatim, never template-rendered")
}

func TestGenericCommand_TemplateEnvBindingsAndWorkingDirectory(t *testing.T) {
	ctx := setupGenericDB(t)
	script := writeArgvEnvScript(t, 0, "AWS_PROFILE")
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli-env", ExecMode: custom_type_entity.ExecModeCommand,
		Fields: []custom_type_entity.Field{{Name: "profile"}},
		Command: &custom_type_entity.CommandConfig{
			Template: script,
			Env:      []custom_type_entity.EnvBinding{{Name: "AWS_PROFILE", Value: "{{profile}}"}},
		},
	}))
	asset := genericAsset(t, "cli-env", map[string]string{"profile": "prod-profile"})

	wd, err := os.Getwd()
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	res, err := StreamGenericOnAsset(ctx, asset, nil, permission.Stdio{Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, stdout.String(), "ENV AWS_PROFILE=prod-profile")
	assert.Contains(t, stdout.String(), "PWD "+wd)
}

func TestGenericCommand_ExitCodePassesThrough(t *testing.T) {
	ctx := setupGenericDB(t)
	script := writeArgvEnvScript(t, 7)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli-exit", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{Template: script},
	}))
	asset := genericAsset(t, "cli-exit", nil)

	var stdout, stderr bytes.Buffer
	res, err := StreamGenericOnAsset(ctx, asset, nil, permission.Stdio{Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err, "a completed run with a non-zero exit is not a Stream error")
	assert.Equal(t, 7, res.ExitCode)
	assert.Contains(t, res.AuditResult, "7")
}

func TestGenericCommand_ProgramNotFoundIsAnErrorNotExitOne(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli-missing-bin", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{Template: "/does/not/exist/on/this/machine"},
	}))
	asset := genericAsset(t, "cli-missing-bin", nil)

	_, err := StreamGenericOnAsset(ctx, asset, nil, permission.Stdio{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}})
	require.Error(t, err, "an incomplete request must be reported as an error, not folded into StreamResult.ExitCode")
}

func TestGenericCommand_NoTemplateRunsThroughTheShellAndSupportsPipes(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "shell-box", Slug: "shell-box", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{},
	}))
	asset := genericAsset(t, "shell-box", nil)

	var stdout, stderr bytes.Buffer
	// 整条命令必须以单个 argv 元素传入（模拟调用方在自己的 shell 里给它加了引号）：
	// 管道字符留在这个元素内部，交给下游 shell 解释，而不是被 opsctl/AI 这一层解释。
	res, err := StreamGenericOnAsset(ctx, asset, []string{"printf 'a\\nb\\nc\\n' | wc -l"},
		permission.Stdio{Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "3", strings.TrimSpace(stdout.String()))
}

func TestGenericCommand_NoTemplateEnvBindings(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "shell-box", Slug: "shell-box-env", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "token", Secret: true}},
		Command: &custom_type_entity.CommandConfig{Env: []custom_type_entity.EnvBinding{{Name: "MY_TOKEN", Value: "{{token}}"}}},
	}))
	asset := genericAsset(t, "shell-box-env", map[string]string{"token": "tok-123"})

	var stdout, stderr bytes.Buffer
	res, err := StreamGenericOnAsset(ctx, asset, []string{"printf '%s' \"$MY_TOKEN\""}, permission.Stdio{Stdout: &stdout, Stderr: &stderr})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "tok-123", stdout.String())
}

func TestGenericCommand_AIExecCapturesOutputAndExitCode(t *testing.T) {
	ctx := setupGenericDB(t)
	script := writeArgvEnvScript(t, 3)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "cli", Slug: "cli-ai", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{Template: script},
	}))
	asset := genericAsset(t, "cli-ai", nil)

	out, err := ExecGenericOnAsset(ctx, asset, "hello", "")
	require.NoError(t, err, "a non-zero exit is a result for the model, not a tool error")
	assert.Contains(t, out, "<hello>")
	assert.Contains(t, out, "exit code 3")
}

// AI exec 捕获输出时 os/exec 经管道转发 stdout/stderr：shell 退出后留在后台的子进程
// （`sleep 5 &`、启动一个守护进程）仍握着管道写端，不设上限的话 Wait 要等它退出，
// 这次工具调用就一直挂着。shell 本身已经退出，结果与退出码在它退出时就确定了。
func TestGenericCommand_AIExecReturnsWhenShellExitsLeavingBackgroundChild(t *testing.T) {
	ctx := setupGenericDB(t)
	require.NoError(t, custom_type_svc.CustomType().Save(ctx, &custom_type_entity.CustomType{
		Name: "shell-box", Slug: "shell-box-bg", ExecMode: custom_type_entity.ExecModeCommand,
		Fields:  []custom_type_entity.Field{{Name: "note"}},
		Command: &custom_type_entity.CommandConfig{},
	}))
	asset := genericAsset(t, "shell-box-bg", nil)

	started := time.Now()
	out, err := ExecGenericOnAsset(ctx, asset, "'sleep 5 & echo started; exit 4'", "")
	elapsed := time.Since(started)
	require.NoError(t, err)
	assert.Less(t, elapsed, 4*time.Second, "exec must not wait for a background child that inherited the output pipe")
	assert.Contains(t, out, "started")
	assert.Contains(t, out, "exit code 4")

	started = time.Now()
	out, err = ExecGenericOnAsset(ctx, asset, "'sleep 5 & echo ok'", "")
	require.NoError(t, err, "a shell that exited 0 is a completed run even though its pipe outlived it")
	assert.Less(t, time.Since(started), 4*time.Second)
	assert.Equal(t, "ok\n(exit code 0)", out)
}
