import { describe, expect, it } from "vitest";
import { getK8sStatusColor, isStatusColumn } from "@/components/k8s/utils";
import { visibleRows } from "@/components/k8s/cluster/listModel";

describe("getK8sStatusColor", () => {
  it.each([
    ["Running", "success"],
    ["Ready", "success"],
    ["Bound", "success"],
    ["Complete", "success"],
    ["Active", "success"],
    ["Pending", "warning"],
    ["ContainerCreating", "warning"],
    ["Terminating", "warning"],
    ["Init:0/1", "warning"],
    ["Failed", "error"],
    ["Error", "error"],
    ["CrashLoopBackOff", "error"],
    ["ImagePullBackOff", "error"],
    ["NotReady", "error"],
    ["Init:Error", "error"],
    ["Init:CrashLoopBackOff", "error"],
    ["Init:ImagePullBackOff", "error"],
    ["Ready,SchedulingDisabled", "success"],
    ["Whatever", "neutral"],
  ])("%s -> %s", (word, variant) => {
    expect(getK8sStatusColor(word)).toBe(variant);
  });
});

describe("isStatusColumn", () => {
  it("recognises status-like server columns but not data columns", () => {
    expect(isStatusColumn({ name: "Status" })).toBe(true);
    expect(isStatusColumn({ name: "Phase" })).toBe(true);
    expect(isStatusColumn({ name: "Name" })).toBe(false);
    expect(isStatusColumn({ name: "Restarts" })).toBe(false);
  });
});

describe("visibleRows", () => {
  it("falls back to name ascending when the sorted column is no longer listed", () => {
    const columns = [
      { name: "Name", type: "string", format: "name" },
      { name: "Status", type: "string", format: "" },
    ];
    const row = (name: string) => ({
      name,
      namespace: "prod",
      uid: name,
      creationTimestamp: "",
      labels: {},
      ownerReferences: [],
      cells: [name, "Running"],
    });
    // sorted by the Namespace column while all namespaces were shown, then one namespace was picked
    const rows = visibleRows(columns, [row("b"), row("c"), row("a")], "", { column: "Namespace", dir: "desc" });
    expect(rows.map((r) => r.name)).toEqual(["a", "b", "c"]);
  });
});
