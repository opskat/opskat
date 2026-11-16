package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 请求校验发生在加载资产之前：零值 binder 也能断言，非法请求不会触达集群。
func TestK8sReadIPCRejectsInvalidRequests(t *testing.T) {
	k := &K8s{}
	valid := K8sListRequest{AssetID: 1, Group: "apps", Version: "v1", Resource: "deployments", Namespace: "default"}

	listCases := map[string]struct {
		mutate func(*K8sListRequest)
		want   string
	}{
		"missing version":      {func(r *K8sListRequest) { r.Version = "" }, "version is required"},
		"missing resource":     {func(r *K8sListRequest) { r.Resource = "" }, "resource is required"},
		"subresource":          {func(r *K8sListRequest) { r.Resource = "pods/log" }, "resource"},
		"group escapes path":   {func(r *K8sListRequest) { r.Group = ".." }, "group"},
		"version escapes path": {func(r *K8sListRequest) { r.Version = "v1/../../x" }, "version"},
		"namespace with slash": {func(r *K8sListRequest) { r.Namespace = "a/b" }, "namespace"},
	}
	for name, c := range listCases {
		t.Run("list "+name, func(t *testing.T) {
			req := valid
			c.mutate(&req)
			_, err := k.ListK8sResources(req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			_, err = k.CountK8sResources(req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
		})
	}

	t.Run("get without name", func(t *testing.T) {
		_, err := k.GetK8sResource(K8sResourceRef{AssetID: 1, Version: "v1", Resource: "pods", Namespace: "default"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name is required")
	})
	t.Run("get with path name", func(t *testing.T) {
		_, err := k.GetK8sResource(K8sResourceRef{AssetID: 1, Version: "v1", Resource: "pods", Namespace: "default", Name: "../secrets"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name")
	})
	t.Run("events namespace with slash", func(t *testing.T) {
		_, err := k.ListK8sEvents(K8sEventsRequest{AssetID: 1, Namespace: "a/b"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace")
	})
	t.Run("health namespace with slash", func(t *testing.T) {
		_, err := k.GetK8sClusterHealth(1, "a/b")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "namespace")
	})
}
