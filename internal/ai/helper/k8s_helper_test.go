package helper

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"

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

func firstExistingExecutable(paths ...string) string {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}
