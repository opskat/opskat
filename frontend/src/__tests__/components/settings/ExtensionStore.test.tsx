import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor, within, act } from "@testing-library/react";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { ExtensionStore } from "../../../components/settings/ExtensionStore";
import { InstallStoreExtension, ListStore, RefreshStore } from "../../../../wailsjs/go/extension/Extension";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import { useSettingsUiStore } from "../../../stores/settingsUiStore";
import { useStoreInstallState } from "../../../components/settings/useStoreInstalls";
import { toast } from "sonner";
import { notifySuccess } from "../../../lib/notify";

vi.mock("../../../lib/notify", () => ({ notifySuccess: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

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

const landed = (name: string, version = "1.0.0") => ({ name, version, canceled: false, error: null });

function renderStore(onInstall = vi.fn().mockResolvedValue(landed("es")), onChangeMirror = vi.fn()) {
  render(<ExtensionStore onInstall={onInstall} onChangeMirror={onChangeMirror} />);
  return onInstall;
}

// Captures the ext:store-progress handler the store subscribes with.
function captureProgress() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return (payload: Record<string, unknown>) => {
    const handler = handlers.get("ext:store-progress");
    if (!handler) throw new Error("ext:store-progress handler not registered");
    act(() => handler(payload));
  };
}

// An install the test settles by hand.
function pendingInstall() {
  let settle!: (v: unknown) => void;
  const onInstall = vi.fn().mockReturnValue(new Promise((r) => (settle = r)));
  return { onInstall, settle: (v: unknown) => act(async () => settle(v)) };
}

const cardEl = (name: string) => screen.getByTestId(`ext-store-card-${name}`);

describe("ExtensionStore", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useStoreInstallState.setState({ installs: {} });
  });
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

  it("an install shows download progress, then verifying, then installing", async () => {
    const progress = captureProgress();
    serve(verified(sample));
    const { onInstall, settle } = pendingInstall();
    renderStore(onInstall);
    await screen.findByTestId("ext-store-card-es");

    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    const es = within(cardEl("es"));
    expect(es.getByRole("button", { name: /extension\.store\.installing/ })).toBeDisabled();

    progress({ name: "es", phase: "downloading", done: 1024, total: 4096 });
    expect(es.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "25");
    expect(es.getByText("extension.store.progress.downloading")).toBeInTheDocument();

    progress({ name: "kafka", phase: "verifying", done: 1, total: 1 });
    expect(within(cardEl("kafka")).queryByText("extension.store.progress.verifying")).not.toBeInTheDocument();

    progress({ name: "es", phase: "verifying", done: 4096, total: 4096 });
    expect(es.getByText("extension.store.progress.verifying")).toBeInTheDocument();
    progress({ name: "es", phase: "installing", done: 4096, total: 4096 });
    expect(es.getByText("extension.store.progress.installing")).toBeInTheDocument();

    vi.mocked(ListStore).mockResolvedValue(verified(sample) as never);
    await settle(landed("es", "0.2.0"));
    expect(within(cardEl("es")).queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("a canceled install is silent", async () => {
    serve(verified(sample));
    const onInstall = renderStore(vi.fn().mockResolvedValue({ name: "", version: "", canceled: true, error: null }));
    await screen.findByTestId("ext-store-card-es");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    await waitFor(() => expect(onInstall).toHaveBeenCalled());
    await waitFor(() =>
      expect(within(cardEl("es")).getByRole("button", { name: "extension.store.install" })).toBeEnabled()
    );
    expect(within(cardEl("es")).queryByRole("alert")).not.toBeInTheDocument();
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("a sha256 mismatch names the expected and actual digests and offers retry, not a mirror change", async () => {
    serve(verified(sample));
    const failure = {
      name: "",
      version: "",
      canceled: false,
      error: { kind: "digest", message: "sha256 mismatch", expected: "aa11", actual: "bb22" },
    };
    const onChangeMirror = vi.fn();
    const onInstall = vi.fn().mockResolvedValueOnce(failure).mockResolvedValueOnce(landed("es"));
    renderStore(onInstall, onChangeMirror);
    await screen.findByTestId("ext-store-card-es");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));

    const alert = await within(cardEl("es")).findByRole("alert");
    expect(alert).toHaveTextContent("extension.store.installFailed.digest");
    expect(alert).toHaveTextContent("aa11");
    expect(alert).toHaveTextContent("bb22");
    expect(within(alert).queryByRole("button", { name: "extension.store.changeMirror" })).not.toBeInTheDocument();

    vi.mocked(ListStore).mockResolvedValue(verified(sample) as never);
    fireEvent.click(within(alert).getByRole("button", { name: "extension.store.retry" }));
    await waitFor(() => expect(onInstall).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(within(cardEl("es")).queryByRole("alert")).not.toBeInTheDocument());
  });

  it.each(["network", "auth", "registry"])("a %s failure offers retry and change mirror", async (kind) => {
    serve(verified(sample));
    const onChangeMirror = vi.fn();
    renderStore(
      vi.fn().mockResolvedValue({
        name: "",
        version: "",
        canceled: false,
        error: { kind, message: `${kind} detail`, expected: "", actual: "" },
      }),
      onChangeMirror
    );
    await screen.findByTestId("ext-store-card-es");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));

    const alert = await within(cardEl("es")).findByRole("alert");
    expect(alert).toHaveTextContent(`extension.store.installFailed.${kind}`);
    expect(alert).toHaveTextContent(`${kind} detail`);
    expect(within(alert).getByRole("button", { name: "extension.store.retry" })).toBeInTheDocument();
    fireEvent.click(within(alert).getByRole("button", { name: "extension.store.changeMirror" }));
    expect(onChangeMirror).toHaveBeenCalled();
  });

  it.each(["signature", "size", "incompatible", "mismatch", "install"])(
    "a %s failure is shown with its own title",
    async (kind) => {
      serve(verified(sample));
      renderStore(
        vi.fn().mockResolvedValue({
          name: "",
          version: "",
          canceled: false,
          error: { kind, message: "detail", expected: "", actual: "" },
        })
      );
      await screen.findByTestId("ext-store-card-es");
      fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
      expect(await within(cardEl("es")).findByRole("alert")).toHaveTextContent(`extension.store.installFailed.${kind}`);
    }
  );
});

describe("ExtensionSection store install", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useStoreInstallState.setState({ installs: {} });
  });
  afterEach(cleanup);

  it("installs through InstallStoreExtension and reports success", async () => {
    serve(verified(sample));
    vi.mocked(InstallStoreExtension).mockResolvedValue(landed("es", "0.2.0") as never);
    render(<ExtensionSection />);
    fireEvent.click(screen.getByRole("radio", { name: "extension.view.store" }));
    await screen.findByTestId("ext-store-card-es");

    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    await waitFor(() => expect(InstallStoreExtension).toHaveBeenCalledWith("es"));
    await waitFor(() => expect(notifySuccess).toHaveBeenCalledWith("extension.installSuccess"));
  });

  it("a landed install is not reported as failed when re-reading the store afterwards fails", async () => {
    serve(verified(sample));
    vi.mocked(InstallStoreExtension).mockResolvedValue(landed("es", "0.2.0") as never);
    render(<ExtensionSection />);
    fireEvent.click(screen.getByRole("radio", { name: "extension.view.store" }));
    await screen.findByTestId("ext-store-card-es");

    vi.mocked(ListStore).mockRejectedValue("store unavailable");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("store unavailable"));
    expect(notifySuccess).toHaveBeenCalledWith("extension.installSuccess");
    expect(toast.error).not.toHaveBeenCalledWith(expect.stringContaining("extension.installError"));
  });

  it("a canceled store install reports no success", async () => {
    serve(verified(sample));
    vi.mocked(InstallStoreExtension).mockResolvedValue({ name: "", version: "", canceled: true, error: null } as never);
    render(<ExtensionSection />);
    fireEvent.click(screen.getByRole("radio", { name: "extension.view.store" }));
    await screen.findByTestId("ext-store-card-es");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    await waitFor(() => expect(InstallStoreExtension).toHaveBeenCalledWith("es"));
    await waitFor(() =>
      expect(within(cardEl("es")).getByRole("button", { name: "extension.store.install" })).toBeEnabled()
    );
    expect(notifySuccess).not.toHaveBeenCalled();
  });

  it("change mirror opens the Extension downloads setting", async () => {
    serve(verified(sample));
    vi.mocked(InstallStoreExtension).mockResolvedValue({
      name: "",
      version: "",
      canceled: false,
      error: { kind: "network", message: "dial tcp: timeout", expected: "", actual: "" },
    } as never);
    render(<ExtensionSection />);
    fireEvent.click(screen.getByRole("radio", { name: "extension.view.store" }));
    await screen.findByTestId("ext-store-card-es");
    fireEvent.click(within(cardEl("es")).getByRole("button", { name: "extension.store.install" }));
    const alert = await within(cardEl("es")).findByRole("alert");

    fireEvent.click(within(alert).getByRole("button", { name: "extension.store.changeMirror" }));
    expect(await screen.findByText("extension.mirror.title")).toBeInTheDocument();
    expect(screen.queryByTestId("ext-store-card-es")).not.toBeInTheDocument();
  });
});
