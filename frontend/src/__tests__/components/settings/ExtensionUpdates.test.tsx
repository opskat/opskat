import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, act, within } from "@testing-library/react";
import { ExtensionStore } from "../../../components/settings/ExtensionStore";
import { SettingsPage } from "../../../components/settings/SettingsPage";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { useExtensionUpdates } from "../../../components/settings/extensionUpdates";
import {
  InstallStoreExtension,
  ListInstalledExtensions,
  ListStore,
  RefreshStore,
} from "../../../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";

vi.mock("../../../lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

// SettingsPage's other sections are not under test (and need their own backends).
vi.mock("../../../components/settings/ShortcutSettings", () => ({ ShortcutSettings: () => null }));
vi.mock("../../../components/settings/AISettingsSection", () => ({ AISettingsSection: () => null }));
vi.mock("../../../components/settings/ImportSection", () => ({ ImportSection: () => null }));
vi.mock("../../../components/settings/BackupSection", () => ({ BackupSection: () => null }));
vi.mock("../../../components/settings/AppearanceSection", () => ({
  AppearanceSection: () => null,
  TerminalSection: () => null,
}));
vi.mock("../../../components/settings/ConnectionSection", () => ({ ConnectionSection: () => null }));
vi.mock("../../../components/settings/UpdateSection", () => ({ UpdateSection: () => null }));
vi.mock("../../../components/settings/SystemStatusSection", () => ({ SystemStatusSection: () => null }));
vi.mock("../../../components/settings/ExternalEditSection", () => ({ ExternalEditSection: () => null }));

const caps = {
  fs: { read: [], write: [] },
  http: { allowlist: [] },
  credentials: "",
  tunnel: false,
  network: { assetEndpoint: false },
};

const card = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  displayName: name.toUpperCase(),
  description: "",
  icon: "",
  version: "0.4.1",
  installedVersion: "0.1.0",
  action: "update",
  unavailable: null,
  capabilities: caps,
  size: 2048,
  ...over,
});

const state = (...extensions: ReturnType<typeof card>[]) => ({
  updatedAt: Date.UTC(2026, 9, 5),
  verified: true,
  error: null,
  extensions,
});

const installedExt = (name: string, version = "0.1.0") => ({
  name,
  version,
  icon: "",
  displayName: name,
  description: "",
  enabled: true,
  manifest: { name, version, capabilities: {} },
});

const landed = (name: string) => ({ name, version: "0.4.1", canceled: false, error: null });

beforeEach(() => {
  vi.clearAllMocks();
  useExtensionUpdates.setState({ updates: {} });
  vi.mocked(ListInstalledExtensions).mockResolvedValue([installedExt("kafka"), installedExt("notebook")] as never);
  vi.mocked(ListStore).mockResolvedValue(state(card("kafka"), card("notebook", { action: "installed" })) as never);
});
afterEach(cleanup);

describe("installed list: update available", () => {
  it("offers the compatible newer version only on the extension that has one, and runs the store update", async () => {
    vi.mocked(InstallStoreExtension).mockResolvedValue(landed("kafka") as never);
    render(<ExtensionSection />);
    await screen.findByText("notebook");
    act(() =>
      useExtensionUpdates
        .getState()
        .setFromState(state(card("kafka"), card("notebook", { action: "installed" })) as never)
    );

    const buttons = screen.getAllByRole("button", { name: /extension\.store\.updateTo/ });
    expect(buttons).toHaveLength(1);

    vi.mocked(ListStore).mockResolvedValue(state(card("kafka", { action: "installed" })) as never);
    fireEvent.click(buttons[0]);
    await waitFor(() => expect(InstallStoreExtension).toHaveBeenCalledWith("kafka"));
    await waitFor(() => expect(screen.queryByRole("button", { name: /extension\.store\.updateTo/ })).toBeNull());
    expect(useExtensionUpdates.getState().updates).toEqual({});
  });

  it("shows the failure under the extension and keeps the update offered", async () => {
    vi.mocked(InstallStoreExtension).mockResolvedValue({
      name: "kafka",
      version: "",
      canceled: false,
      error: { kind: "network", message: "dial tcp: timeout", expected: "", actual: "" },
    } as never);
    render(<ExtensionSection />);
    await screen.findByText("notebook");
    act(() => useExtensionUpdates.getState().setFromState(state(card("kafka")) as never));

    fireEvent.click(screen.getByRole("button", { name: /extension\.store\.updateTo/ }));
    const alert = await screen.findByRole("alert");
    expect(within(alert).getByText("dial tcp: timeout")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /extension\.store\.retry/ })).toBeInTheDocument();
  });
});

function emit(event: string) {
  const call = vi.mocked(EventsOn).mock.calls.find((c) => c[0] === event);
  if (!call) throw new Error(`${event} handler not registered`);
  return act(async () => (call[1] as () => void)());
}

describe("settings extensions tab badge", () => {
  it("shows the number of updatable extensions and clears after a refresh finds none", async () => {
    vi.mocked(ListStore).mockResolvedValue(state(card("kafka"), card("es", { name: "es" })) as never);
    render(<SettingsPage />);
    const badge = await screen.findByText("extension.updatesAvailable");
    expect(badge).toHaveClass("sr-only");
    expect(screen.getByRole("tab", { name: /extension\.title/ })).toHaveTextContent("2");

    vi.mocked(ListStore).mockResolvedValue(state(card("kafka", { action: "installed" })) as never);
    await emit("ext:store-refreshed");
    await waitFor(() => expect(screen.queryByText("extension.updatesAvailable")).toBeNull());
  });

  it("has no badge when nothing can be updated", async () => {
    vi.mocked(ListStore).mockResolvedValue(state(card("kafka", { action: "installed" })) as never);
    render(<SettingsPage />);
    await waitFor(() => expect(ListStore).toHaveBeenCalled());
    expect(screen.queryByText("extension.updatesAvailable")).toBeNull();
  });
});

describe("store page feeds the updates", () => {
  it("a manual refresh that finds an update publishes it", async () => {
    vi.mocked(ListStore).mockResolvedValue(state(card("kafka", { action: "installed" })) as never);
    vi.mocked(RefreshStore).mockResolvedValue(state(card("kafka")) as never);
    render(<ExtensionStore onInstall={vi.fn()} onChangeMirror={vi.fn()} />);
    await waitFor(() => expect(Object.keys(useExtensionUpdates.getState().updates)).toEqual(["kafka"]));
  });
});
