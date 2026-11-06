import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, fireEvent, cleanup, waitFor } from "@testing-library/react";
import { ExtensionSection } from "../../../components/settings/ExtensionSection";
import { useSettingsUiStore } from "@/stores/settingsUiStore";
import {
  ListInstalledExtensions,
  GetExtensionDetail,
  GetCommandReviewState,
} from "../../../../wailsjs/go/extension/Extension";

function installed(name: string, credentials: string) {
  return {
    name,
    version: "1.0.0",
    icon: "",
    displayName: name,
    description: "",
    enabled: true,
    manifest: { name, version: "1.0.0", permissions: { credentials } },
  };
}

// A pure command reviewer: its only tool is the host-only review tool.
function reviewer(name: string) {
  const ext = installed(name, "");
  return {
    ...ext,
    manifest: {
      ...ext.manifest,
      contributes: { commandReviewer: true },
      tools: [{ name: "review_command", hostOnly: true }],
    },
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

  it("warns that a command reviewer can let commands run", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue({ reviewers: [], selected: "", active: "" } as never);
    await openDetail(reviewer("jev-command-review"));
    expect(screen.getByRole("alert")).toHaveTextContent("extension.commandReviewWarning");
  });

  // 纯审核扩展没有（对外的）工具、策略组和页面：不显示三栏"无"，而是列出它审核哪几类危险。
  it("shows a reviewer's questions instead of empty tools, policies and pages", async () => {
    vi.mocked(GetCommandReviewState).mockResolvedValue({
      reviewers: [
        {
          name: "jev-command-review",
          questions: [
            { id: "destructive", names: { en: "may destroy data", "zh-CN": "可能破坏数据" } },
            { id: "disruptive", names: { en: "may interrupt services", "zh-CN": "可能中断服务" } },
          ],
        },
      ],
      selected: "",
      active: "jev-command-review",
    } as never);
    await openDetail(reviewer("jev-command-review"));
    const questions = await screen.findByTestId("extension-review-questions");
    expect(questions).toHaveTextContent("may destroy data");
    expect(questions).toHaveTextContent("may interrupt services");
    expect(screen.queryByText("review_command")).toBeNull();
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
    expect(GetCommandReviewState).not.toHaveBeenCalled();
  });

  // 设置 › AI › 命令审核里点"在扩展详情里设置"：切到扩展页并直接打开那个扩展的详情。
  it("opens the detail of the extension another settings page asked for", async () => {
    const ext = installed("jev-command-review", "");
    vi.mocked(ListInstalledExtensions).mockResolvedValue([ext] as never);
    vi.mocked(GetExtensionDetail).mockResolvedValue(ext as never);
    useSettingsUiStore.getState().openExtensionDetail("jev-command-review");
    render(<ExtensionSection />);
    await waitFor(() => expect(GetExtensionDetail).toHaveBeenCalledWith("jev-command-review"));
    await screen.findByRole("dialog");
    expect(useSettingsUiStore.getState().extensionDetail).toBeNull();
  });
});

describe("ExtensionSection refused extensions", () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);

  const refused = (name: string, kind: string, message: string) => ({
    name,
    version: "1.4.0",
    icon: "",
    displayName: name,
    description: "",
    enabled: false,
    loadError: { kind, message },
  });

  it("shows an incompatible extension with its reason and no enable switch", async () => {
    vi.mocked(ListInstalledExtensions).mockResolvedValue([
      refused("legacy", "incompatible", "declares hostABI 2.3, supports only 1.0"),
    ] as never);
    render(<ExtensionSection />);

    expect(await screen.findByText("legacy")).toBeInTheDocument();
    expect(screen.getByText("extension.incompatible")).toBeInTheDocument();
    expect(screen.getByText("declares hostABI 2.3, supports only 1.0")).toBeInTheDocument();
    expect(screen.queryByTestId("extension-toggle-legacy")).not.toBeInTheDocument();
  });

  it("shows a load failure with the parse error", async () => {
    vi.mocked(ListInstalledExtensions).mockResolvedValue([
      refused("old", "invalid", 'unknown field "capabilities"'),
    ] as never);
    render(<ExtensionSection />);

    expect(await screen.findByText("extension.loadFailed")).toBeInTheDocument();
    expect(screen.getByText('unknown field "capabilities"')).toBeInTheDocument();
    expect(screen.queryByTestId("extension-toggle-old")).not.toBeInTheDocument();
  });
});
