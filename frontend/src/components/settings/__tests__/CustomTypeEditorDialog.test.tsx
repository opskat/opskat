/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

describe("CustomTypeEditorDialog", () => {
  beforeEach(async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.SaveCustomType).mockReset();
    vi.mocked(mod.GetCustomType).mockReset();
    vi.mocked(mod.GetCustomTypeUsage).mockReset();
  });

  it("shows the command tab instead of the HTTP request tab once execution mode switches", async () => {
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("config-tab-request"));
    expect(screen.getByLabelText("customType.baseUrl")).toBeInTheDocument();

    await user.click(screen.getByRole("radio", { name: "customType.execModeCommand" }));
    await user.click(screen.getByTestId("config-tab-request"));
    expect(screen.getByLabelText("customType.commandTemplate")).toBeInTheDocument();
    expect(screen.queryByLabelText("customType.baseUrl")).not.toBeInTheDocument();
  });

  it("renders backend validation issues next to the field they belong to instead of a generic error", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      issues: [{ path: "name", message: "名称不能为空" }],
    } as any);

    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(await screen.findByText("名称不能为空")).toBeInTheDocument();
  });

  it("closes and reports success once the backend accepts the save", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      type: { id: 5, slug: "grafana", name: "Grafana", execMode: "http", fields: [] },
    } as any);
    const onOpenChange = vi.fn();

    render(<CustomTypeEditorDialog open onOpenChange={onOpenChange} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("shows how many assets use the type in the footer once it loads for editing", async () => {
    const { GetCustomType, GetCustomTypeUsage } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(GetCustomType).mockResolvedValue({
      id: 3,
      slug: "grafana",
      name: "Grafana",
      icon: "",
      execMode: "http",
      fields: [],
      usage: "",
      defaultPolicy: { allow_list: [], deny_list: [] },
      createtime: 0,
      updatetime: 0,
    } as any);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue(4);

    render(<CustomTypeEditorDialog open typeId={3} onOpenChange={vi.fn()} />);

    expect(await screen.findByText("customType.usageFooter")).toBeInTheDocument();
  });

  it("prefills the default policy by execution mode for a new type and saves it", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({ issues: [{ path: "name", message: "x" }] } as any);
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "action.save" }));
    expect(vi.mocked(SaveCustomType).mock.calls[0][0].defaultPolicy?.allow_list).toEqual([
      "GET *",
      "HEAD *",
      "OPTIONS *",
    ]);

    await user.click(screen.getByRole("radio", { name: "customType.execModeCommand" }));
    await user.click(screen.getByRole("button", { name: "action.save" }));
    expect(vi.mocked(SaveCustomType).mock.calls[1][0].defaultPolicy?.allow_list).toEqual([]);
  });

  it("warns that any command can read the injected env vars when the command template is empty", async () => {
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("radio", { name: "customType.execModeCommand" }));
    await user.click(screen.getByTestId("config-tab-request"));

    expect(screen.getByText("customType.commandTemplateEmptyHint")).toBeInTheDocument();
    await user.type(screen.getByLabelText("customType.commandTemplate"), "aws");
    expect(screen.queryByText("customType.commandTemplateEmptyHint")).not.toBeInTheDocument();
  });

  it("expands a template input to a multi-line textarea on focus and collapses back on blur", async () => {
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByTestId("config-tab-request"));

    const input = screen.getByLabelText("customType.baseUrl");
    expect(input.tagName).toBe("INPUT");

    await user.click(input);
    expect(screen.getByLabelText("customType.baseUrl").tagName).toBe("TEXTAREA");

    await user.tab();
    expect(screen.getByLabelText("customType.baseUrl").tagName).toBe("INPUT");
  });
});
