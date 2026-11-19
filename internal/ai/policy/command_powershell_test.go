package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractPowerShellSubCommands(t *testing.T) {
	t.Run("简单命令与管道", func(t *testing.T) {
		cmds, err := ExtractPowerShellSubCommands("Get-Process | Sort-Object WS -Descending | Select-Object -First 12")
		require.NoError(t, err)
		require.Equal(t, []string{
			"Get-Process",
			"Sort-Object WS -Descending",
			"Select-Object -First 12",
		}, cmds)
	})

	t.Run("复杂巡检：ForEach-Object、哈希表、变量赋值、.NET 静态调用", func(t *testing.T) {
		cmd := `'--- TOP LEVEL DIRS ---'; Get-ChildItem C:\ -Directory -Force -EA SilentlyContinue | ForEach-Object { $s=(Get-ChildItem $_.FullName -Recurse -Force -File -EA SilentlyContinue | Measure-Object Length -Sum).Sum; [pscustomobject]@{D=$_.Name;GB=[math]::Round($s/1GB,1)} } | Sort-Object GB -Descending | Select-Object -First 10`
		cmds, err := ExtractPowerShellSubCommands(cmd)
		require.NoError(t, err)
		require.NotEmpty(t, cmds)
		require.Contains(t, cmds, `Get-ChildItem C:\ -Directory -Force -EA SilentlyContinue`)
		require.Contains(t, cmds, `Get-ChildItem $_.FullName -Recurse -Force -File -EA SilentlyContinue`)
		require.Contains(t, cmds, `Measure-Object Length -Sum`)
		require.Contains(t, cmds, `Sort-Object GB -Descending`)
		require.Contains(t, cmds, `Select-Object -First 10`)
	})

	t.Run("Get-WinEvent -FilterHashtable 与嵌套子表达式", func(t *testing.T) {
		cmd := `'--- SYSTEM ERRORS 48h ---'; Get-WinEvent -FilterHashtable @{LogName='System';Level=1,2;StartTime=(Get-Date).AddHours(-48)} -MaxEvents 15`
		cmds, err := ExtractPowerShellSubCommands(cmd)
		require.NoError(t, err)
		require.Contains(t, cmds, `Get-Date`)
	})

	t.Run("控制流与变量赋值中的命令都能被提取，deny 规则不会漏掉嵌套删除", func(t *testing.T) {
		cmd := `$p='C:\tmp'; if (Test-Path $p) { Get-ChildItem $p | ForEach-Object { Remove-Item $_.FullName -Force } }`
		cmds, err := ExtractPowerShellSubCommands(cmd)
		require.NoError(t, err)
		require.Contains(t, cmds, "Test-Path $p")
		require.Contains(t, cmds, "Get-ChildItem $p")
		require.Contains(t, cmds, "Remove-Item $_.FullName -Force")
	})

	t.Run("双引号内的 $(...) 子表达式也被提取", func(t *testing.T) {
		cmds, err := ExtractPowerShellSubCommands(`Write-Output "deleted $(Remove-Item C:\secret.txt)"`)
		require.NoError(t, err)
		require.Contains(t, cmds, `Remove-Item C:\secret.txt`)
	})

	t.Run(".NET 静态方法调用保留为子命令", func(t *testing.T) {
		cmds, err := ExtractPowerShellSubCommands(`[System.IO.File]::Delete('C:\important.db')`)
		require.NoError(t, err)
		require.Equal(t, []string{`[System.IO.File]::Delete('C:\important.db')`}, cmds)
	})

	t.Run("括号包裹的属性访问如 (Get-Process robocopy).Count", func(t *testing.T) {
		cmds, err := ExtractPowerShellSubCommands(`(Get-Process robocopy -EA SilentlyContinue).Count; 'FREE=' + [math]::Round((Get-PSDrive C).Free/1GB, 2)`)
		require.NoError(t, err)
		require.Contains(t, cmds, "Get-Process robocopy -EA SilentlyContinue")
		require.Contains(t, cmds, "Get-PSDrive C")
	})

	t.Run("纯注释或空白返回空列表", func(t *testing.T) {
		cmds, err := ExtractPowerShellSubCommands("  # only comment\n <# block comment #> ")
		require.NoError(t, err)
		require.Empty(t, cmds)
	})

	t.Run("未闭合引号/注释/括号返回语法错误", func(t *testing.T) {
		bad := []string{
			"Get-ChildItem 'unclosed",
			`Write-Output "unclosed`,
			"ForEach-Object { Get-Date",
			"Get-Date <# unclosed comment",
			"@'\nunclosed here-string",
			"Get-Process )",
		}
		for _, c := range bad {
			_, err := ExtractPowerShellSubCommands(c)
			require.Error(t, err, "expected syntax error for %q", c)
		}
	})
}
