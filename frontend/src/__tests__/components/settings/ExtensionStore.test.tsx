import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, within } from "@testing-library/react";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { ExtensionStore } from "../../../components/settings/ExtensionStore";
import { ListStore, RefreshStore } from "../../../../wailsjs/go/extension/Extension";
import { useSettingsUiStore } from "../../../stores/settingsUiStore";

const caps = (over: Record<string, unknown> = {}) => ({
  fs: { read: [], write: [] },
  http: { allowlist: [] },
  credentials: "",
  tunnel: false,
  network: { assetEndpoint: false },
  ...over,
});

const card = (name: string, over: Record<string, unknown> = {}) => ({
  name,
  displayName: name.toUpperCase(),
  description: `${name} description`,
  icon: "",
  version: "1.0.0",
  installedVersion: "",
  action: "install",
  unavailable: null,
  capabilities: caps(),
  size: 2048,
  ...over,
});

const verified = (extensions: ReturnType<typeof card>[]) => ({
  updatedAt: Date.UTC(2026, 9, 5, 8, 0),
  verified: true,
  error: null,
  extensions,
});

const failed = (kind: string) => ({
  updatedAt: 0,
  verified: false,
  error: { kind, message: `${kind} detail` },
  extensions: [],
});

const notLoaded = { updatedAt: 0, verified: false, error: null, extensions: [] };

const sample = [
  card("es", {
    version: "0.2.0",
    capabilities: caps({ credentials: "read", network: { assetEndpoint: true } }),
  }),
  card("kafka", { action: "update", version: "0.4.1", installedVersion: "0.1.0" }),
  card("notebook", { action: "installed", installedVersion: "1.0.0" }),
  card("pgdoctor", {
    action: "unavailable",
    version: "0.6.0",
    unavailable: { reason: "hostABI", hostABI: "9.1", minAppVersion: "", sourceType: "" },
  }),
];

function serve(state: unknown) {
  vi.mocked(ListStore).mockResolvedValue(notLoaded as never);
  vi.mocked(RefreshStore).mockResolvedValue(state as never);
}

function renderStore(onInstall = vi.fn().mockResolvedValue(undefined)) {
  render(<ExtensionStore onInstall={onInstall} />);
  return onInstall;
}

const cardEl = (name: string) => screen.getByTestId(`ext-store-card-${name}`);

describe("ExtensionStore", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  it("settings → extensions toggles between the installed list and the store, refreshing on open", async () => {
    serve(verified(sample));
    render(<ExtensionSection />);
    expect(screen.getByText("extension.installed")).toBeInTheDocument();
    expect(RefreshStore).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("radio", { name: "extension.view.store" }));

    await screen.findByTestId("ext-store-card-es");
    expect(RefreshStore).toHaveBeenCalledWith("en");
    expect(screen.queryByText("extension.installed")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("radio", { name: "extension.view.installed" }));
    expect(screen.getByText("extension.installed")).toBeInTheDocument();
  });

  it("shows a local skeleton until the first index arrives", async () => {
    vi.mocked(ListStore).mockResolvedValue(notLoaded as never);
    let resolve!: (v: unknown) => void;
    vi.mocked(RefreshStore).mockReturnValue(new Promise((r) => (resolve = r)) as never);
    renderStore();

    expect(await screen.findByTestId("ext-store-skeleton")).toBeInTheDocument();
    resolve(verified(sample));
    await screen.findByTestId("ext-store-card-es");
    expect(screen.queryByTestId("ext-store-skeleton")).not.toBeInTheDocument();
  });

  it("shows the last index at once while it refreshes", async () => {
    vi.mocked(ListStore).mockResolvedValue(verified(sample) as never);
    vi.mocked(RefreshStore).mockReturnValue(new Promise(() => {}) as never);
    renderStore();
    expect(await screen.findByTestId("ext-store-card-es")).toBeInTheDocument();
    expect(screen.queryByTestId("ext-store-skeleton")).not.toBeInTheDocument();
  });

  it("shows when the verified index was updated", async () => {
    serve(verified(sample));
    renderStore();
    const status = await screen.findByTestId("ext-store-status");
    expect(status).toHaveTextContent("extension.store.updatedAt");
    expect(status).toHaveTextContent("extension.store.verified");
  });

  it("each card shows name, version, description, capabilities, size and its status button", async () => {
    serve(verified(sample));
    renderStore();
    await screen.findByTestId("ext-store-card-es");

    const es = within(cardEl("es"));
    expect(es.getByText("ES")).toBeInTheDocument();
    expect(es.getByText("v0.2.0")).toBeInTheDocument();
    expect(es.getByText("es description")).toBeInTheDocument();
    expect(es.getByText("2.0 KB")).toBeInTheDocument();
    expect(es.getByText("extension.capability.assetEndpoint")).toBeInTheDocument();
    expect(es.getByText("extension.capability.credentials").closest("[data-warning]")).toHaveClass("text-warning");
    expect(es.getByRole("button", { name: "extension.store.install" })).toBeEnabled();

    const kafka = within(cardEl("kafka"));
    expect(kafka.getByRole("button", { name: "extension.store.updateTo" })).toBeEnabled();
    expect(kafka.getByText(/extension\.store\.installedVersion/)).toBeInTheDocument();

    expect(within(cardEl("notebook")).getByRole("button", { name: "extension.store.installed" })).toBeDisabled();

    const pg = within(cardEl("pgdoctor"));
    expect(pg.getByRole("button", { name: "extension.store.unavailable" })).toBeDisabled();
    expect(pg.getByText("extension.store.reason.hostABI")).toBeInTheDocument();
  });

  it("install and update hand the card to onInstall, then reload the store", async () => {
    serve(verified(sample));
    const onInstall = renderStore();
    await screen.findByTestId("ext-store-card-es");
    vi.mocked(ListStore).mockClear();
    vi.mocked(ListStore).mockResolvedValue(verified(sample) as never);

    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    await waitFor(() => expect(onInstall).toHaveBeenCalledWith(expect.objectContaining({ name: "es" })));
    await waitFor(() => expect(ListStore).toHaveBeenCalledWith("en"));

    fireEvent.click(within(cardEl("kafka")).getByRole("button", { name: "extension.store.updateTo" }));
    await waitFor(() => expect(onInstall).toHaveBeenCalledWith(expect.objectContaining({ name: "kafka" })));
  });

  it("search matches name and description, and says when nothing matches", async () => {
    serve(verified(sample));
    renderStore();
    await screen.findByTestId("ext-store-card-es");
    const search = screen.getByPlaceholderText("extension.store.searchPlaceholder");

    fireEvent.change(search, { target: { value: "KAFKA" } });
    expect(screen.getByTestId("ext-store-card-kafka")).toBeInTheDocument();
    expect(screen.queryByTestId("ext-store-card-es")).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "notebook description" } });
    expect(screen.getByTestId("ext-store-card-notebook")).toBeInTheDocument();
    expect(screen.queryByTestId("ext-store-card-kafka")).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: "zzz-nothing" } });
    expect(screen.getByText("extension.store.noResults")).toBeInTheDocument();
  });

  it("refresh fetches the index again", async () => {
    serve(verified(sample));
    renderStore();
    await screen.findByTestId("ext-store-card-es");
    expect(RefreshStore).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "extension.store.refresh" }));
    await waitFor(() => expect(RefreshStore).toHaveBeenCalledTimes(2));
  });

  it.each([
    ["fetch", "extension.store.fetchFailed"],
    ["signature", "extension.store.signatureInvalid"],
  ])("%s failure shows no cards and offers retry and change mirror", async (kind, title) => {
    serve(failed(kind));
    renderStore();
    expect(await screen.findByText(title)).toBeInTheDocument();
    expect(screen.queryByTestId(/^ext-store-card-/)).not.toBeInTheDocument();
    expect(screen.getByText(`${kind} detail`)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "extension.store.retry" }));
    await waitFor(() => expect(RefreshStore).toHaveBeenCalledTimes(2));

    useSettingsUiStore.setState({ activeTab: "extensions" });
    fireEvent.click(screen.getByRole("button", { name: "extension.store.changeMirror" }));
    expect(useSettingsUiStore.getState().activeTab).toBe("about");
  });

  it("an unknown index format asks to update OpsKat instead of showing the store", async () => {
    serve(failed("format"));
    renderStore();
    expect(await screen.findByText("extension.store.unknownFormat")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "extension.store.changeMirror" })).not.toBeInTheDocument();

    useSettingsUiStore.setState({ activeTab: "extensions" });
    fireEvent.click(screen.getByRole("button", { name: "extension.store.checkUpdate" }));
    expect(useSettingsUiStore.getState().activeTab).toBe("about");
  });

  it("says so when the store has no extensions", async () => {
    serve(verified([]));
    renderStore();
    expect(await screen.findByText("extension.store.empty")).toBeInTheDocument();
  });
});
