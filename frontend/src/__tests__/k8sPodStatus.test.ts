import { describe, expect, it } from "vitest";
import { podStatus } from "@/components/k8s/detail/pod/podInfo";

const running = { running: { startedAt: "2026-11-16T00:00:00Z" } };

// The status word must match what `kubectl get pods` prints for the same Pod.
describe("podStatus", () => {
  it.each([
    [
      "an init container in CrashLoopBackOff",
      {
        spec: { initContainers: [{ name: "migrate" }], containers: [{ name: "app" }] },
        status: {
          phase: "Pending",
          initContainerStatuses: [{ name: "migrate", state: { waiting: { reason: "CrashLoopBackOff" } } }],
          containerStatuses: [{ name: "app", state: { waiting: { reason: "PodInitializing" } } }],
        },
      },
      "Init:CrashLoopBackOff",
    ],
    [
      "init containers still running",
      {
        spec: { initContainers: [{ name: "a" }, { name: "b" }], containers: [{ name: "app" }] },
        status: {
          phase: "Pending",
          initContainerStatuses: [
            { name: "a", state: running },
            { name: "b", state: { waiting: { reason: "PodInitializing" } } },
          ],
          containerStatuses: [{ name: "app", state: { waiting: { reason: "PodInitializing" } } }],
        },
      },
      "Init:0/2",
    ],
    [
      "an init container that failed",
      {
        spec: { initContainers: [{ name: "migrate" }], containers: [{ name: "app" }] },
        status: {
          phase: "Failed",
          initContainerStatuses: [{ name: "migrate", state: { terminated: { reason: "Error", exitCode: 1 } } }],
        },
      },
      "Init:Error",
    ],
    [
      "a running sidecar (restartable init container)",
      {
        spec: { initContainers: [{ name: "proxy", restartPolicy: "Always" }], containers: [{ name: "app" }] },
        status: {
          phase: "Running",
          conditions: [{ type: "Ready", status: "True" }],
          initContainerStatuses: [{ name: "proxy", started: true, ready: true, state: running }],
          containerStatuses: [{ name: "app", ready: true, state: running }],
        },
      },
      "Running",
    ],
    [
      "a completed Pod",
      {
        status: {
          phase: "Succeeded",
          containerStatuses: [{ name: "job", state: { terminated: { reason: "Completed", exitCode: 0 } } }],
        },
      },
      "Completed",
    ],
    [
      "a container killed for memory",
      {
        status: {
          phase: "Failed",
          containerStatuses: [{ name: "app", state: { terminated: { reason: "OOMKilled", exitCode: 137 } } }],
        },
      },
      "OOMKilled",
    ],
    [
      "a terminated container without a reason",
      {
        status: { phase: "Failed", containerStatuses: [{ name: "app", state: { terminated: { exitCode: 2 } } }] },
      },
      "ExitCode:2",
    ],
    [
      "a container waiting to restart",
      {
        status: {
          phase: "Running",
          containerStatuses: [{ name: "app", state: { waiting: { reason: "CrashLoopBackOff" } } }],
        },
      },
      "CrashLoopBackOff",
    ],
    ["an evicted Pod", { status: { phase: "Failed", reason: "Evicted" } }, "Evicted"],
    [
      "a Pod being deleted",
      {
        metadata: { deletionTimestamp: "2026-11-16T00:00:00Z" },
        status: { phase: "Running", containerStatuses: [{ name: "app", ready: true, state: running }] },
      },
      "Terminating",
    ],
  ])("%s", (_name, pod, text) => {
    expect(podStatus(pod).text).toBe(text);
  });
});
