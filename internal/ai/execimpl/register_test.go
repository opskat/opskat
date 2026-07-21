package execimpl

import (
	"testing"

	"github.com/opskat/opskat/internal/ai/permission"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

func TestInit_RegistersVerbatimTypes(t *testing.T) {
	want := []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
		asset_entity.AssetTypeEtcd,
		asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeKafka,
	}
	for _, at := range want {
		if _, ok := permission.ExecutorFor(at); !ok {
			t.Fatalf("no executor registered for %q", at)
		}
	}
}

func TestInit_HelpDocAttachedForEachType(t *testing.T) {
	for _, at := range []string{
		asset_entity.AssetTypeSSH,
		asset_entity.AssetTypeSerial,
		asset_entity.AssetTypeDatabase,
		asset_entity.AssetTypeRedis,
		asset_entity.AssetTypeK8s,
		asset_entity.AssetTypeEtcd,
		asset_entity.AssetTypeMongoDB,
		asset_entity.AssetTypeKafka,
	} {
		help, ok := permission.HelpFor(at)
		if !ok || help == "" {
			t.Fatalf("no help doc registered for %q", at)
		}
	}
}

// M5 regression lock: the empty-kubeconfig check must live in canonicalizeK8sCommand, not
// only in helper.ExecK8sOnAsset — canonicalize runs before the unified exec's permission
// check (internal/ai/tool/tool_handlers_unified.go's handleExec), so this is what makes
// an unconfigured k8s asset fail before the user ever sees an approval dialog, matching
// what the deleted exec_k8s tool did before calling BuildK8sCommandPlan.
func TestCanonicalizeK8sCommand_EmptyKubeconfigFailsBeforeApproval(t *testing.T) {
	canonicalize, ok := permission.CanonicalizeFor(asset_entity.AssetTypeK8s)
	if !ok {
		t.Fatal("k8s canonicalizer not registered")
	}

	asset := &asset_entity.Asset{Type: asset_entity.AssetTypeK8s}
	if err := asset.SetK8sConfig(&asset_entity.K8sConfig{Context: "prod-ctx"}); err != nil {
		t.Fatalf("set k8s config: %v", err)
	}

	if _, err := canonicalize(asset, "get pods"); err == nil {
		t.Fatal("expected an error for a k8s asset with no kubeconfig configured")
	}
}

func TestCanonicalizeK8sCommand_NonEmptyKubeconfigSucceeds(t *testing.T) {
	canonicalize, ok := permission.CanonicalizeFor(asset_entity.AssetTypeK8s)
	if !ok {
		t.Fatal("k8s canonicalizer not registered")
	}

	asset := &asset_entity.Asset{Type: asset_entity.AssetTypeK8s}
	if err := asset.SetK8sConfig(&asset_entity.K8sConfig{
		Kubeconfig: "enc-kubeconfig",
		Context:    "prod-ctx",
		Namespace:  "prod-ns",
	}); err != nil {
		t.Fatalf("set k8s config: %v", err)
	}

	got, err := canonicalize(asset, "get pods")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "kubectl --context prod-ctx --namespace prod-ns get pods"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
