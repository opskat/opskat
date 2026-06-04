import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AssetTypePicker } from "@/components/asset/AssetTypePicker";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k }),
}));

vi.mock("@/extension", () => ({
  useExtensionStore: (sel: (s: { extensions: Record<string, unknown> }) => unknown) => sel({ extensions: {} }),
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
