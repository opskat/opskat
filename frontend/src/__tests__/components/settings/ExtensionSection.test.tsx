import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import {
  ListInstalledExtensions,
  GetExtensionDetail,
  GetExtensionSettings,
} from "../../../../wailsjs/go/extension/Extension";

function installed(name: string, credentials: string) {
  return {
    name,
    version: "1.0.0",
    icon: "",
    displayName: name,
    description: "",
    enabled: true,
    manifest: { name, version: "1.0.0", capabilities: { credentials } },
  };
}

// Opens the extension's detail dialog the way a user does: row menu → Details.
async function openDetail(ext: { name: string; [key: string]: unknown }) {
  vi.mocked(ListInstalledExtensions).mockResolvedValue([ext] as never);
  vi.mocked(GetExtensionDetail).mockResolvedValue(ext as never);
  render(<ExtensionSection />);
  await screen.findByText(ext.name);
  // The row menu trigger is the icon-only button; Radix opens it on pointerdown.
  const trigger = screen.getAllByRole("button").find((b) => b.textContent === "")!;
  fireEvent.pointerDown(trigger, { button: 0, ctrlKey: false });
  fireEvent.click(await screen.findByText("extension.detail"));
  await waitFor(() => expect(GetExtensionDetail).toHaveBeenCalledWith(ext.name));
  await screen.findByRole("dialog");
}

describe("ExtensionSection detail", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  it("warns that an extension with credentials:read reads stored passwords in plaintext", async () => {
    await openDetail(installed("es", "read"));
    expect(screen.getByRole("alert")).toHaveTextContent("extension.credentialsReadWarning");
  });

  it("shows no credentials warning for an extension without credentials:read", async () => {
    await openDetail(installed("notebook", ""));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("shows a review extension's settings and warns that it can let commands run", async () => {
    vi.mocked(GetExtensionSettings).mockResolvedValue({
      schema: { type: "object", properties: { token: { type: "string", format: "password", title: "API key" } } },
      values: {},
      secretsSet: [],
    } as never);
    const ext = installed("command-review", "");
    await openDetail({
      ...ext,
      manifest: {
        ...ext.manifest,
        capabilities: { commandReview: true },
        settingsSchema: { type: "object", properties: { token: { type: "string", format: "password" } } },
      },
    });
    expect(screen.getByRole("alert")).toHaveTextContent("extension.commandReviewWarning");
    expect(await screen.findByTestId("extension-settings-form")).toBeInTheDocument();
    expect(GetExtensionSettings).toHaveBeenCalledWith("command-review");
  });

  it("shows no settings section for an extension without settings", async () => {
    await openDetail(installed("notebook", ""));
    expect(screen.queryByTestId("extension-settings-form")).toBeNull();
    expect(GetExtensionSettings).not.toHaveBeenCalled();
  });

  // 设置 › AI › 命令审核里点"去扩展里设置"：切到扩展页并直接打开那个扩展的详情。
  it("opens the detail of the extension another settings page asked for", async () => {
    const ext = installed("command-review", "");
    vi.mocked(ListInstalledExtensions).mockResolvedValue([ext] as never);
    vi.mocked(GetExtensionDetail).mockResolvedValue(ext as never);
    useSettingsUiStore.getState().openExtensionDetail("command-review");
    render(<ExtensionSection />);
    await waitFor(() => expect(GetExtensionDetail).toHaveBeenCalledWith("command-review"));
    await screen.findByRole("dialog");
    expect(useSettingsUiStore.getState().extensionDetail).toBeNull();
  });

  // 纯审核扩展没有工具、策略组和页面：不显示三栏"无"，而是列出它审核哪几类危险。
  it("shows a review extension's questions instead of empty tools, policies and pages", async () => {
    const ext = installed("command-review", "");
    await openDetail({
      ...ext,
      manifest: {
        ...ext.manifest,
        capabilities: { commandReview: true },
        reviewer: {
          questions: [
            { id: "destructive", i18n: { name: { en: "may destroy data", "zh-CN": "可能破坏数据" } } },
            { id: "disruptive", i18n: { name: { en: "may interrupt services", "zh-CN": "可能中断服务" } } },
          ],
        },
      },
    });
    const questions = screen.getByTestId("extension-review-questions");
    expect(questions).toHaveTextContent("may destroy data");
    expect(questions).toHaveTextContent("may interrupt services");
    expect(screen.queryByText("extension.noTools")).toBeNull();
    expect(screen.queryByText("extension.noPolicies")).toBeNull();
    expect(screen.queryByText("extension.noPages")).toBeNull();
  });

  it("still lists the tools of an extension that has them", async () => {
    const ext = installed("es", "");
    await openDetail({
      ...ext,
      manifest: { ...ext.manifest, tools: [{ name: "search", i18n: { description: "Search an index" } }] },
    });
    expect(screen.getByText("extension.tools")).toBeInTheDocument();
    expect(screen.getByText("search")).toBeInTheDocument();
    expect(screen.queryByTestId("extension-review-questions")).toBeNull();
  });
});
