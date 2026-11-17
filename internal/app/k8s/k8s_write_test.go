package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyNamespaceFallsBackToAssetNamespaceOnlyForAllNamespaces(t *testing.T) {
	assert.Equal(t, "team", applyNamespace("team", "configured"), "the selected namespace wins")
	assert.Equal(t, "configured", applyNamespace("", "configured"), "all namespaces -> the asset's namespace")
	assert.Equal(t, "", applyNamespace("", ""), "nothing to default to")
}

func TestWriteRequestsRejectBadPathSegments(t *testing.T) {
	k := &K8s{}
	_, err := k.UpdateK8sResource(K8sUpdateRequest{Ref: K8sResourceRef{AssetID: 1, Version: "v1", Resource: "pods", Namespace: "../x", Name: "p"}, YAML: "a: b"})
	require.ErrorContains(t, err, "invalid namespace")
	_, err = k.ApplyK8sYAML(K8sApplyRequest{AssetID: 1, YAML: "a: b", DefaultNamespace: "a/b"})
	require.ErrorContains(t, err, "invalid namespace")
}

func TestDeleteScaleRestartRejectBadRefs(t *testing.T) {
	k := &K8s{}
	bad := K8sResourceRef{AssetID: 1, Version: "v1", Resource: "pods", Namespace: "../x", Name: "p"}
	_, err := k.DeleteK8sResource(bad)
	require.ErrorContains(t, err, "invalid namespace")
	_, err = k.RestartK8sWorkload(bad)
	require.ErrorContains(t, err, "invalid namespace")
	_, err = k.ScaleK8sResource(K8sScaleRequest{Ref: K8sResourceRef{AssetID: 1, Group: "apps", Version: "v1", Resource: "deployments", Namespace: "d", Name: "w"}, Replicas: -1})
	require.ErrorContains(t, err, "replicas")
}

func TestCronJobAndNodeRequestsRejectBadInput(t *testing.T) {
	k := &K8s{}
	_, err := k.TriggerK8sCronJob(K8sCronJobRequest{AssetID: 1, Namespace: "../x", Name: "c"})
	require.ErrorContains(t, err, "invalid namespace")
	_, err = k.TriggerK8sCronJob(K8sCronJobRequest{AssetID: 1, Namespace: "", Name: "c"})
	require.ErrorContains(t, err, "namespace is required")
	_, err = k.TriggerK8sCronJob(K8sCronJobRequest{AssetID: 1, Namespace: "d", Name: ""})
	require.ErrorContains(t, err, "name is required")

	_, err = k.SetK8sCronJobSuspend(K8sCronJobSuspendRequest{AssetID: 1, Namespace: "d", Name: "a/b", Suspend: true})
	require.ErrorContains(t, err, "invalid name")
	_, err = k.SetK8sCronJobSuspend(K8sCronJobSuspendRequest{AssetID: 1, Namespace: "", Name: "c", Suspend: true})
	require.ErrorContains(t, err, "namespace is required")

	_, err = k.SetK8sNodeUnschedulable(K8sNodeUnschedulableRequest{AssetID: 1, Name: "../n", Unschedulable: true})
	require.ErrorContains(t, err, "invalid name")
	_, err = k.SetK8sNodeUnschedulable(K8sNodeUnschedulableRequest{AssetID: 1, Name: "", Unschedulable: true})
	require.ErrorContains(t, err, "name is required")
}
