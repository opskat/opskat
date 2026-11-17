import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { K8sClusterPage } from "@/components/k8s/K8sClusterPage";
import { useK8sStore } from "@/stores/k8sStore";
import {
  CountK8sResources,
  GetK8sClusterHealth,
  GetK8sClusterVersion,
  GetK8sResourceTypes,
  ListK8sEvents,
  ListK8sResources,
} from "../../wailsjs/go/k8s/K8s";
import type { asset_entity, k8s } from "../../wailsjs/go/models";

vi.mock("@/lib/notify", () => ({ notifyCopied: vi.fn(), notifySuccess: vi.fn() }));

const type = (group: string, resource: string, kind: string, namespaced: boolean, category: string) => ({
  group,
  version: "v1",
  resource,
  kind,
  namespaced,
  verbs: ["list"],
  category,
});
const TYPES = {
  types: [type("", "events", "Event", true, "cluster"), type("", "pods", "Pod", true, "workloads")],
  failedGroups: [],
};

const ev = (type: string, reason: string, message: string, pod: string, ts: string, kind = "Pod") => ({
  type,
  reason,
  message,
  count: 3,
  lastTimestamp: ts,
  involvedObject: { apiVersion: "v1", kind, namespace: "default", name: pod, uid: pod },
});
// The cluster already returns them newest first.
const WARNINGS = [
  ev("Warning", "BackOff", "Back-off restarting failed container", "api-1", "2026-11-12T08:00:00Z"),
  ev("Warning", "FailedScheduling", "0/3 nodes are available", "web-1", "2026-11-11T08:00:00Z"),
];
const ALL = [
  WARNINGS[0],
  ev("Normal", "Pulled", "Image pulled", "api-1", "2026-11-11T12:00:00Z"),
  WARNINGS[1],
  ev("Warning", "Odd", "No such kind", "x", "2026-11-10T08:00:00Z", "Mystery"),
];

let nextId = 94000;
const asset = (ID: number) =>
  ({ ID, Name: "k8s", Type: "k8s", Config: '{"namespace":"default"}' }) as asset_entity.Asset;

const rowReasons = () =>
  screen.queryAllByTestId("k8s-event-row").map((r) => within(r).getByTestId("k8s-event-reason").textContent);

async function openEvents(user: ReturnType<typeof userEvent.setup>) {
  const nav = within(await screen.findByTestId("k8s-sidebar"));
  await user.click(await nav.findByTestId("k8s-type-/events"));
  return await screen.findByTestId("k8s-events-view");
}

describe("K8s events view", () => {
  beforeEach(() => {
    for (const fn of [
      GetK8sClusterVersion,
      GetK8sResourceTypes,
      ListK8sResources,
      CountK8sResources,
      GetK8sClusterHealth,
      ListK8sEvents,
    ]) {
      vi.mocked(fn).mockReset();
    }
    vi.mocked(GetK8sClusterVersion).mockResolvedValue(JSON.stringify({ version: "v1", platform: "x" }) as never);
    vi.mocked(GetK8sResourceTypes).mockResolvedValue(JSON.stringify(TYPES) as never);
    vi.mocked(ListK8sResources).mockResolvedValue(JSON.stringify({ columns: [], rows: [] }) as never);
    vi.mocked(ListK8sEvents).mockImplementation((async (req: k8s.K8sEventsRequest) =>
      JSON.stringify({ events: req.warningOnly ? WARNINGS : ALL })) as never);
  });

  it("lists warnings newest first by default and switches to all events", async () => {
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openEvents(user);
    await waitFor(() => expect(rowReasons()).toEqual(["BackOff", "FailedScheduling"]));

    await user.click(screen.getByRole("tab", { name: "asset.k8sEventsAll" }));
    await waitFor(() => expect(rowReasons()).toEqual(["BackOff", "Pulled", "FailedScheduling", "Odd"]));
    expect(vi.mocked(ListK8sEvents).mock.calls.at(-1)?.[0]).toMatchObject({ namespace: "default", warningOnly: false });
  });

  it("disables the refresh button while its re-read is in flight", async () => {
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const view = within(await openEvents(user));
    await waitFor(() => expect(rowReasons()).toHaveLength(2));

    let release = () => {};
    vi.mocked(ListK8sEvents).mockImplementation(
      () =>
        new Promise((resolve) => {
          release = () => resolve(JSON.stringify({ events: WARNINGS }) as never);
        }) as never
    );
    const refresh = view.getByRole("button", { name: "action.refresh" });
    await user.click(refresh);
    expect(refresh).toBeDisabled();
    expect(rowReasons()).toHaveLength(2);

    release();
    await waitFor(() => expect(refresh).toBeEnabled());
  });

  it("filters by object, reason and message", async () => {
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openEvents(user);
    await waitFor(() => expect(rowReasons()).toHaveLength(2));
    const input = screen.getByPlaceholderText("asset.k8sEventsFilterPlaceholder");

    await user.type(input, "web-1");
    expect(rowReasons()).toEqual(["FailedScheduling"]);
    await user.clear(input);
    await user.type(input, "backoff");
    expect(rowReasons()).toEqual(["BackOff"]);
    await user.clear(input);
    await user.type(input, "nodes are available");
    expect(rowReasons()).toEqual(["FailedScheduling"]);
    await user.clear(input);
    await user.type(input, "zzz");
    expect(rowReasons()).toEqual([]);
  });

  it("opens the involved object's detail from the object link, and plain text when its kind is unknown", async () => {
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    await openEvents(user);
    await waitFor(() => expect(rowReasons()).toHaveLength(2));

    await user.click(within(screen.getAllByTestId("k8s-event-row")[0]).getByTestId("k8s-event-object"));
    const clusters = useK8sStore.getState().clusters;
    expect(clusters[Number(Object.keys(clusters).pop())].activeTabId).toBe("res:/pods:default:api-1");
  });

  it("shows a failed read with retry instead of an empty list", async () => {
    vi.mocked(ListK8sEvents).mockRejectedValueOnce(new Error("events forbidden"));
    const user = userEvent.setup();
    render(<K8sClusterPage asset={asset(nextId++)} />);
    const view = await openEvents(user);
    await waitFor(() =>
      expect(within(view).getByTestId("async-state-error").textContent).toContain("events forbidden")
    );
    await user.click(within(view).getByTestId("async-state-retry"));
    await waitFor(() => expect(rowReasons()).toHaveLength(2));
  });
});
