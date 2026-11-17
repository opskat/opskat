package k8s

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

var deployRef = ResourceRef{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "default", Name: "web"}

const deployPath = "/apis/apps/v1/namespaces/default/deployments/web"

func TestDeleteResourceUsesBackgroundPropagation(t *testing.T) {
	f := newFakeAPIServer(t)
	var body map[string]any
	f.handle(deployPath, func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Success"}`))
	})
	require.NoError(t, DeleteResource(context.Background(), f.kubeconfig(), deployRef))

	reqs := f.requests(deployPath)
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodDelete, reqs[0].Method)
	assert.Equal(t, "Background", body["propagationPolicy"])
}

func TestDeleteResourceClusterScopedHasNoNamespaceInPath(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/namespaces/team", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Success"}`))
	})
	require.NoError(t, DeleteResource(context.Background(), f.kubeconfig(), ResourceRef{Version: "v1", Resource: "namespaces", Name: "team"}))
	require.Len(t, f.requests("/api/v1/namespaces/team"), 1)
}

func TestDeleteResourcePassesThroughClusterError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle(deployPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `deployments.apps "web" is forbidden: User "u" cannot delete resource`)
	})
	require.ErrorContains(t, DeleteResource(context.Background(), f.kubeconfig(), deployRef), "cannot delete resource")

	g := newFakeAPIServer(t)
	err := DeleteResource(context.Background(), g.kubeconfig(), deployRef)
	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err), "a missing object stays a NotFound error")
}

func TestScaleResourcePatchesScaleSubresource(t *testing.T) {
	f := newFakeAPIServer(t)
	var body map[string]any
	var contentType string
	f.handle(deployPath+"/scale", func(w http.ResponseWriter, r *http.Request) {
		body, contentType = readBody(t, r), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Scale","apiVersion":"autoscaling/v1","spec":{"replicas":5}}`))
	})
	require.NoError(t, ScaleResource(context.Background(), f.kubeconfig(), deployRef, 5))

	reqs := f.requests(deployPath + "/scale")
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPatch, reqs[0].Method)
	assert.Equal(t, "application/merge-patch+json", contentType)
	assert.Equal(t, float64(5), body["spec"].(map[string]any)["replicas"])
	assert.Empty(t, f.requests(deployPath), "the workload object itself is not written")
}

func TestScaleResourceAllowsZeroAndRejectsNegative(t *testing.T) {
	f := newFakeAPIServer(t)
	var body map[string]any
	f.handle(deployPath+"/scale", func(w http.ResponseWriter, r *http.Request) {
		body = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Scale","apiVersion":"autoscaling/v1"}`))
	})
	require.NoError(t, ScaleResource(context.Background(), f.kubeconfig(), deployRef, 0))
	assert.Equal(t, float64(0), body["spec"].(map[string]any)["replicas"])

	require.Error(t, ScaleResource(context.Background(), f.kubeconfig(), deployRef, -1))
	assert.Len(t, f.requests(deployPath+"/scale"), 1, "a negative target never reaches the cluster")
}

func TestScaleResourcePassesThroughClusterError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle(deployPath+"/scale", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `deployments.apps "web" is forbidden: cannot patch resource "deployments/scale"`)
	})
	require.ErrorContains(t, ScaleResource(context.Background(), f.kubeconfig(), deployRef, 2), `deployments/scale`)
}

func TestRestartWorkloadPatchesRestartedAtAnnotation(t *testing.T) {
	f := newFakeAPIServer(t)
	var body map[string]any
	var contentType string
	f.handle(deployPath, func(w http.ResponseWriter, r *http.Request) {
		body, contentType = readBody(t, r), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"web"}}`))
	})
	before := time.Now().Add(-time.Second)
	require.NoError(t, RestartWorkload(context.Background(), f.kubeconfig(), deployRef))

	reqs := f.requests(deployPath)
	require.Len(t, reqs, 1)
	assert.Equal(t, http.MethodPatch, reqs[0].Method)
	assert.Equal(t, "application/strategic-merge-patch+json", contentType)
	annotations := body["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
	require.Len(t, annotations, 1)
	at, err := time.Parse(time.RFC3339, annotations["kubectl.kubernetes.io/restartedAt"].(string))
	require.NoError(t, err)
	assert.False(t, at.Before(before.Truncate(time.Second)))
}

func TestRestartWorkloadPassesThroughClusterError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle(deployPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `deployments.apps "web" is forbidden: cannot patch resource`)
	})
	require.ErrorContains(t, RestartWorkload(context.Background(), f.kubeconfig(), deployRef), "cannot patch resource")
}

const cronPath = "/apis/batch/v1/namespaces/default/cronjobs/nightly"
const jobsPath = "/apis/batch/v1/namespaces/default/jobs"

const cronJSON = `{"apiVersion":"batch/v1","kind":"CronJob","metadata":{"name":"nightly","namespace":"default","uid":"cron-uid"},
 "spec":{"schedule":"0 2 * * *","suspend":true,"jobTemplate":{
  "metadata":{"labels":{"app":"report"},"annotations":{"team":"data"}},
  "spec":{"backoffLimit":2,"template":{"spec":{"restartPolicy":"Never","containers":[{"name":"c","image":"busybox"}]}}}}}}`

func serveCron(f *fakeAPIServer, path, body string) {
	f.json(path, body)
}

func TestTriggerCronJobCreatesJobFromTemplateWithOwnerReference(t *testing.T) {
	f := newFakeAPIServer(t)
	serveCron(f, cronPath, cronJSON)
	var posted map[string]any
	f.handle(jobsPath, func(w http.ResponseWriter, r *http.Request) {
		posted = readBody(t, r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(posted)
	})

	name, err := TriggerCronJob(context.Background(), f.kubeconfig(), "default", "nightly")
	require.NoError(t, err)

	assert.Regexp(t, `^nightly-manual-[a-z0-9]{5}$`, name)
	meta := posted["metadata"].(map[string]any)
	assert.Equal(t, name, meta["name"])
	assert.Equal(t, "default", meta["namespace"])
	assert.Equal(t, "report", meta["labels"].(map[string]any)["app"])
	annotations := meta["annotations"].(map[string]any)
	assert.Equal(t, "data", annotations["team"])
	assert.Equal(t, "manual", annotations["cronjob.kubernetes.io/instantiate"])
	owners := meta["ownerReferences"].([]any)
	require.Len(t, owners, 1)
	owner := owners[0].(map[string]any)
	assert.Equal(t, "CronJob", owner["kind"])
	assert.Equal(t, "nightly", owner["name"])
	assert.Equal(t, "cron-uid", owner["uid"])
	assert.Equal(t, "batch/v1", owner["apiVersion"])
	assert.Equal(t, true, owner["controller"])
	spec := posted["spec"].(map[string]any)
	assert.Equal(t, float64(2), spec["backoffLimit"])
	assert.Contains(t, spec, "template")
	assert.Equal(t, "Job", posted["kind"])
	assert.Equal(t, "batch/v1", posted["apiVersion"])
	assert.Empty(t, f.requests(cronPath)[1:], "the CronJob itself is only read, so its schedule is untouched")
	assert.Equal(t, http.MethodGet, f.requests(cronPath)[0].Method)
}

func TestTriggerCronJobTruncatesLongNamesToALegalJobName(t *testing.T) {
	long := strings.Repeat("a", 49) + "-" + strings.Repeat("b", 10) // cut lands right after a '-'
	path := "/apis/batch/v1/namespaces/default/cronjobs/" + long
	f := newFakeAPIServer(t)
	serveCron(f, path, strings.Replace(cronJSON, `"name":"nightly"`, `"name":"`+long+`"`, 1))
	f.handle(jobsPath, echoObject(t))

	name, err := TriggerCronJob(context.Background(), f.kubeconfig(), "default", long)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(name), 63)
	assert.Regexp(t, `^a{49}-manual-[a-z0-9]{5}$`, name, "no dangling '-' before the suffix")
}

func TestTriggerCronJobPassesThroughClusterErrors(t *testing.T) {
	f := newFakeAPIServer(t)
	serveCron(f, cronPath, cronJSON)
	f.handle(jobsPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `jobs.batch is forbidden: cannot create resource "jobs"`)
	})
	_, err := TriggerCronJob(context.Background(), f.kubeconfig(), "default", "nightly")
	require.ErrorContains(t, err, `cannot create resource "jobs"`)

	g := newFakeAPIServer(t)
	_, err = TriggerCronJob(context.Background(), g.kubeconfig(), "default", "nightly")
	require.Error(t, err)
	assert.True(t, apierrors.IsNotFound(err))
}

func TestSetCronJobSuspendMergePatchesOnlySuspend(t *testing.T) {
	for _, suspend := range []bool{true, false} {
		f := newFakeAPIServer(t)
		var body map[string]any
		var contentType string
		f.handle(cronPath, func(w http.ResponseWriter, r *http.Request) {
			body, contentType = readBody(t, r), r.Header.Get("Content-Type")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(cronJSON))
		})
		require.NoError(t, SetCronJobSuspend(context.Background(), f.kubeconfig(), "default", "nightly", suspend))
		reqs := f.requests(cronPath)
		require.Len(t, reqs, 1)
		assert.Equal(t, http.MethodPatch, reqs[0].Method)
		assert.Equal(t, "application/merge-patch+json", contentType)
		assert.Equal(t, map[string]any{"spec": map[string]any{"suspend": suspend}}, body)
	}
}

func TestSetCronJobSuspendPassesThroughClusterError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle(cronPath, func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `cronjobs.batch "nightly" is forbidden: cannot patch resource`)
	})
	require.ErrorContains(t, SetCronJobSuspend(context.Background(), f.kubeconfig(), "default", "nightly", true), "cannot patch resource")
}

func TestSetNodeUnschedulableMergePatchesOnlyThatField(t *testing.T) {
	for _, unschedulable := range []bool{true, false} {
		f := newFakeAPIServer(t)
		var body map[string]any
		var contentType string
		f.handle("/api/v1/nodes/n1", func(w http.ResponseWriter, r *http.Request) {
			body, contentType = readBody(t, r), r.Header.Get("Content-Type")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"Node","apiVersion":"v1","metadata":{"name":"n1"}}`))
		})
		require.NoError(t, SetNodeUnschedulable(context.Background(), f.kubeconfig(), "n1", unschedulable))
		reqs := f.requests("/api/v1/nodes/n1")
		require.Len(t, reqs, 1, "no eviction / pod calls")
		assert.Equal(t, http.MethodPatch, reqs[0].Method)
		assert.Equal(t, "application/merge-patch+json", contentType)
		assert.Equal(t, map[string]any{"spec": map[string]any{"unschedulable": unschedulable}}, body)
	}
}

func TestSetNodeUnschedulablePassesThroughClusterError(t *testing.T) {
	f := newFakeAPIServer(t)
	f.handle("/api/v1/nodes/n1", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusForbidden, "Forbidden", `nodes "n1" is forbidden: cannot patch resource "nodes"`)
	})
	require.ErrorContains(t, SetNodeUnschedulable(context.Background(), f.kubeconfig(), "n1", true), `cannot patch resource "nodes"`)
}
