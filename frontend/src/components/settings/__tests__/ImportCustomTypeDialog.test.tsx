import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ImportCustomTypeDialog } from "@/components/settings/ImportCustomTypeDialog";
import { custom_type_entity, customtype } from "../../../../wailsjs/go/models";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() } }));

function httpPreview(overrides: Partial<custom_type_entity.CustomType> = {}): customtype.ImportPreview {
  return new customtype.ImportPreview({
    type: {
      id: 0,
      slug: "grafana",
      name: "Grafana",
      icon: "",
      execMode: "http",
      fields: [
        { name: "host", label: "Host", secret: false, required: true },
        { name: "token", label: "Token", secret: true, required: true },
      ],
      http: { base_url: "https://{{host}}", auth: [{ type: "header", name: "Authorization", values: ["{{token}}"] }] },
      usage: "",
      createtime: 0,
      updatetime: 0,
      ...overrides,
    },
    slugTaken: false,
  });
}

describe("ImportCustomTypeDialog", () => {
  beforeEach(async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.SaveCustomType).mockReset();
    vi.mocked(mod.ListCustomTypes).mockReset().mockResolvedValue([]);
  });

  it("shows rejection reasons and no confirm button when the file fails validation", async () => {
    const preview = new customtype.ImportPreview({
      issues: [{ path: "format", code: "version_unsupported", params: { version: "99", supported: "1" } }],
      slugTaken: false,
    });
    render(<ImportCustomTypeDialog preview={preview} onOpenChange={vi.fn()} />);

    expect(await screen.findByText("customType.issue.version_unsupported")).toBeInTheDocument();
    expect(screen.queryByTestId("customtype-import-confirm")).not.toBeInTheDocument();
  });

  it("prefills name/slug from the preview and previews fields + bindings", async () => {
    render(<ImportCustomTypeDialog preview={httpPreview()} onOpenChange={vi.fn()} />);

    expect(screen.getByTestId("customtype-import-slug-input")).toHaveValue("grafana");
    expect(screen.getByDisplayValue("Grafana")).toBeInTheDocument();
    expect(screen.getByText(/^host/)).toBeInTheDocument();
    expect(screen.getByText(/https:\/\/{{host}}/)).toBeInTheDocument();
  });

  it("requires a new slug on conflict: save() rejects the unchanged slug, succeeds once changed", async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.SaveCustomType).mockImplementation(async (ct) => {
      if (ct.slug === "grafana") {
        return new customtype.SaveResult({
          issues: [{ path: "slug", code: "slug_taken", params: { slug: "grafana", name: "Local" } }],
        });
      }
      return new customtype.SaveResult({ type: ct });
    });

    const preview = new customtype.ImportPreview({ ...httpPreview(), slugTaken: true });
    render(<ImportCustomTypeDialog preview={preview} onOpenChange={vi.fn()} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("customtype-import-confirm"));
    expect(await screen.findByText("customType.issue.slug_taken")).toBeInTheDocument();
    expect(screen.getByTestId("customtype-import-confirm")).toBeInTheDocument(); // dialog stays open

    const slugInput = screen.getByTestId("customtype-import-slug-input");
    await user.clear(slugInput);
    await user.type(slugInput, "grafana-2");
    await user.click(screen.getByTestId("customtype-import-confirm"));

    await vi.waitFor(() =>
      expect(mod.SaveCustomType).toHaveBeenCalledWith(expect.objectContaining({ slug: "grafana-2" }))
    );
  });

  it("confirming calls SaveCustomType and closes the dialog on success", async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    const onOpenChange = vi.fn();
    vi.mocked(mod.SaveCustomType).mockResolvedValue(new customtype.SaveResult({ type: httpPreview().type }));

    render(<ImportCustomTypeDialog preview={httpPreview()} onOpenChange={onOpenChange} />);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("customtype-import-confirm"));

    expect(mod.SaveCustomType).toHaveBeenCalled();
    await vi.waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
  });

  it("surfaces the non-blocking save warnings after importing, same as saving in the editor", async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    const { toast } = await import("sonner");
    vi.mocked(toast.warning).mockClear();
    vi.mocked(mod.SaveCustomType).mockResolvedValue(
      new customtype.SaveResult({
        type: httpPreview().type,
        warnings: [{ path: "command.template", code: "command_secret_in_args" }],
      })
    );
    const onOpenChange = vi.fn();

    render(<ImportCustomTypeDialog preview={httpPreview()} onOpenChange={onOpenChange} />);
    await userEvent.setup().click(screen.getByTestId("customtype-import-confirm"));

    await vi.waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
    expect(toast.warning).toHaveBeenCalledWith("customType.issue.command_secret_in_args");
  });
});
