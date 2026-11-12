package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_svc"

	. "github.com/smartystreets/goconvey/convey"
)

func TestBuildK8sCommandPlan(t *testing.T) {
	Convey("BuildK8sCommandPlan", t, func() {
		Convey("injects default context and namespace", func() {
			plan, err := BuildK8sCommandPlan("get pods", &asset_entity.K8sConfig{
				Context:   "prod",
				Namespace: "app",
			})
			So(err, ShouldBeNil)
			So(plan.EffectiveCommand, ShouldEqual, "kubectl --context prod --namespace app get pods")
		})

		Convey("keeps explicit namespace", func() {
			plan, err := BuildK8sCommandPlan("kubectl get pods -n kube-system", &asset_entity.K8sConfig{
				Namespace: "app",
			})
			So(err, ShouldBeNil)
			So(plan.EffectiveCommand, ShouldEqual, "kubectl get pods -n kube-system")
		})

		Convey("rejects shell composition", func() {
			_, err := BuildK8sCommandPlan("kubectl get pods && kubectl delete pod api-0", nil)
			So(err, ShouldNotBeNil)
		})

		Convey("rejects explicit kubeconfig override", func() {
			_, err := BuildK8sCommandPlan("kubectl --kubeconfig /tmp/demo get pods", nil)
			So(err, ShouldNotBeNil)
		})
	})
}

// TestParseK8sCommandArgs_RejectsEmptyWordSubcommand pins MINOR-1's fix.
// cmdline.Words used to silently drop a deliberately-quoted empty word
// (an empty single- or double-quoted word); now it preserves it, so a
// command whose subcommand position (args[0]
// after any leading "kubectl"/"kubectl.exe" is stripped) resolves to the
// empty string must be rejected the same way a truly empty args slice
// already was — otherwise it falls through to decrypting the kubeconfig and
// spawning kubectl with an empty-string argv entry. Uses plain testing
// (not the goconvey style used elsewhere in this file) per this fix round's
// constraints.
func TestParseK8sCommandArgs_RejectsEmptyWordSubcommand(t *testing.T) {
	rejectCases := []string{
		`''`,          // only word is empty -> no subcommand at all
		`'' get pods`, // first word (the subcommand slot) is empty
	}
	for _, in := range rejectCases {
		if _, err := parseK8sCommandArgs(in); err == nil {
			t.Fatalf("parseK8sCommandArgs(%q) = nil error, want rejection (missing subcommand)", in)
		}
	}

	// A trailing empty word is a plain positional argument, not the
	// subcommand slot this guard protects — left unchanged intentionally,
	// not part of this fix's scope.
	const trailingEmpty = `kubectl get pods ''`
	args, err := parseK8sCommandArgs(trailingEmpty)
	if err != nil {
		t.Fatalf("parseK8sCommandArgs(%q) unexpected error: %v", trailingEmpty, err)
	}
	want := []string{"get", "pods", ""}
	if len(args) != len(want) {
		t.Fatalf("parseK8sCommandArgs(%q) = %#v, want %#v", trailingEmpty, args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Fatalf("parseK8sCommandArgs(%q) = %#v, want %#v", trailingEmpty, args, want)
		}
	}
}

func TestExecuteK8sCommandLocalFindsHomebrewKubectlWhenPathIsMinimal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Homebrew kubectl PATH fallback is macOS-specific")
	}

	kubectlPath := firstExistingExecutable(
		"/opt/homebrew/bin/kubectl",
		"/usr/local/bin/kubectl",
	)
	if kubectlPath == "" {
		t.Skip("Homebrew kubectl is not installed")
	}

	kubectlDir := filepath.Dir(kubectlPath)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	if strings.Contains(os.Getenv("PATH"), kubectlDir) {
		t.Fatalf("test PATH unexpectedly contains kubectl dir %s", kubectlDir)
	}

	out, err := ExecuteK8sCommandLocal(context.Background(), "apiVersion: v1\nkind: Config\n", []string{"version", "--client=true", "--output=yaml"})

	if err != nil {
		t.Fatalf("execute k8s command locally: %v", err)
	}
	if !strings.Contains(out, "clientVersion:") {
		t.Fatalf("expected kubectl client version output, got %q", out)
	}
}

// kubeconfig 的凭据插件（如等待浏览器登录的 kubelogin）由 kubectl 启动并继承它的输出管道；
// 停止时 kubectl 被结束，插件却还占着管道，执行不能因此一直等到插件退出。
func TestExecuteK8sCommandLocalReturnsPromptlyOnCancelWhenChildHoldsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a POSIX shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 30 &\nwait\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ExecuteK8sCommandLocal(ctx, "apiVersion: v1\nkind: Config\n", []string{"get", "pods"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("returned %v after start; want within a few seconds of the 300ms cancel", elapsed)
	}
}

// 不停止时结果不变：kubectl 正常退出、只剩后台子进程还占着输出管道，仍按成功返回它的输出，
// 不能因为管道收尾的上限到了就报失败。
func TestExecuteK8sCommandLocalSucceedsWhenChildOutlivesKubectl(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a POSIX shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho pod-a\nsleep 3 &\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")

	out, err := ExecuteK8sCommandLocal(context.Background(), "apiVersion: v1\nkind: Config\n", []string{"get", "pods"})
	if err != nil {
		t.Fatalf("kubectl exited 0 but got error: %v", err)
	}
	if !strings.Contains(out, "pod-a") {
		t.Fatalf("output lost: %q", out)
	}
}

func firstExistingExecutable(paths ...string) string {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

func TestExecK8sOnAsset_UsesEffectiveProxyChainSSHHop(t *testing.T) {
	credential_svc.SetDefault(credential_svc.New("test-master-key", []byte("0123456789abcdef")))
	encryptedKubeconfig, err := credential_svc.Default().Encrypt("apiVersion: v1\nkind: Config\n")
	if err != nil {
		t.Fatalf("encrypt kubeconfig: %v", err)
	}

	t.Run("routes via proxy_chain SSH layer when SSHTunnelID is 0", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		dialErr := errors.New("hop 42 dial reached")
		mockAsset.EXPECT().Find(gomock.Any(), int64(42)).Return(nil, dialErr)

		enabled := true
		asset := &asset_entity.Asset{
			ID:          1,
			Name:        "k8s-chain-only",
			Type:        asset_entity.AssetTypeK8s,
			SSHTunnelID: 0,
		}
		if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
			Kubeconfig: encryptedKubeconfig,
			ProxyChain: &asset_entity.ProxyChainConfig{
				Layers: []asset_entity.ProxyChainLayer{{
					ID:         "ssh-layer-1",
					Type:       asset_entity.ProxyChainLayerSSH,
					Enabled:    &enabled,
					Order:      1,
					SSHAssetID: 42,
				}},
			},
		}); err != nil {
			t.Fatalf("set k8s config: %v", err)
		}

		_, err := ExecK8sOnAsset(ctx, asset, "kubectl get pods", "")
		if !errors.Is(err, dialErr) {
			t.Fatalf("ExecK8sOnAsset error = %v, want %v", err, dialErr)
		}
	})

	t.Run("proxy_chain overrides stale SSHTunnelID column", func(t *testing.T) {
		ctx, mockAsset, _ := setupPolicyTest(t)
		dialErr := errors.New("hop 77 dial reached")
		mockAsset.EXPECT().Find(gomock.Any(), int64(77)).Return(nil, dialErr)

		enabled := true
		asset := &asset_entity.Asset{
			ID:          2,
			Name:        "k8s-chain-override",
			Type:        asset_entity.AssetTypeK8s,
			SSHTunnelID: 5,
		}
		if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
			Kubeconfig: encryptedKubeconfig,
			ProxyChain: &asset_entity.ProxyChainConfig{
				Layers: []asset_entity.ProxyChainLayer{{
					ID:         "ssh-layer-1",
					Type:       asset_entity.ProxyChainLayerSSH,
					Enabled:    &enabled,
					Order:      1,
					SSHAssetID: 77,
				}},
			},
		}); err != nil {
			t.Fatalf("set k8s config: %v", err)
		}

		_, err := ExecK8sOnAsset(ctx, asset, "kubectl get pods", "")
		if !errors.Is(err, dialErr) {
			t.Fatalf("ExecK8sOnAsset error = %v, want %v", err, dialErr)
		}
	})
}
