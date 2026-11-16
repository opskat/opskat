import { useEffect } from "react";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import yaml from "js-yaml";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { getK8sActions } from "@/components/k8s/actions";
import { getYamlEditHandler } from "@/components/k8s/detail/registry";
import { K8sYamlDialogs } from "@/components/k8s/yaml/K8sYamlDialogs";
import "@/components/k8s/yaml/register";
import { useK8sStore, type K8sClusterState, type K8sResourceRow } from "@/stores/k8sStore";
import { ApplyK8sYAML, GetK8sResource, UpdateK8sResource } from "../../wailsjs/go/k8s/K8s";

const h = vi.hoisted(() => ({
  notifySuccess: vi.fn(),
  toastError: vi.fn(),
  setModelMarkers: vi.fn(),
}));
vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: h.notifySuccess }));
vi.mock("sonner", () => ({ toast: { error: h.toastError, success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));
vi.mock("@/components/CodeEditor", () => ({
  CodeEditor: ({
    value,
    onChange,
    onMount,
  }: {
    value: string;
    onChange: (v: string) => void;
    onMount?: (editor: unknown, monaco: unknown) => void;
  }) => {
    useEffect(() => {
      onMount?.(
        { getModel: () => ({ id: "model" }) },
        { editor: { setModelMarkers: h.setModelMarkers }, MarkerSeverity: { Error: 8 } }
      );
    }, [onMount]);
    return <textarea data-testid="yaml-editor" value={value} onChange={(e) => onChange(e.target.value)} />;
  },
}));
vi.mock("@/components/CodeDiffViewer", () => ({
  CodeDiffViewer: ({ original, modified }: { original: string; modified: string }) => (
    <div data-testid="yaml-diff">
      <pre data-testid="diff-original">{original}</pre>
      <pre data-testid="diff-modified">{modified}</pre>
    </div>
  ),
}));

const ASSET = 93000;
const deployment = (resourceVersion: string, replicas = 3) => ({
  apiVersion: "apps/v1",
  kind: "Deployment",
  metadata: { name: "web", namespace: "default", resourceVersion },
  spec: { replicas },
  status: { readyReplicas: replicas, availableReplicas: replicas },
});
const read = (resourceVersion: string, replicas = 3) => {
  const object = deployment(resourceVersion, replicas);
  return JSON.stringify({ notFound: false, object, yaml: yaml.dump({ ...object, metadata: { ...object.metadata } }) });
};

function seedCluster(namespace = "team") {
  const ready = <T,>(data: T) => ({ status: "ready", data, error: null });
  const cluster = {
    namespace,
    types: ready({
      types: [
        {
          group: "apps",
          version: "v1",
          resource: "deployments",
          kind: "Deployment",
          namespaced: true,
          verbs: ["list", "update"],
          category: "workloads",
        },
        {
          group: "",
          version: "v1",
          resource: "events",
          kind: "Event",
          namespaced: true,
          verbs: ["list"],
          category: "cluster",
        },
      ],
      failedGroups: [],
    }),
  } as unknown as K8sClusterState;
  useK8sStore.setState({ clusters: { [ASSET]: cluster }, yamlDialogs: {} });
}

const editCtx = { assetId: ASSET, typeKey: "apps/deployments", namespace: "default", name: "web" };
const editor = () => screen.getByTestId("yaml-editor") as HTMLTextAreaElement;
const sentYaml = () =>
  yaml.load((vi.mocked(UpdateK8sResource).mock.calls[0][0] as { yaml: string }).yaml) as ReturnType<typeof deployment>;

async function openEdit() {
  const refreshType = vi.fn().mockResolvedValue(undefined);
  useK8sStore.setState({ refreshType });
  render(<K8sYamlDialogs assetId={ASSET} />);
  useK8sStore.getState().openYamlEdit(editCtx);
  await waitFor(() => expect(editor().value).toContain("name: web"));
  return refreshType;
}

describe("K8s edit YAML dialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    seedCluster();
    vi.mocked(GetK8sResource).mockResolvedValue(read("100") as never);
  });

  it("hides status and managedFields in the editor", async () => {
    await openEdit();
    expect(editor().value).toContain("replicas: 3");
    expect(editor().value).not.toContain("status");
    expect(editor().value).not.toContain("managedFields");
    expect(vi.mocked(GetK8sResource)).toHaveBeenCalledWith(
      expect.objectContaining({
        assetId: ASSET,
        group: "apps",
        version: "v1",
        resource: "deployments",
        namespace: "default",
        name: "web",
      })
    );
  });

  it("compares the edit against the cluster's current version", async () => {
    const user = userEvent.setup();
    await openEdit();
    fireEvent.change(editor(), { target: { value: editor().value.replace("replicas: 3", "replicas: 5") } });

    await user.click(screen.getByRole("tab", { name: /asset\.k8sYamlCompare/ }));
    expect(screen.getByTestId("diff-original").textContent).toContain("replicas: 3");
    expect(screen.getByTestId("diff-modified").textContent).toContain("replicas: 5");
  });

  it("marks a syntax error in the editor and does not submit", async () => {
    const user = userEvent.setup();
    await openEdit();
    fireEvent.change(editor(), { target: { value: "spec: [unclosed" } });

    expect(await screen.findByRole("alert")).toHaveTextContent("asset.k8sYamlSyntaxError");
    await waitFor(() =>
      expect(h.setModelMarkers).toHaveBeenLastCalledWith(expect.anything(), expect.any(String), [
        expect.objectContaining({ severity: 8, startLineNumber: expect.any(Number) }),
      ])
    );
    const save = screen.getByRole("button", { name: /asset\.k8sYamlSave/ });
    expect(save).toBeDisabled();
    await user.click(save);
    expect(vi.mocked(UpdateK8sResource)).not.toHaveBeenCalled();
  });

  it("saves with the read-time resourceVersion, keeps the hidden status, then notifies and refreshes", async () => {
    const user = userEvent.setup();
    vi.mocked(UpdateK8sResource).mockResolvedValue(JSON.stringify({ conflict: false, message: "" }) as never);
    const refreshType = await openEdit();
    // the user removes the resourceVersion line while editing
    fireEvent.change(editor(), {
      target: {
        value: editor()
          .value.replace(/\s*resourceVersion: .*\n/, "\n")
          .replace("replicas: 3", "replicas: 5"),
      },
    });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlSave/ }));

    await waitFor(() => expect(vi.mocked(UpdateK8sResource)).toHaveBeenCalledTimes(1));
    expect(vi.mocked(UpdateK8sResource).mock.calls[0][0]).toMatchObject({
      ref: { assetId: ASSET, group: "apps", version: "v1", resource: "deployments", namespace: "default", name: "web" },
    });
    const sent = sentYaml();
    expect(sent.metadata.resourceVersion).toBe("100");
    expect(sent.spec.replicas).toBe(5);
    expect(sent.status).toEqual(deployment("100").status);
    await waitFor(() => expect(h.notifySuccess).toHaveBeenCalled());
    expect(refreshType).toHaveBeenCalledWith(ASSET, "apps/deployments");
    expect(useK8sStore.getState().yamlDialogs[ASSET]).toBeUndefined();
  });

  it("shows a conflict banner and reloads then compares against the new cluster version", async () => {
    const user = userEvent.setup();
    vi.mocked(UpdateK8sResource)
      .mockResolvedValueOnce(
        JSON.stringify({
          conflict: true,
          message: 'Operation cannot be fulfilled on deployments.apps "web": the object has been modified',
        }) as never
      )
      .mockResolvedValue(JSON.stringify({ conflict: false, message: "" }) as never);
    await openEdit();
    fireEvent.change(editor(), { target: { value: editor().value.replace("replicas: 3", "replicas: 5") } });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlSave/ }));

    const banner = await screen.findByRole("alert");
    expect(banner).toHaveTextContent("the object has been modified");
    expect(h.notifySuccess).not.toHaveBeenCalled();
    expect(useK8sStore.getState().yamlDialogs[ASSET]).toBeDefined();

    vi.mocked(GetK8sResource).mockResolvedValue(read("101", 4) as never);
    await user.click(within(banner).getByRole("button", { name: /asset\.k8sYamlReloadCompare/ }));
    await waitFor(() => expect(screen.getByTestId("diff-original").textContent).toContain("replicas: 4"));
    expect(screen.getByTestId("diff-modified").textContent).toContain("replicas: 5");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlSave/ }));
    await waitFor(() => expect(vi.mocked(UpdateK8sResource)).toHaveBeenCalledTimes(2));
    const second = yaml.load((vi.mocked(UpdateK8sResource).mock.calls[1][0] as { yaml: string }).yaml) as ReturnType<
      typeof deployment
    >;
    expect(second.metadata.resourceVersion).toBe("101");
    expect(second.spec.replicas).toBe(5);
  });

  it("reports a failed save with the cluster's reason and leaves the dialog as it was", async () => {
    const user = userEvent.setup();
    vi.mocked(UpdateK8sResource).mockRejectedValue(new Error('deployments.apps "web" is forbidden: cannot update'));
    await openEdit();
    fireEvent.change(editor(), { target: { value: editor().value.replace("replicas: 3", "replicas: 5") } });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlSave/ }));

    await waitFor(() =>
      expect(h.toastError).toHaveBeenCalledWith('deployments.apps "web" is forbidden: cannot update')
    );
    expect(h.notifySuccess).not.toHaveBeenCalled();
    expect(editor().value).toContain("replicas: 5");
    expect(useK8sStore.getState().yamlDialogs[ASSET]).toBeDefined();
  });
});

describe("K8s apply YAML dialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    seedCluster("team");
  });

  async function openApply() {
    const refreshAll = vi.fn().mockResolvedValue(undefined);
    useK8sStore.setState({ refreshAll });
    render(<K8sYamlDialogs assetId={ASSET} />);
    useK8sStore.getState().openYamlApply(ASSET);
    await screen.findByTestId("yaml-editor");
    return refreshAll;
  }

  it("submits the pasted YAML with the selected namespace and lists each document's result", async () => {
    const user = userEvent.setup();
    vi.mocked(ApplyK8sYAML).mockResolvedValue(
      JSON.stringify({
        results: [
          { index: 0, kind: "ConfigMap", name: "a", namespace: "team", action: "created" },
          { index: 1, kind: "Deployment", name: "b", namespace: "team", action: "updated" },
          { index: 2, kind: "Widget", name: "c", namespace: "", action: "failed", error: "no matches for kind Widget" },
          {
            index: 3,
            kind: "Secret",
            name: "d",
            namespace: "",
            action: "failed",
            error: "namespace not specified",
            errorCode: "namespaceRequired",
          },
        ],
      }) as never
    );
    const refreshAll = await openApply();
    fireEvent.change(editor(), { target: { value: "kind: ConfigMap\n---\nkind: Deployment\n" } });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlApply$/ }));

    await waitFor(() =>
      expect(vi.mocked(ApplyK8sYAML)).toHaveBeenCalledWith({
        assetId: ASSET,
        yaml: "kind: ConfigMap\n---\nkind: Deployment\n",
        defaultNamespace: "team",
      })
    );
    const rows = await screen.findAllByTestId("k8s-apply-result");
    expect(rows).toHaveLength(4);
    expect(rows[0]).toHaveTextContent("asset.k8sApplyCreated");
    expect(rows[1]).toHaveTextContent("asset.k8sApplyUpdated");
    expect(rows[2]).toHaveTextContent("asset.k8sApplyFailed");
    expect(rows[2]).toHaveTextContent("no matches for kind Widget");
    expect(rows[3]).toHaveTextContent("asset.k8sYamlNamespaceRequired");
    // a failed document is only reported; the others' success still refreshes the lists
    expect(refreshAll).toHaveBeenCalledWith(ASSET);
    expect(h.notifySuccess).not.toHaveBeenCalled();
  });

  it("notifies success and refreshes when every document applied", async () => {
    const user = userEvent.setup();
    vi.mocked(ApplyK8sYAML).mockResolvedValue(
      JSON.stringify({
        results: [{ index: 0, kind: "ConfigMap", name: "a", namespace: "team", action: "created" }],
      }) as never
    );
    const refreshAll = await openApply();
    fireEvent.change(editor(), { target: { value: "kind: ConfigMap\n" } });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlApply$/ }));

    await waitFor(() => expect(h.notifySuccess).toHaveBeenCalled());
    expect(refreshAll).toHaveBeenCalledWith(ASSET);
  });

  it("reports a failed call with the cluster's reason and keeps the text", async () => {
    const user = userEvent.setup();
    vi.mocked(ApplyK8sYAML).mockRejectedValue(new Error("connection refused"));
    const refreshAll = await openApply();
    fireEvent.change(editor(), { target: { value: "kind: ConfigMap\n" } });
    await user.click(screen.getByRole("button", { name: /asset\.k8sYamlApply$/ }));

    await waitFor(() => expect(h.toastError).toHaveBeenCalledWith("connection refused"));
    expect(screen.queryByTestId("k8s-apply-result")).not.toBeInTheDocument();
    expect(editor().value).toBe("kind: ConfigMap\n");
    expect(refreshAll).not.toHaveBeenCalled();
  });

  it("does not submit an empty editor", async () => {
    await openApply();
    expect(screen.getByRole("button", { name: /asset\.k8sYamlApply$/ })).toBeDisabled();
  });
});

describe("K8s edit YAML entry points", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    seedCluster();
  });
  const row = { name: "web", namespace: "default", cells: [], phase: "" } as unknown as K8sResourceRow;

  it("offers a non-primary 编辑 YAML action only for types whose verbs include update", () => {
    const edit = getK8sActions("apps/deployments", row, ASSET).find((a) => a.id === "edit-yaml");
    expect(edit).toBeDefined();
    expect(edit?.primary).toBeFalsy();
    expect(edit?.destructive).toBeFalsy();
    expect(getK8sActions("/events", row, ASSET).some((a) => a.id === "edit-yaml")).toBe(false);
  });

  it("opens the edit dialog from the action and from the detail YAML tab's handler", () => {
    const edit = getK8sActions("apps/deployments", row, ASSET).find((a) => a.id === "edit-yaml")!;
    void edit.run({ assetId: ASSET, typeKey: "apps/deployments", namespace: "default", name: "web" });
    expect(useK8sStore.getState().yamlDialogs[ASSET]).toEqual({
      mode: "edit",
      ctx: expect.objectContaining({ name: "web" }),
    });

    useK8sStore.getState().closeYamlDialog(ASSET);
    getYamlEditHandler()?.({ assetId: ASSET, typeKey: "apps/deployments", namespace: "default", name: "web" });
    expect(useK8sStore.getState().yamlDialogs[ASSET]?.mode).toBe("edit");
  });
});
