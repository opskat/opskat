import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomTypeSection } from "@/components/settings/CustomTypeSection";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { useAssetStore } from "@/stores/assetStore";
import { customtype } from "../../../../wailsjs/go/models";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

// 全局 mock 的 t() 只回显 key;这里同时回显参数,才能断言资产名随消息带出。
vi.mock("react-i18next", () => {
  const t = (key: string, opts?: Record<string, unknown>) => (opts ? `${key} ${JSON.stringify(opts)}` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return {
    useTranslation: () => ({ t, i18n }),
    initReactI18next: { type: "3rdParty", init: vi.fn() },
  };
});

describe("CustomTypeSection", () => {
  beforeEach(async () => {
    toastError.mockClear();
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.ListCustomTypes).mockReset();
    vi.mocked(mod.DeleteCustomType).mockReset();
    vi.mocked(mod.GetCustomTypeUsage).mockReset();
    vi.mocked(mod.ExportCustomType).mockReset();
    vi.mocked(mod.SelectImportTypeFile).mockReset();
    useCustomTypeStore.setState({ types: [], loading: false, loaded: false });
  });

  it("exports a type via the per-row export button", async () => {
    const { ListCustomTypes, ExportCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 0 },
    ]);
    vi.mocked(ExportCustomType).mockResolvedValue(undefined);

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByTestId("customtype-export-1"));
    expect(ExportCustomType).toHaveBeenCalledWith(1);
  });

  it("opens the import preview dialog after selecting a file", async () => {
    const { ListCustomTypes, SelectImportTypeFile } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([]);
    vi.mocked(SelectImportTypeFile).mockResolvedValue(
      new customtype.ImportPreview({
        type: {
          id: 0,
          slug: "grafana",
          name: "Grafana",
          icon: "",
          execMode: "http",
          fields: [{ name: "host", label: "Host", secret: false, required: true }],
          http: { base_url: "https://{{host}}", auth: [] },
          usage: "",
          createtime: 0,
          updatetime: 0,
        },
        slugTaken: false,
      })
    );

    render(<CustomTypeSection />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("customtype-import-button"));
    expect(await screen.findByTestId("customtype-import-preview")).toBeInTheDocument();
    expect(screen.getByTestId("customtype-import-slug-input")).toHaveValue("grafana");
  });

  it("lists each type's execution mode and asset count", async () => {
    const { ListCustomTypes } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 3 },
      { id: 2, slug: "aws-cli", name: "AWS CLI", icon: "", execMode: "command", assetCount: 0 },
    ]);

    render(<CustomTypeSection />);

    expect(await screen.findByText("Grafana")).toBeInTheDocument();
    expect(screen.getByText("AWS CLI")).toBeInTheDocument();
    expect(screen.getByText(/grafana/)).toBeInTheDocument();
    expect(screen.getAllByText(/customType.assetCount/)).toHaveLength(2);
  });

  it("refreshes the asset counts when assets change", async () => {
    const { ListCustomTypes } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValueOnce([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 1 },
    ]);
    render(<CustomTypeSection />);
    expect(await screen.findByText(/customType.assetCount.*"count":1/)).toBeInTheDocument();

    vi.mocked(ListCustomTypes).mockResolvedValueOnce([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 4 },
    ]);
    act(() => useAssetStore.setState({ assets: [{ ID: 9 } as never] }));

    expect(await screen.findByText(/customType.assetCount.*"count":4/)).toBeInTheDocument();
  });

  it("reports an in-use type's assets directly without a confirm dialog or a delete call", async () => {
    const { ListCustomTypes, DeleteCustomType, GetCustomTypeUsage } =
      await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 2 },
    ]);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue(["grafana-prod", "grafana-dev"]);

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByRole("button", { name: /action.delete/ }));

    await waitFor(() => expect(toastError).toHaveBeenCalled());
    expect(GetCustomTypeUsage).toHaveBeenCalledWith(1);
    // mocked t() 只回显 key 与 JSON 参数;资产名与数量必须随消息带出
    const msg = String(toastError.mock.calls[0][0]);
    expect(msg).toContain("customType.deleteInUse");
    expect(msg).toContain("grafana-prod, grafana-dev");
    expect(msg).toContain("Grafana");
    expect(screen.queryByText(/customType.deleteConfirmTitle/)).not.toBeInTheDocument();
    expect(DeleteCustomType).not.toHaveBeenCalled();
  });

  it("opens the confirm dialog for an unused type and deletes it on confirm", async () => {
    const { ListCustomTypes, DeleteCustomType, GetCustomTypeUsage } =
      await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 0 },
    ]);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue([]);
    vi.mocked(DeleteCustomType).mockResolvedValue({ deleted: true });

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByRole("button", { name: /action.delete/ }));
    expect(await screen.findByText(/customType.deleteConfirmTitle/)).toBeInTheDocument();
    expect(DeleteCustomType).not.toHaveBeenCalled();

    const confirmButtons = screen.getAllByRole("button", { name: /action.delete/ });
    await user.click(confirmButtons[confirmButtons.length - 1]);
    await waitFor(() => expect(DeleteCustomType).toHaveBeenCalledWith(1));
    expect(toastError).not.toHaveBeenCalled();
  });

  it("still reports the assets when the backend refuses a delete after the pre-check (race)", async () => {
    const { ListCustomTypes, DeleteCustomType, GetCustomTypeUsage } =
      await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 0 },
    ]);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue([]);
    vi.mocked(DeleteCustomType).mockResolvedValue({ deleted: false, assets: ["grafana-prod"] });

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByRole("button", { name: /action.delete/ }));
    await screen.findByText(/customType.deleteConfirmTitle/);
    const confirmButtons = screen.getAllByRole("button", { name: /action.delete/ });
    await user.click(confirmButtons[confirmButtons.length - 1]);

    await waitFor(() => expect(toastError).toHaveBeenCalledWith(expect.stringContaining("grafana-prod")));
    expect(screen.getByText("Grafana")).toBeInTheDocument();
  });
});
