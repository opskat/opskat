package archtest

import (
	"fmt"
	"strings"
	"testing"
)

// importBans 是仓库的 import 禁令表。豁免清单是 2026-07-18 圈定的存量债务
// （或文档明示的保留），只允许减少、不允许新增；新代码一律走规则指向的正道。
var importBans = []importBanRule{
	{
		files: map[string]bool{
			"cmd/opsctl/command/policy.go":       true,
			"cmd/opsctl/command/policy_group.go": true,
			"cmd/opsctl/command/tty_approval.go": true,
		},
		banned: []string{
			"github.com/opskat/opskat/internal/repository",
			"github.com/opskat/opskat/internal/pkg/dbutil",
		},
		message: "opsctl policy 命令只负责解析与展示，业务和持久化经 policy service（AGENTS.md → bindings → service → repository）",
	},
	{
		dir:       "internal/service/ssh_agent_svc/",
		banned:    []string{"github.com/opskat/opskat/internal/repository/asset_repo"},
		skipTests: true,
		message:   "SSH Agent service 不得直接调用资产 repository，经 asset_svc getter 获取跨域资产数据（AGENTS.md → 依赖接口、调 getter）",
	},
	{
		dir:       "internal/app/",
		banned:    []string{"github.com/opskat/opskat/internal/repository"},
		skipTests: true, // 测试注入 mock repo 属正常用法
		exempt: map[string]bool{
			"internal/app/opsctl/approval.go":     true,
			"internal/app/ssh/forward_manager.go": true,
			"internal/app/system/settings.go":     true,
		},
		message: "internal/app 不得直接调用 repository，经对应域服务取数（AGENTS.md → 依赖接口、调 getter）",
	},
	{
		dir:       "internal/app/",
		banned:    []string{"gorm.io/gorm", "github.com/opskat/opskat/internal/db"},
		skipTests: true,
		exempt: map[string]bool{
			"internal/app/ai/chat.go": true,
		},
		message: "internal/app 不得直接依赖 GORM / 内部 db，数据访问经 service → repository（AGENTS.md）",
	},
	{
		banned: []string{"log"},
		exact:  true, // log/slog 不在此列：给要求 slog.Logger 的第三方库做适配是正当用法
		exempt: map[string]bool{
			"main.go": true, // logger 初始化前的输出，docs/DEVELOP.md 明示保留
		},
		message: "业务代码不用标准库 log，用 github.com/cago-frame/cago/pkg/logger（优先 logger.Ctx；docs/DEVELOP.md → Logging for key flows）",
	},
}

const zapAnyMessage = `禁用 zap.Any，按值类型选强类型字段（zap.String/Int/Error/Duration/Stringer…）；` +
	`唯一豁免是 recover() 的 panic 值，key 须为 "panic"（docs/DEVELOP.md → Logging for key flows）`

// TestRepoConventions 全仓扫描：AGENTS.md / docs/DEVELOP.md 里的可判定约定在此强制。
// 报红时按提示改代码，而不是改这里的规则；确需新增豁免请在评审中说明原因。
func TestRepoConventions(t *testing.T) {
	root := repoRoot(t)
	files := collectGoFiles(t, root)
	all := make([]violation, 0, len(files))
	for _, rel := range files {
		pf := parseRepoFile(t, root, rel)
		for _, rule := range importBans {
			all = append(all, checkImportBan(pf, rule)...)
		}
		all = append(all, checkZapAny(pf, zapAnyMessage)...)
	}
	if len(all) > 0 {
		var b strings.Builder
		for _, v := range all {
			fmt.Fprintf(&b, "%s:%d: %s\n", v.file, v.line, v.message)
		}
		t.Fatalf("发现 %d 处约定违规：\n%s", len(all), b.String())
	}
}
