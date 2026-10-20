package archtest

import (
	"bytes"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const modulePath = "github.com/opskat/opskat"

// publicDriverContract 是数据库驱动的公开契约：扩展（编译到 GOOS=wasip1）与宿主共用的
// 描述、会话、请求与结果类型，以及通用实现 sqldb。
var publicDriverContract = []string{
	modulePath + "/pkg/dbdriver",
	modulePath + "/pkg/dbdriver/sqldb",
}

// TestPublicDriverContractStandsAlone 守护公开契约的两条性质：不依赖任何 internal/ 包
// （仓库外的扩展 import 不到它们），并且能编译到扩展的目标平台 GOOS=wasip1 GOARCH=wasm。
func TestPublicDriverContractStandsAlone(t *testing.T) {
	root := repoRoot(t)

	out := runGo(t, root, nil, "list", "-deps", "./pkg/dbdriver/...")
	deps := strings.Fields(out)
	for _, want := range publicDriverContract {
		if !slices.Contains(deps, want) {
			t.Errorf("公开驱动契约缺少包 %s（go list -deps ./pkg/dbdriver/... 没有列出）", want)
		}
	}
	for _, dep := range deps {
		if strings.HasPrefix(dep, modulePath+"/internal/") {
			t.Errorf("pkg/dbdriver 依赖了 %s：公开契约不得依赖 internal/ 包", dep)
		}
	}

	runGo(t, root, []string{"GOOS=wasip1", "GOARCH=wasm"}, "build", "./pkg/dbdriver/...")
}

func runGo(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("go", args...) //nolint:gosec // argv comes from this file's fixed go list / go build calls
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s go %s: %v\n%s", strings.Join(env, " "), strings.Join(args, " "), err, stderr.String())
	}
	return string(out)
}
