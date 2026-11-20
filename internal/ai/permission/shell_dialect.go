package permission

import (
	"github.com/opskat/opskat/internal/ai/policy"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/pkg/pwshdecode"
	"github.com/opskat/opskat/internal/pkg/shelldecode"
)

// shellDialect 是 shell 类命令面（SSH、串口、K8s、通用资产命令方式）按远端 shell 区分的部分：
// 策略面怎样拆子命令、怎样匹配规则，送审时用什么写法、怎样判断命令把要执行的内容藏在解码后面。
// 判定流程（checkShellCommandPolicy、reviewRequestFor）只经由它取这些差异，不按 shell 分支。
type shellDialect struct {
	// parse 拆出子命令。ruleDecidable 报告子命令是否保证拆全：为假时它们只用来匹配 deny 规则
	// 与给出提示，allow 规则与 grant 不能放行这条命令。
	parse func(command string) (subCmds []string, ruleDecidable bool, err error)
	// allow 匹配 allow 规则与 grant，deny 匹配 deny 规则。
	allow, deny policy.MatchFunc
	// reviewSyntax 是送审时的写法；decodesAndRuns 判断命令是否既解码又执行代码，超长时返回
	// errTooLong。
	reviewSyntax   Syntax
	decodesAndRuns func(command string) (bool, error)
	errTooLong     error
}

var (
	posixShellDialect = shellDialect{
		parse: func(command string) ([]string, bool, error) {
			subCmds, err := policy.ExtractSubCommands(command)
			return subCmds, true, err
		},
		allow:          policy.MatchCommandRule,
		deny:           policy.MatchCommandRule,
		reviewSyntax:   SyntaxShell,
		decodesAndRuns: shelldecode.DecodesAndRuns,
		errTooLong:     shelldecode.ErrTooLong,
	}
	powerShellDialect = shellDialect{
		parse: func(command string) ([]string, bool, error) {
			parsed, err := policy.ParsePowerShellCommand(command)
			return parsed.SubCommands, parsed.Plain, err
		},
		allow:          policy.MatchPowerShellRule,
		deny:           policy.MatchPowerShellDenyRule,
		reviewSyntax:   SyntaxPowerShell,
		decodesAndRuns: pwshdecode.DecodesAndRuns,
		errTooLong:     pwshdecode.ErrTooLong,
	}
)

// shellDialectFor 按资产声明的远端 shell 选方言：声明了 PowerShell 的 SSH 资产用 PowerShell，
// 其余用 POSIX shell。
func shellDialectFor(asset *asset_entity.Asset) shellDialect {
	if asset.IsPowerShellSSH() {
		return powerShellDialect
	}
	return posixShellDialect
}
