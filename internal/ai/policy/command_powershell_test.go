package policy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellSubCommandExtraction(t *testing.T) {
	t.Run("简单命令与管道", func(t *testing.T) {
		cmds, err := extractPS("Get-Process | Sort-Object WS -Descending | Select-Object -First 12")
		require.NoError(t, err)
		require.Equal(t, []string{
			"Get-Process",
			"Sort-Object WS -Descending",
			"Select-Object -First 12",
		}, cmds)
	})

	t.Run("复杂巡检：ForEach-Object、哈希表、变量赋值、.NET 静态调用", func(t *testing.T) {
		cmd := `'--- TOP LEVEL DIRS ---'; Get-ChildItem C:\ -Directory -Force -EA SilentlyContinue | ForEach-Object { $s=(Get-ChildItem $_.FullName -Recurse -Force -File -EA SilentlyContinue | Measure-Object Length -Sum).Sum; [pscustomobject]@{D=$_.Name;GB=[math]::Round($s/1GB,1)} } | Sort-Object GB -Descending | Select-Object -First 10`
		cmds, err := extractPS(cmd)
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
		cmds, err := extractPS(cmd)
		require.NoError(t, err)
		require.Contains(t, cmds, `Get-Date`)
	})

	t.Run("控制流与变量赋值中的命令都能被提取，deny 规则不会漏掉嵌套删除", func(t *testing.T) {
		cmd := `$p='C:\tmp'; if (Test-Path $p) { Get-ChildItem $p | ForEach-Object { Remove-Item $_.FullName -Force } }`
		cmds, err := extractPS(cmd)
		require.NoError(t, err)
		require.Contains(t, cmds, "Test-Path $p")
		require.Contains(t, cmds, "Get-ChildItem $p")
		require.Contains(t, cmds, "Remove-Item $_.FullName -Force")
	})

	t.Run("双引号内的 $(...) 子表达式也被提取", func(t *testing.T) {
		cmds, err := extractPS(`Write-Output "deleted $(Remove-Item C:\secret.txt)"`)
		require.NoError(t, err)
		require.Contains(t, cmds, `Remove-Item C:\secret.txt`)
	})

	t.Run(".NET 静态方法调用保留为子命令", func(t *testing.T) {
		cmds, err := extractPS(`[System.IO.File]::Delete('C:\important.db')`)
		require.NoError(t, err)
		require.Equal(t, []string{`[System.IO.File]::Delete('C:\important.db')`}, cmds)
	})

	t.Run("括号包裹的属性访问如 (Get-Process robocopy).Count", func(t *testing.T) {
		cmds, err := extractPS(`(Get-Process robocopy -EA SilentlyContinue).Count; 'FREE=' + [math]::Round((Get-PSDrive C).Free/1GB, 2)`)
		require.NoError(t, err)
		require.Contains(t, cmds, "Get-Process robocopy -EA SilentlyContinue")
		require.Contains(t, cmds, "Get-PSDrive C")
	})

	t.Run("纯注释或空白返回空列表", func(t *testing.T) {
		cmds, err := extractPS("  # only comment\n <# block comment #> ")
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
			"Get-ChildItem '\u2019; Remove-Item C:\\data; '\u2019", // PowerShell 把弯引号当引号
		}
		for _, c := range bad {
			_, err := extractPS(c)
			require.Error(t, err, "expected syntax error for %q", c)
		}
	})

	t.Run("词中的 # 不是注释、单独的 CR 是换行：后面的命令照样拆出来", func(t *testing.T) {
		for _, c := range []string{
			"Get-ChildItem a#; Remove-Item C:\\data",
			"Get-ChildItem x\rRemove-Item C:\\data",
			"$a = 1; Get-ChildItem a#b | ForEach-Object { Remove-Item C:\\data }",
		} {
			cmds, err := extractPS(c)
			require.NoError(t, err, c)
			require.Contains(t, cmds, `Remove-Item C:\data`, c)
		}
	})
}

func extractPS(command string) ([]string, error) {
	parsed, err := ParsePowerShellCommand(command)
	return parsed.SubCommands, err
}

func TestParsePowerShellCommandPlain(t *testing.T) {
	plain := map[string][]string{
		`Get-ChildItem C:\logs -Recurse`:                          {`Get-ChildItem C:\logs -Recurse`},
		"Get-Process | Sort-Object WS -Descending\r\nGet-Date":    {"Get-Process", "Sort-Object WS -Descending", "Get-Date"},
		`Get-ChildItem 'C:\Program Files' "D:\a b" -Filter *.log`: {`Get-ChildItem 'C:\Program Files' "D:\a b" -Filter *.log`},
		`Get-Item 'a; b | c' && Test-Path x || whoami`:            {`Get-Item 'a; b | c'`, "Test-Path x", "whoami"},
		`Get-ChildItem D:\文档 -Path:C:\x a,b`:                      {`Get-ChildItem D:\文档 -Path:C:\x a,b`},
		`.\deploy.ps1 -WhatIf`:                                    {`.\deploy.ps1 -WhatIf`},
	}
	for c, want := range plain {
		parsed, err := ParsePowerShellCommand(c)
		require.NoError(t, err, c)
		require.True(t, parsed.Plain, "want plain: %s", c)
		require.Equal(t, want, parsed.SubCommands, c)
	}

	// 都能拆出子命令，但切分依赖本地近似的语法，不能交给 allow 规则与 grant。
	for _, c := range []string{
		`Get-ChildItem a#; Remove-Item C:\data`,      // 注释与否取决于上下文
		`$f = Get-Item C:\x.db; $f.Delete()`,         // 变量与方法调用
		`(Get-Item C:\x.db).Delete()`,                // 括号表达式
		`Get-ChildItem | ForEach-Object { $_.Name }`, // 脚本块
		`Get-Item "$(whoami)"`,                       // 双引号里的子表达式
		"Get-Item x `\n; Remove-Item y",              // 反引号转义
		`& Remove-Item x`,                            // 调用运算符
		`Get-Item x > C:\out.txt`,                    // 重定向
		`cmd /c --% dir & del x`,                     // 停止解析符
		`Get-Item @args`,                             // splatting
		`[IO.File]::Delete('C:\x')`,                  // .NET 静态调用
		"Get-Item\u00a0x",                            // PowerShell 视为空白的非 ASCII 字符
		"Get-Item x \u2013Force",                     // 破折号参数前缀
		`exit 1`,                                     // 关键字
		`'C:\a.exe' arg`,                             // 以字符串开头的是表达式
		`Get-Item x |`,                               // 管道后缺命令
		"Get-Item x |\nRemove-Item y",                // 管道跨行续写
	} {
		parsed, err := ParsePowerShellCommand(c)
		if err == nil {
			require.False(t, parsed.Plain, "want not plain: %q", c)
		}
	}
}
