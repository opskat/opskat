import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomTypeSection } from "@/components/settings/CustomTypeSection";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { customtype } from "../../../../wailsjs/go/models";

const { toastError } = vi.hoisted(() => ({ toastError: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError, success: vi.fn() } }));

describe("CustomTypeSection", () => {
  beforeEach(async () => {
    toastError.mockClear();
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.ListCustomTypes).mockReset();
    vi.mocked(mod.DeleteCustomType).mockReset();
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
    expect(screen.getAllByText("customType.assetCount")).toHaveLength(2);
  });

  it("refuses to delete an in-use type and reports the blocking assets instead of removing it", async () => {
    const { ListCustomTypes, DeleteCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(ListCustomTypes).mockResolvedValue([
      { id: 1, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 1 },
    ]);
    vi.mocked(DeleteCustomType).mockResolvedValue({ deleted: false, assets: ["grafana-prod"] });

    render(<CustomTypeSection />);
    const user = userEvent.setup();
    await screen.findByText("Grafana");

    await user.click(screen.getByRole("button", { name: "action.delete" }));
    const confirmButtons = await screen.findAllByRole("button", { name: "action.delete" });
    await user.click(confirmButtons[confirmButtons.length - 1]);

    // 拒绝原因走 toast.error(customType.deleteInUse),而不是当作删除成功处理
    // (mocked t() 只回显 key,资产名是否正确传参由 customTypeStore.test.ts 覆盖)。
    expect(toastError).toHaveBeenCalledWith(expect.stringContaining("customType.deleteInUse"));
    // 仍在列表里——没有被当作已删除处理
    expect(screen.getByText("Grafana")).toBeInTheDocument();
  });
});
