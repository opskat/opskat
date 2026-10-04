import { beforeEach, describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";
import { useCustomTypeStore } from "@/stores/customTypeStore";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import { useTabStore } from "@/stores/tabStore";
import { customtype } from "../../wailsjs/go/models";

// 编辑器本身由设置页的测试覆盖;这里只模拟"保存了一个新类型"——store.save 会先重新加载列表再回调 onSaved。
vi.mock("@/components/settings/CustomTypeEditorDialog", () => ({
  CustomTypeEditorDialog: ({ open, onSaved }: { open: boolean; onSaved?: () => void }) =>
    open ? (
      <button
        type="button"
        data-testid="fake-editor-save"
        onClick={() => {
          useCustomTypeStore.setState((st) => ({
            types: [
              ...st.types,
              new customtype.Summary({
                id: 9,
                slug: "ding",
                name: "DingTalk",
                icon: "",
                execMode: "http",
                assetCount: 0,
              }),
            ],
          }));
          onSaved?.();
        }}
      />
    ) : null,
}));

describe("AssetTypePicker", () => {
  it("shows the current type label on the trigger", () => {
    render(<AssetTypePicker value="redis" onChange={() => {}} />);
    expect(screen.getByRole("combobox")).toHaveTextContent("nav.redis");
  });

  it("opens to grouped options and filters by search", async () => {
    const user = userEvent.setup();
    render(<AssetTypePicker value="ssh" onChange={() => {}} />);
    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText("assetType.group.servers")).toBeTruthy();
    expect(screen.getByText("assetType.group.databases")).toBeTruthy();
    expect(screen.getByText("nav.mongodb")).toBeTruthy();

    await user.type(screen.getByPlaceholderText("assetType.searchPlaceholder"), "mongo");
    // After filtering, "nav.ssh" should not appear in the options list
    // (the trigger still shows the selected label, so we query within the popover content)
    const popover = document.querySelector("[data-radix-popper-content-wrapper]");
    expect(popover).toBeTruthy();
    const inPopover = within(popover as HTMLElement);
    expect(inPopover.queryByText("nav.ssh")).toBeNull();
    expect(inPopover.getByText("nav.mongodb")).toBeTruthy();
  });

  it("calls onChange with the option value when an item is clicked", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AssetTypePicker value="ssh" onChange={onChange} />);
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByText("nav.mongodb"));
    expect(onChange).toHaveBeenCalledWith("mongodb");
  });
});

describe("AssetTypePicker custom types", () => {
  beforeEach(() => {
    useCustomTypeStore.setState({
      loaded: true,
      loading: false,
      types: [
        new customtype.Summary({ id: 7, slug: "grafana", name: "Grafana", icon: "", execMode: "http", assetCount: 0 }),
        new customtype.Summary({
          id: 8,
          slug: "aws-cli",
          name: "AWS CLI",
          icon: "",
          execMode: "command",
          assetCount: 0,
        }),
      ],
    });
  });

  it("lists each custom type under the custom group and selects it as a generic asset", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AssetTypePicker value="ssh" onChange={onChange} />);
    await user.click(screen.getByRole("combobox"));

    expect(screen.getByText("assetType.group.custom")).toBeTruthy();
    expect(screen.queryByText("assetType.generic")).toBeNull();
    expect(screen.getByTestId("asset-type-new-custom")).toBeTruthy();
    expect(screen.getByTestId("asset-type-manage-custom")).toBeTruthy();

    await user.click(screen.getByText("Grafana"));
    expect(onChange).toHaveBeenCalledWith("generic", "grafana");
  });

  it("shows the selected custom type on the trigger", () => {
    render(<AssetTypePicker value="generic" variant="aws-cli" onChange={() => {}} />);
    expect(screen.getByRole("combobox")).toHaveTextContent("AWS CLI");
  });

  it("manage opens the custom types settings tab and leaves the form", async () => {
    const user = userEvent.setup();
    const onLeave = vi.fn();
    render(<AssetTypePicker value="ssh" onChange={() => {}} onLeave={onLeave} />);
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByTestId("asset-type-manage-custom"));

    expect(useSettingsUiStore.getState().activeTab).toBe("custom-types");
    expect(useTabStore.getState().tabs.some((tab) => tab.id === "settings")).toBe(true);
    expect(onLeave).toHaveBeenCalled();
  });

  it("selects a newly created custom type when the editor saves", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<AssetTypePicker value="ssh" onChange={onChange} />);
    await user.click(screen.getByRole("combobox"));
    await user.click(screen.getByTestId("asset-type-new-custom"));
    await user.click(screen.getByTestId("fake-editor-save"));
    expect(onChange).toHaveBeenCalledWith("generic", "ding");
  });
});
