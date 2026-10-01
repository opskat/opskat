import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { PermissionModeCard } from "../PermissionModeCard";
import { useAssetStore } from "@/stores/assetStore";
import { useTabStore } from "@/stores/tabStore";
import { toast } from "sonner";
import { GetCommandReviewSettings } from "../../../../wailsjs/go/system/System";
import { group_entity } from "../../../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));

const group = (ID: number, Name: string, ParentID: number, permissionMode = "") =>
  new group_entity.Group({ ID, Name, ParentID, Icon: "", SortOrder: ID, Status: 1, permissionMode });

function mockApiKey(apiKeySet: boolean) {
  vi.mocked(GetCommandReviewSettings).mockResolvedValue({
    apiKeySet,
    model: "jev-1.13.0",
    timeoutMs: 5000,
    threshold: 0.2,
    lastFailReason: "",
    lastFailAt: 0,
  } as never);
}

async function renderCard(props: Partial<Parameters<typeof PermissionModeCard>[0]> = {}) {
  const onChange = vi.fn().mockResolvedValue(undefined);
  await act(async () => {
    render(<PermissionModeCard value="" subject="asset" parentGroupId={0} onChange={onChange} {...props} />);
  });
  return onChange;
}

const option = (key: string) => screen.getByRole("radio", { name: `commandReview.mode.${key}` });

describe("PermissionModeCard", () => {
  beforeEach(() => {
    mockApiKey(true);
    // 生产（autopilot）> 数据库（沿用）；测试（没设置）
    useAssetStore.setState({
      groups: [group(1, "生产", 0, "autopilot"), group(2, "数据库", 1), group(3, "测试", 0)],
    });
    useTabStore.setState({ tabs: [], activeTabId: null });
  });

  it("保存过程中显示“保存中”，不是“已保存”，也不能再切换", async () => {
    const onChange = vi.fn(() => new Promise<void>(() => {}));
    await act(async () => {
      render(<PermissionModeCard value="autopilot" subject="asset" parentGroupId={0} onChange={onChange} />);
    });
    await act(async () => {
      fireEvent.click(option("default"));
    });

    expect(onChange).toHaveBeenCalledWith("default");
    expect(screen.getByText("action.saving")).toBeInTheDocument();
    expect(screen.queryByText(/settings\.saved/)).toBeNull();
    expect(option("assisted")).toBeDisabled();
  });

  it("保存失败时提示错误", async () => {
    const onChange = vi.fn().mockRejectedValue(new Error("db locked"));
    await act(async () => {
      render(<PermissionModeCard value="autopilot" subject="asset" parentGroupId={0} onChange={onChange} />);
    });
    await act(async () => {
      fireEvent.click(option("default"));
    });

    expect(toast.error).toHaveBeenCalledWith("db locked");
    expect(screen.queryByText("action.saving")).toBeNull();
  });

  it("切回默认不需要确认，直接保存", async () => {
    const onChange = await renderCard({ value: "autopilot" });
    fireEvent.click(option("default"));
    expect(onChange).toHaveBeenCalledWith("default");
    expect(screen.queryByRole("alertdialog")).toBeNull();
  });

  it("开启 Autopilot 先弹确认，确认后才保存", async () => {
    const onChange = await renderCard();
    fireEvent.click(option("autopilot"));
    expect(onChange).not.toHaveBeenCalled();

    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByText("commandReview.mode.confirmAutopilot")).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(within(dialog).getByRole("button", { name: "commandReview.mode.confirm" }));
    });
    expect(onChange).toHaveBeenCalledWith("autopilot");
  });

  it("沿用的分组开着 Autopilot 时，切到沿用也要确认", async () => {
    const onChange = await renderCard({ value: "default", parentGroupId: 2 });
    fireEvent.click(option("inheritOption.asset"));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("沿用时写明来自哪个分组，点分组名打开它的详情", async () => {
    await renderCard({ value: "", parentGroupId: 2 });
    const source = screen.getByTestId("permission-mode-inherit-source");
    expect(source).toHaveTextContent("commandReview.mode.inheritFrom.asset");
    expect(screen.getByText("commandReview.mode.autopilotDesc")).toBeInTheDocument();

    fireEvent.click(within(source).getByRole("button", { name: "生产" }));
    expect(useTabStore.getState().activeTabId).toBe("info-group-1");
    expect(useAssetStore.getState().selectedGroupId).toBe(1);
  });

  // 往上都没设置时，给出所在（上级）分组的入口，方便去那里设置；没有分组就不给。
  it.each([
    ["asset", 0, "inheritNone.ungrouped", null],
    ["asset", 3, "inheritNone.asset", "测试"],
    ["group", 0, "inheritNone.top", null],
    ["group", 3, "inheritNone.group", "测试"],
  ] as const)("%s 从分组 %i 沿用不到设置时按默认处理", async (subject, parentGroupId, key, link) => {
    await renderCard({ value: "", subject, parentGroupId });
    const source = screen.getByTestId("permission-mode-inherit-source");
    expect(source).toHaveTextContent(`commandReview.mode.${key}`);
    expect(within(source).queryByRole("button")?.textContent ?? null).toBe(link);
    expect(screen.getByText("commandReview.mode.defaultDesc")).toBeInTheDocument();
  });

  it("自己设置了模式时不显示沿用来源", async () => {
    await renderCard({ value: "assisted", parentGroupId: 2 });
    expect(screen.queryByTestId("permission-mode-inherit-source")).toBeNull();
  });

  it("需要审核但没配置 API key 时提示，确认弹窗里也提醒", async () => {
    mockApiKey(false);
    await renderCard({ value: "assisted" });
    expect(screen.getByTestId("permission-mode-no-api-key")).toBeInTheDocument();

    fireEvent.click(option("autopilot"));
    expect(within(screen.getByRole("alertdialog")).getByText("commandReview.mode.noApiKey")).toBeInTheDocument();
  });

  it("默认模式下不提示 API key", async () => {
    mockApiKey(false);
    await renderCard({ value: "default" });
    expect(screen.queryByTestId("permission-mode-no-api-key")).toBeNull();
  });
});
