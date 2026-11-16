import { afterEach, describe, expect, it, vi } from "vitest";
import { Trash2, Play } from "lucide-react";
import { getK8sActions, registerK8sAction } from "@/components/k8s/actions";
import type { K8sResourceRow } from "@/stores/k8sStore";

const row = (name: string): K8sResourceRow => ({
  name,
  namespace: "default",
  uid: name,
  creationTimestamp: "2026-11-16T00:00:00Z",
  labels: {},
  ownerReferences: [],
  cells: [name],
});

describe("k8s action registry", () => {
  const disposers: (() => void)[] = [];
  afterEach(() => disposers.splice(0).forEach((d) => d()));
  const reg = (...args: Parameters<typeof registerK8sAction>) => {
    const off = registerK8sAction(...args);
    disposers.push(off);
    return off;
  };

  it("returns only the actions that apply to the type and row, in registration order", () => {
    reg({ id: "a", labelKey: "a", icon: Play, appliesTo: (t) => t === "/pods", run: vi.fn() });
    reg({ id: "b", labelKey: "b", icon: Play, appliesTo: (_t, r) => (r as K8sResourceRow).name === "x", run: vi.fn() });
    reg({ id: "c", labelKey: "c", icon: Play, appliesTo: () => true, run: vi.fn() });

    expect(getK8sActions("/pods", row("x")).map((a) => a.id)).toEqual(["a", "b", "c"]);
    expect(getK8sActions("/pods", row("y")).map((a) => a.id)).toEqual(["a", "c"]);
    expect(getK8sActions("apps/deployments", row("y")).map((a) => a.id)).toEqual(["c"]);
  });

  it("lists destructive actions last", () => {
    reg({ id: "del", labelKey: "del", icon: Trash2, destructive: true, appliesTo: () => true, run: vi.fn() });
    reg({ id: "run", labelKey: "run", icon: Play, appliesTo: () => true, run: vi.fn() });
    expect(getK8sActions("/pods", row("x")).map((a) => a.id)).toEqual(["run", "del"]);
  });

  it("stops returning an action once it is unregistered", () => {
    const off = reg({ id: "a", labelKey: "a", icon: Play, appliesTo: () => true, run: vi.fn() });
    off();
    expect(getK8sActions("/pods", row("x"))).toEqual([]);
  });
});
