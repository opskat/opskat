import { describe, it, expect, beforeEach, vi } from "vitest";
import type { ComponentProps } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TooltipProvider } from "@opskat/ui";
import { ImportDialog } from "@/components/settings/ImportDialog";
import { import_svc } from "../../wailsjs/go/models";

vi.mock("@/stores/assetStore", () => ({
  useAssetStore: () => ({ refresh: vi.fn().mockResolvedValue(undefined) }),
}));

function buildPreview(items: Partial<import_svc.PreviewItem>[]) {
  const groupIds = [...new Set(items.map((item) => item.groupId).filter((id): id is string => Boolean(id)))];
  return import_svc.PreviewResult.createFrom({
    groups: groupIds.map((id) => import_svc.PreviewGroup.createFrom({ id, name: id })),
    hasVault: false,
    items: items.map((item, i) =>
      import_svc.PreviewItem.createFrom({
        index: i,
        name: item.name ?? "",
        host: item.host ?? "",
        port: item.port ?? 22,
        exists: item.exists ?? false,
        ...item,
      })
    ),
  });
}

function renderDialog(props: Partial<ComponentProps<typeof ImportDialog>> = {}) {
  const base: ComponentProps<typeof ImportDialog> = {
    open: true,
    onOpenChange: vi.fn(),
    preview: buildPreview([{ name: "new-host", host: "2.2.2.2" }]),
    title: "test-import",
    onImport: vi
      .fn()
      .mockResolvedValue(
        import_svc.ImportResult.createFrom({ total: 1, success: 1, skipped: 0, failed: 0, errors: [] })
      ),
    ...props,
  };
  const view = render(
    <TooltipProvider>
      <ImportDialog {...base} />
    </TooltipProvider>
  );
  return { ...view, props: base };
}

async function confirmImport() {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /import\.confirmImport/ }));
}

describe("ImportDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows skipped and failed details in a result view instead of closing", async () => {
    const onImport = vi.fn().mockResolvedValue(
      import_svc.ImportResult.createFrom({
        total: 3,
        success: 1,
        skipped: 1,
        failed: 1,
        errors: [
          import_svc.ImportError.createFrom({
            name: "dup-host",
            status: "skipped",
            reason: "已存在，未开启覆盖",
          }),
          import_svc.ImportError.createFrom({ name: "bad-host", status: "failed", reason: "host 为空" }),
        ],
      })
    );
    renderDialog({ onImport });

    await confirmImport();

    // 明细窗口出现：跳过与失败条目都有名称与原因
    expect(await screen.findByText("dup-host")).toBeInTheDocument();
    expect(screen.getByText("bad-host")).toBeInTheDocument();
    expect(screen.getByText("已存在，未开启覆盖")).toBeInTheDocument();
    expect(screen.getByText("host 为空")).toBeInTheDocument();
    // 对话框保持打开（未调用 onOpenChange(false)）
    expect(screen.getByRole("button", { name: "action.close" })).toBeInTheDocument();
  });

  it("closes with no detail view when everything succeeded", async () => {
    const onOpenChange = vi.fn();
    renderDialog({ onOpenChange });

    await confirmImport();

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(screen.queryByRole("button", { name: "action.close" })).not.toBeInTheDocument();
  });

  it("never selects invalid preview items through defaults, select all, group, or item controls", async () => {
    const user = userEvent.setup();
    const onImport = vi
      .fn()
      .mockResolvedValue(
        import_svc.ImportResult.createFrom({ total: 1, success: 1, skipped: 0, failed: 0, errors: [] })
      );
    renderDialog({
      preview: buildPreview([
        { name: "valid-host", host: "10.0.0.1", groupId: "prod" },
        { name: "invalid-host", host: "10.0.0.2", groupId: "prod", reason: "端口无效" },
      ]),
      onImport,
    });

    const validCheckbox = screen.getByText("valid-host").closest("label")!.querySelector("input")!;
    const invalidCheckbox = screen.getByText("invalid-host").closest("label")!.querySelector("input")!;
    const groupCheckbox = screen.getAllByRole("checkbox")[0];
    const importButton = screen.getByRole("button", { name: /import\.confirmImport/ });

    expect(validCheckbox).toBeChecked();
    expect(invalidCheckbox).not.toBeChecked();
    expect(invalidCheckbox).toBeDisabled();
    expect(importButton).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "import.selectAll" }));
    expect(validCheckbox).toBeChecked();
    expect(invalidCheckbox).not.toBeChecked();

    await user.click(validCheckbox);
    expect(importButton).toBeDisabled();
    await user.click(groupCheckbox);
    expect(validCheckbox).toBeChecked();
    expect(invalidCheckbox).not.toBeChecked();

    await user.click(importButton);
    await waitFor(() => expect(onImport).toHaveBeenCalledWith([0], { passphrase: "", overwrite: false }));
  });

  it("keeps an all-invalid group unchecked with no indeterminate, and its toggle is a no-op", async () => {
    const user = userEvent.setup();
    const onImport = vi
      .fn()
      .mockResolvedValue(
        import_svc.ImportResult.createFrom({ total: 1, success: 1, skipped: 0, failed: 0, errors: [] })
      );
    renderDialog({
      preview: buildPreview([
        { name: "bad-a", host: "1.1.1.1", groupId: "empty", reason: "缺少用户名" },
        { name: "bad-b", host: "1.1.1.2", groupId: "empty", reason: "端口无效" },
      ]),
      onImport,
    });

    // 分组复选框在子项之前渲染，取第一个
    const groupCheckbox = screen.getAllByRole("checkbox")[0] as HTMLInputElement;
    const importButton = screen.getByRole("button", { name: /import\.confirmImport/ });

    expect(groupCheckbox).not.toBeChecked();
    expect(groupCheckbox.indeterminate).toBe(false);
    expect(importButton).toBeDisabled();

    await user.click(groupCheckbox);

    // 全空分组的 toggle 无副作用：仍无选中项，导入按钮仍禁用
    expect(groupCheckbox).not.toBeChecked();
    expect(groupCheckbox.indeterminate).toBe(false);
    expect(importButton).toBeDisabled();
    expect(onImport).not.toHaveBeenCalled();
  });
});
