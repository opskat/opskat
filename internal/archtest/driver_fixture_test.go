package archtest

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	dbregistry "github.com/opskat/opskat/internal/dbdriver"
	"github.com/opskat/opskat/pkg/dbdriver"
)

// 前端测试把内置驱动的描述当作 Query.ListDatabaseDrivers 的返回值用，那份描述是一个提交进
// 仓库的 JSON 文件。它与注册表是两处独立的事实来源：改了某个驱动的描述（字段、功能、别名）而
// 没改它，前端的表单与入口测试就在对着过时的描述断言。这里核对两者一致。
func TestFrontendBuiltinDriverFixtureMatchesRegistry(t *testing.T) {
	const file = "frontend/src/__tests__/builtinDbDrivers.json"

	var fixture []dbdriver.Descriptor
	decoder := json.NewDecoder(strings.NewReader(readRepoText(t, file)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}

	registered := make([]dbdriver.Descriptor, 0, len(fixture))
	for _, d := range dbregistry.List() {
		registered = append(registered, d.Descriptor())
	}
	if !reflect.DeepEqual(fixture, registered) {
		want, err := json.MarshalIndent(registered, "", "  ")
		if err != nil {
			t.Fatalf("marshal registered descriptors: %v", err)
		}
		t.Errorf("%s 与驱动注册表不一致，应为：\n%s", file, want)
	}
}
