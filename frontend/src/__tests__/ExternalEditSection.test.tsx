import fs from "node:fs";
import path from "node:path";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ExternalEditSection } from "../components/settings/ExternalEditSection";
import {
  builtInEditorID,
  getExternalEditSettings,
  saveExternalEditSettings,
  selectExternalEditorExecutable,
  selectExternalEditWorkspaceRoot,
} from "../lib/externalEditApi";

const { toastSuccess, toastError } = vi.hoisted(() => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
}));

vi.mock("sonner", () => ({
  toast: {
    success: toastSuccess,
    error: toastError,
  },
}));

// 只替换 IPC 调用，builtInEditorID 等常量沿用真实模块，避免测试自己伪造内置编辑器的 ID。
vi.mock("../lib/externalEditApi", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/externalEditApi")>()),
  getExternalEditSettings: vi.fn(),
  saveExternalEditSettings: vi.fn(),
  selectExternalEditorExecutable: vi.fn(),
  selectExternalEditWorkspaceRoot: vi.fn(),
}));

const builtInEditor = {
  id: "system-text",
  name: "System Text Editor",
  path: "/bin/editor",
  args: [],
  builtIn: true,
  available: true,
  default: false,
};

function makeSettings(overrides: Partial<Awaited<ReturnType<typeof getExternalEditSettings>>> = {}) {
  return {
    defaultEditorId: "system-text",
    workspaceRoot: "/tmp",
    cleanupRetentionDays: 7,
    maxReadFileSizeMB: 10,
    editors: [builtInEditor],
    customEditors: [],
    ...overrides,
  };
}

describe("ExternalEditSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(getExternalEditSettings).mockResolvedValue(makeSettings());
    vi.mocked(saveExternalEditSettings).mockImplementation(async (input) =>
      makeSettings({
        defaultEditorId: input.defaultEditorId,
        workspaceRoot: input.workspaceRoot,
        cleanupRetentionDays: input.cleanupRetentionDays,
        maxReadFileSizeMB: input.maxReadFileSizeMB,
        editors: [
          { ...builtInEditor, default: input.defaultEditorId === builtInEditor.id },
          ...(input.customEditors || []).map((editor) => ({
            id: editor.id,
            name: editor.name,
            path: editor.path,
            args: editor.args || [],
            builtIn: false,
            available: true,
            default: input.defaultEditorId === editor.id,
          })),
        ],
        customEditors: input.customEditors || [],
      })
    );
    vi.mocked(selectExternalEditorExecutable).mockResolvedValue("/bin/custom-editor");
    vi.mocked(selectExternalEditWorkspaceRoot).mockResolvedValue("/tmp");
  });

  it("offers the always-available built-in editor as a default choice and explains its explicit save", async () => {
    vi.mocked(getExternalEditSettings).mockResolvedValueOnce(
      makeSettings({
        editors: [
          {
            id: builtInEditorID,
            name: "Built-in Editor",
            path: "",
            args: [],
            builtIn: true,
            available: true,
            default: false,
          },
          builtInEditor,
        ],
      })
    );

    const user = userEvent.setup();
    render(<ExternalEditSection />);

    expect(await screen.findByText("externalEdit.settings.builtInEditorHint")).toBeInTheDocument();

    await user.click(screen.getByRole("combobox"));
    const option = await screen.findByRole("option", { name: "externalEdit.settings.builtInEditorName" });
    expect(option).not.toHaveAttribute("aria-disabled", "true");

    await user.click(option);
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => {
      expect(saveExternalEditSettings).toHaveBeenCalledWith(
        expect.objectContaining({ defaultEditorId: builtInEditorID })
      );
    });
  });

  it("uses an explicit dialog when editing one custom editor", async () => {
    vi.mocked(getExternalEditSettings).mockResolvedValueOnce(
      makeSettings({
        editors: [
          { ...builtInEditor, default: false },
          {
            id: "custom-1",
            name: "VS Code",
            path: "/bin/code",
            args: ["--wait"],
            builtIn: false,
            available: true,
            default: true,
          },
        ],
        customEditors: [{ id: "custom-1", name: "VS Code", path: "/bin/code", args: ["--wait"] }],
        defaultEditorId: "custom-1",
      })
    );

    const user = userEvent.setup();
    render(<ExternalEditSection />);

    expect(await screen.findByRole("button", { name: "action.edit" })).toBeInTheDocument();
    expect(screen.queryByDisplayValue("/bin/code")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "action.edit" }));

    const dialog = await screen.findByRole("dialog");
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue("VS Code")).toBeInTheDocument();
    expect(within(dialog).getByDisplayValue("/bin/code")).toBeInTheDocument();
  });

  it("adds a custom editor via a dedicated dialog and saves it", async () => {
    const user = userEvent.setup();
    render(<ExternalEditSection />);

    expect(await screen.findByText("externalEdit.settings.emptyCustomEditors")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "externalEdit.settings.addEditor" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("asset.name"), "Custom Vim");
    await user.type(within(dialog).getByLabelText("externalEdit.settings.editorPath"), "/bin/vim");
    await user.type(within(dialog).getByLabelText("externalEdit.settings.editorArgs"), "--clean");
    await user.click(within(dialog).getByRole("button", { name: "action.add" }));
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => {
      expect(saveExternalEditSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          cleanupRetentionDays: 7,
          maxReadFileSizeMB: 10,
          customEditors: [
            expect.objectContaining({
              name: "Custom Vim",
              path: "/bin/vim",
              args: ["--clean"],
            }),
          ],
        })
      );
    });
  });

  it("falls back to a built-in default editor after deleting the default custom editor", async () => {
    vi.mocked(getExternalEditSettings).mockResolvedValueOnce(
      makeSettings({
        editors: [
          { ...builtInEditor, default: false },
          {
            id: "custom-1",
            name: "VS Code",
            path: "/bin/code",
            args: ["--wait"],
            builtIn: false,
            available: true,
            default: true,
          },
        ],
        customEditors: [{ id: "custom-1", name: "VS Code", path: "/bin/code", args: ["--wait"] }],
        defaultEditorId: "custom-1",
      })
    );

    const user = userEvent.setup();
    render(<ExternalEditSection />);

    expect(await screen.findByRole("button", { name: "action.delete" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "action.delete" }));
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => {
      expect(saveExternalEditSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          defaultEditorId: "system-text",
          cleanupRetentionDays: 7,
          maxReadFileSizeMB: 10,
          customEditors: [],
        })
      );
    });
  });

  it("saves cleanup retention days with the settings snapshot", async () => {
    const user = userEvent.setup();
    render(<ExternalEditSection />);

    const retentionInput = await screen.findByLabelText("externalEdit.settings.cleanupRetentionDays");
    await user.clear(retentionInput);
    await user.type(retentionInput, "14");
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => {
      expect(saveExternalEditSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          cleanupRetentionDays: 14,
          maxReadFileSizeMB: 10,
        })
      );
    });
  });

  it("loads and saves max read file size in MB", async () => {
    vi.mocked(getExternalEditSettings).mockResolvedValueOnce(
      makeSettings({
        maxReadFileSizeMB: 32,
      })
    );

    const user = userEvent.setup();
    render(<ExternalEditSection />);

    const input = await screen.findByLabelText("externalEdit.settings.maxReadFileSizeMB");
    expect(input).toHaveValue(32);
    await user.clear(input);
    await user.type(input, "64");
    await user.click(screen.getByRole("button", { name: "action.save" }));

    await waitFor(() => {
      expect(saveExternalEditSettings).toHaveBeenCalledWith(
        expect.objectContaining({
          maxReadFileSizeMB: 64,
        })
      );
    });
  });
});

describe("built-in editor id", () => {
  it("matches the backend editor id", () => {
    // 后端按这个 ID 决定“不拉起外部进程、走显式保存”，前端按它决定去向与标签；
    // 两侧是独立来源，任一侧改名都会让内置编辑器在界面上退化成一条普通外部编辑器条目。
    const goSource = fs.readFileSync(
      path.resolve(process.cwd(), "../internal/service/external_edit_svc/types.go"),
      "utf8"
    );
    const goValue = /builtInEditorID\s*=\s*"([^"]+)"/.exec(goSource)?.[1];
    expect(goValue).toBe(builtInEditorID);
  });
});
