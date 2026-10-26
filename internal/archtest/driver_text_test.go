package archtest

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
)

// 两份给 AI / opsctl 用户看的静态文本（SKILL.md、commands.md）手写了驱动名单，没法由注册表生成。
// 注册表是独立的事实来源，这里核对文本与它一致：加驱动或改别名而忘了改文本，这里先红。
// 同类的帮助文本（opsctl exec --help、缺参报错）已由注册表生成，不在此核对。

func readRepoText(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func lineContaining(t *testing.T, text, needle, file string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("%s 中找不到包含 %q 的行，核对规则需随文本调整", file, needle)
	return ""
}

func sortedUnique(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}

// TestDatabaseSkillDriverListMatchesRegistry 核对 SKILL.md 配置表里的驱动取值与默认端口。
func TestDatabaseSkillDriverListMatchesRegistry(t *testing.T) {
	const file = "internal/ai/skills/database/SKILL.md"
	text := readRepoText(t, file)

	row := lineContaining(t, text, "| `driver` |", file)
	quoted := regexp.MustCompile(`"([a-z0-9]+)"`).FindAllStringSubmatch(row, -1)
	listed := make([]string, 0, len(quoted))
	for _, m := range quoted {
		listed = append(listed, m[1])
	}
	got := sortedUnique(listed)
	if want := sortedUnique(dbregistry.IDs()); !slices.Equal(got, want) {
		t.Errorf("%s 的 driver 行列出 %v，注册表是 %v", file, got, want)
	}

	// 「Defaults by driver: 3306 / 5432 / 1433」：有端口的驱动，按注册表顺序。
	portsLine := lineContaining(t, text, "Defaults by driver:", file)
	numbers := regexp.MustCompile(`\d+`).FindAllString(portsLine[strings.Index(portsLine, "Defaults by driver:"):], -1)
	listedPorts := make([]int, 0, len(numbers))
	for _, m := range numbers {
		n, _ := strconv.Atoi(m)
		listedPorts = append(listedPorts, n)
	}
	var wantPorts []int
	for _, d := range dbregistry.List() {
		if p := d.Descriptor().DefaultPort; p != 0 {
			wantPorts = append(wantPorts, p)
		}
	}
	if !slices.Equal(listedPorts, wantPorts) {
		t.Errorf("%s 的默认端口 %v，注册表是 %v", file, listedPorts, wantPorts)
	}
}

// TestOpsctlCommandsDriverNamesMatchRegistry 核对 commands.md 里 --type 可用的驱动名。
func TestOpsctlCommandsDriverNamesMatchRegistry(t *testing.T) {
	const file = "plugin/opsctl/skills/opsctl/references/commands.md"
	text := readRepoText(t, file)

	const marker = "database driver names:"
	i := strings.Index(text, marker)
	if i < 0 {
		t.Fatalf("%s 中找不到 %q，核对规则需随文本调整", file, marker)
	}
	// 名单到句号结束：`mysql`, `postgresql` / `postgres`, … `sqlite3`.
	seg := text[i+len(marker):]
	end := strings.Index(seg, "`. ")
	if end < 0 {
		t.Fatalf("%s 的驱动名单没有以句号结束，核对规则需随文本调整", file)
	}
	ticked := regexp.MustCompile("`([a-z0-9]+)`").FindAllStringSubmatch(seg[:end+1], -1)
	listed := make([]string, 0, len(ticked))
	for _, m := range ticked {
		listed = append(listed, m[1])
	}

	var want []string
	for _, d := range dbregistry.List() {
		want = append(want, d.Descriptor().Names()...)
	}
	if !slices.Equal(sortedUnique(listed), sortedUnique(want)) {
		t.Errorf("%s 列出的驱动名 %v，注册表（id 与别名）是 %v", file, sortedUnique(listed), sortedUnique(want))
	}
}
