package k8s

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const writeVerbs = `["create","delete","get","list","patch","update","watch"]`

func serveWriteDiscovery(f *fakeAPIServer) {
	f.json("/api", `{"kind":"APIVersions","versions":["v1"]}`)
	f.json("/apis", `{"kind":"APIGroupList","apiVersion":"v1","groups":[
	 {"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}
	]}`)
	f.json("/api/v1", `{"kind":"APIResourceList","groupVersion":"v1","resources":[
	 {"name":"configmaps","namespaced":true,"kind":"ConfigMap","verbs":`+writeVerbs+`},
	 {"name":"namespaces","namespaced":false,"kind":"Namespace","verbs":`+writeVerbs+`}
	]}`)
	f.json("/apis/apps/v1", `{"kind":"APIResourceList","groupVersion":"apps/v1","resources":[
	 {"name":"deployments","namespaced":true,"kind":"Deployment","verbs":`+writeVerbs+`}
	]}`)
}

func readBody(t *testing.T, r *http.Request) map[string]any {
	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func echoObject(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		_, _ = w.Write(raw)
	}
}

var cmRef = ResourceRef{Version: "v1", Resource: "configmaps", Namespace: "default", Name: "cm1"}

const cmYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm1
  namespace: default
  resourceVersion: "42"
data:
  k: v
`

func TestUpdateResourcePutsWithReadTimeResourceVersion(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/configmaps/cm1", echoObject(t))

	res, err := UpdateResource(context.Background(), f.kubeconfig(), cmRef, cmYAML)
	require.NoError(t, err)
	assert.False(t, res.Conflict)
	assert.Equal(t, "v", res.Object["data"].(map[string]any)["k"])

	reqs := f.requests("/api/v1/namespaces/default/configmaps/cm1")
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPut, reqs[0].Method)
}

func TestUpdateResourceSendsResourceVersionInBody(t *testing.T) {
	f := newFakeAPIServer(t)
	var sent map[string]any
	f.handle("/api/v1/namespaces/default/configmaps/cm1", func(w http.ResponseWriter, r *http.Request) {
		sent = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sent)
	})
	_, err := UpdateResource(context.Background(), f.kubeconfig(), cmRef, cmYAML)
	require.NoError(t, err)
	assert.Equal(t, "42", sent["metadata"].(map[string]any)["resourceVersion"])
}

func TestUpdateResourceMapsConflict(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/configmaps/cm1", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusConflict, "Conflict", `Operation cannot be fulfilled on configmaps "cm1": the object has been modified`)
	})
	res, err := UpdateResource(context.Background(), f.kubeconfig(), cmRef, cmYAML)
	require.NoError(t, err)
	assert.True(t, res.Conflict)
	assert.Contains(t, res.Message, "the object has been modified")
	assert.Nil(t, res.Object)
}

func TestUpdateResourcePassesThroughOtherFailures(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/default/configmaps/cm1", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `configmaps "cm1" is forbidden: User "u" cannot update resource`)
	})
	_, err := UpdateResource(context.Background(), f.kubeconfig(), cmRef, cmYAML)
	require.ErrorContains(t, err, `cannot update resource`)
}

func TestUpdateResourceRejectsBadInput(t *testing.T) {
	f := newFakeAPIServer(t)
	_, err := UpdateResource(context.Background(), f.kubeconfig(), cmRef, "metadata: [unclosed")
	require.Error(t, err)
	_, err = UpdateResource(context.Background(), f.kubeconfig(), cmRef, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm1\n")
	require.ErrorContains(t, err, "resourceVersion")
	assert.Empty(t, f.requests("/api/v1/namespaces/default/configmaps/cm1"), "nothing is sent for invalid input")
}

func TestApplyYAMLIndependentPerDocumentResults(t *testing.T) {
	f := newFakeAPIServer(t)
	serveWriteDiscovery(f)
	// cm-existing exists; the Deployment does not; the cm-denied patch is forbidden.
	f.handle("/api/v1/namespaces/team/configmaps/cm-existing", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"ConfigMap","apiVersion":"v1","metadata":{"name":"cm-existing","namespace":"team"}}`))
			return
		}
		echoObject(t)(w, r)
	})
	var appliedDeployment map[string]any
	f.handle("/apis/apps/v1/namespaces/team/deployments/web", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		appliedDeployment = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(appliedDeployment)
	})
	f.handle("/api/v1/namespaces/team/configmaps/cm-denied", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		writeStatus(w, http.StatusForbidden, "Forbidden", `configmaps "cm-denied" is forbidden: cannot patch`)
	})
	f.handle("/api/v1/namespaces/ops", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		echoObject(t)(w, r)
	})

	yamlText := `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-existing
data: {a: b}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec: {replicas: 1}
---
apiVersion: example.com/v1
kind: Widget
metadata:
  name: w
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-denied
  namespace: team
---
apiVersion: v1
kind: Namespace
metadata:
  name: ops
`
	got, err := ApplyYAML(context.Background(), f.kubeconfig(), yamlText, "team")
	require.NoError(t, err)
	require.Len(t, got.Results, 5)

	assert.Equal(t, ApplyResult{Index: 0, Kind: "ConfigMap", Name: "cm-existing", Namespace: "team", Action: ApplyUpdated}, got.Results[0])
	assert.Equal(t, ApplyResult{Index: 1, Kind: "Deployment", Name: "web", Namespace: "team", Action: ApplyCreated}, got.Results[1])
	assert.Equal(t, ApplyFailed, got.Results[2].Action)
	assert.Equal(t, "Widget", got.Results[2].Kind)
	assert.NotEmpty(t, got.Results[2].Error, "an unknown kind fails only its own document")
	assert.Equal(t, ApplyFailed, got.Results[3].Action)
	assert.Contains(t, got.Results[3].Error, "cannot patch")
	assert.Equal(t, ApplyResult{Index: 4, Kind: "Namespace", Name: "ops", Action: ApplyCreated}, got.Results[4])

	patches := f.requests("/apis/apps/v1/namespaces/team/deployments/web")
	require.Len(t, patches, 2)
	patch := patches[1]
	assert.Equal(t, http.MethodPatch, patch.Method)
	assert.Equal(t, "application/apply-patch+yaml", patch.Header.Get("Content-Type"))
	assert.Equal(t, "opskat", patch.URL.Query().Get("fieldManager"))
	assert.Equal(t, "false", patch.URL.Query().Get("force"))
	assert.Equal(t, "team", appliedDeployment["metadata"].(map[string]any)["namespace"], "the defaulted namespace is part of the applied object")
}

func TestApplyYAMLWithoutAnyNamespaceFailsOnlyNamespacedDocuments(t *testing.T) {
	f := newFakeAPIServer(t)
	serveWriteDiscovery(f)
	f.handle("/api/v1/namespaces/ops", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeStatus(w, http.StatusNotFound, "NotFound", "not found")
			return
		}
		echoObject(t)(w, r)
	})
	yamlText := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n---\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: ops\n"

	got, err := ApplyYAML(context.Background(), f.kubeconfig(), yamlText, "")
	require.NoError(t, err)
	require.Len(t, got.Results, 2)
	assert.Equal(t, ApplyFailed, got.Results[0].Action)
	assert.Equal(t, ErrCodeNamespaceRequired, got.Results[0].ErrorCode)
	assert.Equal(t, ApplyCreated, got.Results[1].Action)
	assert.Empty(t, f.requests("/api/v1/namespaces/default/configmaps/cm"))
}

func TestApplyYAMLReportsSyntaxErrorsAndMissingNamesPerDocument(t *testing.T) {
	f := newFakeAPIServer(t)
	serveWriteDiscovery(f)
	yamlText := "metadata: [unclosed\n---\napiVersion: v1\nkind: ConfigMap\nmetadata: {}\n---\n# only a comment\n"

	got, err := ApplyYAML(context.Background(), f.kubeconfig(), yamlText, "team")
	require.NoError(t, err)
	require.Len(t, got.Results, 2, "a comment-only document is not a document")
	for i, r := range got.Results {
		assert.Equal(t, i, r.Index)
		assert.Equal(t, ApplyFailed, r.Action)
		assert.NotEmpty(t, r.Error)
	}
}
