import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, act, fireEvent, within } from "@testing-library/react";
import { ExtensionInstallConfirmDialog } from "@/extension/InstallConfirmDialog";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { RespondExtensionInstallConfirm } from "../../wailsjs/go/extension/Extension";

// 插值参数必须可观察：本文件的 t 把参数一并渲染出来（同 OpsctlApprovalDialogExtension.test.tsx）。
vi.mock("react-i18next", () => {
  const t = (key: string, params?: Record<string, unknown>) => (params ? `${key}(${JSON.stringify(params)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return {
    useTranslation: () => ({ t, i18n }),
    initReactI18next: { type: "3rdParty", init: vi.fn() },
  };
});

function captureHandlers() {
  const handlers = new Map<string, (data: unknown) => void>();
  vi.mocked(EventsOn).mockImplementation(((event: string, handler: (data: unknown) => void) => {
    handlers.set(event, handler);
    return vi.fn();
  }) as never);
  return handlers;
}

// internal/app/extension confirmInstall 发出的 ext:install-confirm 事件形状。
function fire(handlers: Map<string, (data: unknown) => void>, event: string, payload: Record<string, unknown>) {
  const handler = handlers.get(event);
  if (!handler) throw new Error(`${event} handler not registered`);
  act(() => handler(payload));
}

const upgrade = {
  id: "ext_install_1",
  name: "acme",
  displayName: "Acme Store",
  icon: "database",
  from: "1.0.0",
  to: "2.0.0",
  downgrade: false,
  source: "file",
  size: 2048,
  capabilities: [
    { kind: "http", value: "https://api.acme.dev/" },
    { kind: "credentials", value: "read" },
  ],
  added: [{ kind: "credentials", value: "read" }],
};

describe("ExtensionInstallConfirmDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows what is being installed and marks the added grants and the credentials warning", () => {
    const handlers = captureHandlers();
    render(<ExtensionInstallConfirmDialog />);
    fire(handlers, "ext:install-confirm", upgrade);

    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("Acme Store")).toBeInTheDocument();
    expect(within(dialog).getByTestId("ext-install-version")).toHaveTextContent("1.0.0 → 2.0.0");
    expect(within(dialog).queryByText("extension.installConfirm.downgrade")).toBeNull();
    expect(within(dialog).getByText("extension.installConfirm.sourceFile")).toBeInTheDocument();
    expect(within(dialog).getByText("2.0 KB")).toBeInTheDocument();
    expect(within(dialog).getByRole("alert")).toHaveTextContent("extension.credentialsReadWarning");

    const http = within(dialog).getByTestId("ext-install-cap-http");
    expect(http).toHaveTextContent("https://api.acme.dev/");
    expect(within(http).queryByText("extension.installConfirm.added")).toBeNull();
    const creds = within(dialog).getByTestId("ext-install-cap-credentials");
    expect(within(creds).getByText("extension.installConfirm.added")).toBeInTheDocument();
  });

  it("flags a downgrade, names a fresh install's version alone and labels each source", () => {
    const handlers = captureHandlers();
    render(<ExtensionInstallConfirmDialog />);
    fire(handlers, "ext:install-confirm", { ...upgrade, from: "2.0.0", to: "1.5.0", downgrade: true, source: "dir" });
    let dialog = screen.getByRole("dialog");
    expect(within(dialog).getByTestId("ext-install-version")).toHaveTextContent("2.0.0 → 1.5.0");
    expect(within(dialog).getByText("extension.installConfirm.downgrade")).toBeInTheDocument();
    expect(within(dialog).getByText("extension.installConfirm.sourceDir")).toBeInTheDocument();
    fireEvent.click(within(dialog).getByTestId("ext-install-cancel"));

    fire(handlers, "ext:install-confirm", {
      ...upgrade,
      id: "ext_install_2",
      from: "",
      source: "store",
      capabilities: [],
      added: [],
    });
    dialog = screen.getByRole("dialog");
    expect(within(dialog).getByTestId("ext-install-version")).toHaveTextContent("2.0.0");
    expect(within(dialog).getByTestId("ext-install-version")).not.toHaveTextContent("→");
    expect(within(dialog).getByText("extension.installConfirm.sourceStore")).toBeInTheDocument();
    expect(within(dialog).getByText("extension.installConfirm.noCapabilities")).toBeInTheDocument();
    expect(within(dialog).queryByRole("alert")).toBeNull();
  });

  it("answers the confirm it shows: install accepts, cancel declines", () => {
    const handlers = captureHandlers();
    render(<ExtensionInstallConfirmDialog />);
    fire(handlers, "ext:install-confirm", upgrade);
    fire(handlers, "ext:install-confirm", { ...upgrade, id: "ext_install_2", displayName: "Second" });

    fireEvent.click(screen.getByTestId("ext-install-confirm"));
    expect(RespondExtensionInstallConfirm).toHaveBeenCalledWith("ext_install_1", true);

    // The queued confirm is next.
    expect(within(screen.getByRole("dialog")).getByText("Second")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("ext-install-cancel"));
    expect(RespondExtensionInstallConfirm).toHaveBeenCalledWith("ext_install_2", false);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("closes a confirm the backend stopped waiting for without answering it", () => {
    const handlers = captureHandlers();
    render(<ExtensionInstallConfirmDialog />);
    fire(handlers, "ext:install-confirm", upgrade);
    fire(handlers, "ext:install-confirm-closed", { id: "ext_install_1" });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(RespondExtensionInstallConfirm).not.toHaveBeenCalled();
  });
});
