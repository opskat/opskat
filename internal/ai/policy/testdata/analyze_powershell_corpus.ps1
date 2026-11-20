param([string]$In, [string]$Out)
# 用 PowerShell 自身的解析器分析语料里的每条命令，导出：解析错误数、全部命令调用（原文与各元素），
# 以及所有可能执行代码的节点：调用运算符、方法调用、静态成员、类型转换、写文件的重定向、
# 函数 / 类定义、-as、赋值目标、动态成员名。供 Go 侧差分测试使用。
$ErrorActionPreference = 'Stop'
$corpus = Get-Content -Raw -Encoding UTF8 $In | ConvertFrom-Json
$L = [System.Management.Automation.Language.Ast]
$results = New-Object System.Collections.Generic.List[object]
foreach ($c in $corpus) {
  $tokens = $null; $errs = $null
  $ast = [System.Management.Automation.Language.Parser]::ParseInput($c, [ref]$tokens, [ref]$errs)
  $commands = @()
  $issues = @()
  foreach ($n in $ast.FindAll({ $true }, $true)) {
    $t = $n.GetType().Name
    switch ($t) {
      'CommandAst' {
        $els = @($n.CommandElements | ForEach-Object {
          $pn = ''
          if ($_ -is [System.Management.Automation.Language.CommandParameterAst]) { $pn = $_.ParameterName }
          [ordered]@{ type = $_.GetType().Name; text = $_.Extent.Text; param = $pn }
        })
        $commands += [ordered]@{ text = $n.Extent.Text; name = [string]$n.GetCommandName(); op = [string]$n.InvocationOperator; elements = $els }
      }
      'InvokeMemberExpressionAst' {
        $m = $n.Member
        $name = if ($m -is [System.Management.Automation.Language.StringConstantExpressionAst]) { $m.Value } else { '<dynamic>' }
        if ($n.Static) { $issues += 'static:' + $n.Expression.TypeName.FullName + '::' + $name } else { $issues += 'member:' + $name }
      }
      'MemberExpressionAst' {
        $m = $n.Member
        if (-not ($m -is [System.Management.Automation.Language.StringConstantExpressionAst])) { $issues += 'dynmember' }
        elseif ($n.Static) { $issues += 'staticprop:' + $n.Expression.TypeName.FullName + '::' + $m.Value }
      }
      'ConvertExpressionAst' { $issues += 'convert:' + $n.Type.TypeName.FullName }
      'FileRedirectionAst' { $issues += 'redirect:' + $n.Location.Extent.Text }
      'FunctionDefinitionAst' { $issues += 'function' }
      'TypeDefinitionAst' { $issues += 'typedef' }
      'BinaryExpressionAst' { if ([string]$n.Operator -eq 'As') { $issues += 'as' } }
      'AssignmentStatementAst' {
        $left = $n.Left
        if ($left -is [System.Management.Automation.Language.VariableExpressionAst]) { $issues += 'assign:' + $left.VariablePath.UserPath }
        else { $issues += 'assign-other:' + $left.GetType().Name }
      }
      'UsingExpressionAst' { $issues += 'using' }
      'ScriptBlockAst' { if ($n.ParamBlock) { $issues += 'paramblock' } }
    }
  }
  $results.Add([ordered]@{ command = $c; errors = @($errs).Count; commands = $commands; issues = $issues })
}
$json = ConvertTo-Json -InputObject $results.ToArray() -Depth 8 -Compress
[System.IO.File]::WriteAllText($Out, $json, (New-Object System.Text.UTF8Encoding $false))
'analyzed ' + $results.Count + ' (' + $PSVersionTable.PSVersion.ToString() + ')'
