import { describe, expect, it } from "vitest";
import { getK8sStatusColor, isStatusColumn } from "@/components/k8s/utils";

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
