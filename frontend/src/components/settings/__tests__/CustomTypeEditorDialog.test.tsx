/* eslint-disable @typescript-eslint/no-explicit-any */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() } }));

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
      issues: [{ path: "name", code: "name_required" }],
    } as any);

    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(await screen.findByText("customType.issue.name_required")).toBeInTheDocument();
  });

  // 问题所在的标签页没打开时,保存被拒必须切过去——否则对话框原地不动、没有任何提示。
  it("switches to the tab that holds the rejected field when it is not the open one", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      issues: [{ path: "http.base_url", code: "base_url_required" }],
    } as any);
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(await screen.findByText("customType.issue.base_url_required")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-request")).toHaveAttribute("aria-selected", "true");
  });

  it("stays on the open tab when it already shows one of the rejected fields", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      issues: [
        { path: "fields[0].name", code: "field_name_invalid" },
        { path: "http.base_url", code: "base_url_required" },
      ],
    } as any);
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByTestId("config-tab-request"));

    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(await screen.findByText("customType.issue.base_url_required")).toBeInTheDocument();
    expect(screen.getByTestId("config-tab-request")).toHaveAttribute("aria-selected", "true");
  });

  // 打开「编辑 A」后立刻关掉再点「新建」:A 的加载结果晚到时不能灌进新建表单(带着 A 的 id,
  // 一保存就覆盖 A)。
  it("ignores a type that finishes loading after the dialog moved on to another target", async () => {
    const { GetCustomType, GetCustomTypeUsage, SaveCustomType } =
      await import("../../../../wailsjs/go/customtype/CustomType");
    let resolveA!: (ct: any) => void;
    vi.mocked(GetCustomType).mockReturnValue(new Promise((r) => (resolveA = r)) as any);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue([]);
    vi.mocked(SaveCustomType).mockResolvedValue({ issues: [{ path: "name", code: "name_required" }] } as any);

    const { rerender } = render(<CustomTypeEditorDialog open typeId={3} onOpenChange={vi.fn()} />);
    rerender(<CustomTypeEditorDialog open={false} typeId={3} onOpenChange={vi.fn()} />);
    rerender(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    await act(async () => {
      resolveA({ id: 3, slug: "type-a", name: "Type A", icon: "", execMode: "http", fields: [], usage: "" });
    });

    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));
    const sent = vi.mocked(SaveCustomType).mock.calls[0][0];
    expect(sent.id).toBeFalsy();
    expect(sent.slug).toBe("");
  });

  // 默认策略里引用的权限组不在编辑器里编辑(导入的类型可能带着),保存不能把它们丢掉。
  it("keeps the default policy's groups it does not edit", async () => {
    const { GetCustomType, GetCustomTypeUsage, SaveCustomType } =
      await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(GetCustomType).mockResolvedValue({
      id: 3,
      slug: "grafana",
      name: "Grafana",
      icon: "",
      execMode: "http",
      fields: [{ name: "host", label: "", secret: false, required: true }],
      http: { base_url: "https://{{host}}" },
      usage: "",
      defaultPolicy: { allow_list: ["GET *"], deny_list: [], groups: ["builtin:readonly"] },
    } as any);
    vi.mocked(GetCustomTypeUsage).mockResolvedValue([]);
    vi.mocked(SaveCustomType).mockResolvedValue({ issues: [{ path: "name", code: "name_required" }] } as any);

    render(<CustomTypeEditorDialog open typeId={3} onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await screen.findByDisplayValue("Grafana");
    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(vi.mocked(SaveCustomType).mock.calls[0][0].defaultPolicy).toEqual({
      allow_list: ["GET *"],
      deny_list: [],
      groups: ["builtin:readonly"],
    });
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

  it("surfaces the non-blocking save warnings to the user even though the dialog closes", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    const { toast } = await import("sonner");
    vi.mocked(toast.warning).mockReset();
    vi.mocked(SaveCustomType).mockResolvedValue({
      type: { id: 6, slug: "aws", name: "AWS", execMode: "command", fields: [] },
      warnings: [{ path: "command.template", code: "command_secret_in_args" }],
    } as any);
    const onOpenChange = vi.fn();

    render(<CustomTypeEditorDialog open onOpenChange={onOpenChange} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));

    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(toast.warning).toHaveBeenCalledWith("customType.issue.command_secret_in_args");
  });

  it("prefills the default policy by execution mode for a new type and saves it", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({ issues: [{ path: "name", code: "name_required" }] } as any);
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

  it("keeps a template input as the same element while focused so the layout does not shift on blur", async () => {
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByTestId("config-tab-request"));

    const input = screen.getByLabelText("customType.baseUrl");
    await user.click(input);
    expect(screen.getByLabelText("customType.baseUrl")).toBe(input);

    await user.tab();
    expect(screen.getByLabelText("customType.baseUrl")).toBe(input);
  });

  it("drops a field's validation message as soon as that field is edited, without saving again", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      issues: [
        { path: "name", code: "name_required" },
        { path: "slug", code: "slug_required" },
      ],
    } as any);
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "action.save" }));
    expect(await screen.findByText("customType.issue.name_required")).toBeInTheDocument();

    const [nameInput] = screen.getAllByRole("textbox");
    await user.type(nameInput, "G");

    expect(screen.queryByText("customType.issue.name_required")).not.toBeInTheDocument();
    expect(screen.getByText("customType.issue.slug_required")).toBeInTheDocument();
    expect(vi.mocked(SaveCustomType)).toHaveBeenCalledTimes(1);
  });

  it("drops the message of a template input once its value is edited", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue({
      issues: [{ path: "http.base_url", code: "base_url_required" }],
    } as any);
    render(<CustomTypeEditorDialog open onOpenChange={vi.fn()} />);
    const user = userEvent.setup();
    await user.click(screen.getByTestId("config-tab-request"));
    await user.click(screen.getByRole("button", { name: "action.save" }));
    expect(await screen.findByText("customType.issue.base_url_required")).toBeInTheDocument();

    await user.type(screen.getByLabelText("customType.baseUrl"), "h");

    expect(screen.queryByText("customType.issue.base_url_required")).not.toBeInTheDocument();
  });
});
