package pwshdecode

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodesAndRuns(t *testing.T) {
	t.Run("正常只读与常规 PowerShell 巡检命令放行送审", func(t *testing.T) {
		safe := []string{
			`Get-ChildItem C:\ -Directory -Force -EA SilentlyContinue | ForEach-Object { $s=(Get-ChildItem $_.FullName -Recurse -Force -File -EA SilentlyContinue | Measure-Object Length -Sum).Sum; [pscustomobject]@{D=$_.Name;GB=[math]::Round($s/1GB,1)} }`,
			`Get-WinEvent -FilterHashtable @{LogName='System';Level=1,2;StartTime=(Get-Date).AddHours(-48)} -MaxEvents 15`,
			`Get-Process | Sort-Object WS -Descending | Select-Object -First 12`,
			`Get-LocalUser | ForEach-Object { $_.Name+' | enabled='+$_.Enabled }`,
			`(Get-Process robocopy -EA SilentlyContinue).Count`,
			`# comment with FromBase64String and iex should be ignored`,
			`[Convert]::FromBase64String('aGVsbG8=')`, // 仅解码不执行
		}
		for _, cmd := range safe {
			hidden, err := DecodesAndRuns(cmd)
			require.NoError(t, err, "cmd=%s", cmd)
			require.False(t, hidden, "expected safe cmd=%s", cmd)
		}
	})

	t.Run("EncodedCommand 与解码执行混淆拦截", func(t *testing.T) {
		hiddenCases := []string{
			`powershell -EncodedCommand SQBFAFgA`,
			`powershell.exe -NoProfile -enc SQBFAFgA`,
			`pwsh -ec SQBFAFgA`,
			`powershell -e SQBFAFgA`,
			`[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('aWQ=')) | iex`,
			`[System.Text.Encoding]::Unicode.GetString([System.Convert]::FromBase64String('aWQ=')) | Invoke-Expression`,
			`$s = [Convert]::FromBase64String('aWQ='); I` + "`" + `E` + "`" + `X ([Text.Encoding]::UTF8.GetString($s))`,
			`[ScriptBlock]::Create([Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('aWQ=')))`,
			`certutil -decode payload.b64 payload.ps1; powershell -File payload.ps1`,
		}
		for _, cmd := range hiddenCases {
			hidden, err := DecodesAndRuns(cmd)
			require.NoError(t, err, "cmd=%s", cmd)
			require.True(t, hidden, "expected hidden cmd=%s", cmd)
		}
	})

	t.Run("超长返回 ErrTooLong", func(t *testing.T) {
		_, err := DecodesAndRuns(strings.Repeat("a", MaxCommandBytes+1))
		require.ErrorIs(t, err, ErrTooLong)
	})

	t.Run("语法未闭合返回解析错误", func(t *testing.T) {
		_, err := DecodesAndRuns("Get-ChildItem 'unclosed")
		require.Error(t, err)
	})
}
