import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { SystemStatusSection } from "../SystemStatusSection";
import { ExtensionSection } from "../ExtensionSection";
import { NetworkSection } from "../NetworkSection";
import { CommandReviewSection } from "../CommandReviewSection";
import { BackupSection } from "../BackupSection";
import { AISettingsSection } from "../AISettingsSection";
import { system } from "../../../../wailsjs/go/models";
import {
  DetectOpsctl,
  DetectSkills,
  GetAppVersion,
  GetCommandReviewSettings,
  GetDataDir,
  GetGitHubToken,
  GetGitHubUser,
  GetNetworkProxySettings,
  GetOpsctlInstallDir,
  GetStoredGitHubUser,
  GetSystemStatus,
  GetWebDAVConfig,
  ListBackupGists,
  ListWebDAVBackups,
} from "../../../../wailsjs/go/system/System";
import { GetCommandReviewState, ListInstalledExtensions } from "../../../../wailsjs/go/extension/Extension";
import { ListAIProviders } from "../../../../wailsjs/go/ai/AI";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));

async function renderAwait(ui: React.ReactElement) {
  await act(async () => {
    render(ui);
  });
}

describe("settings load states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("system status shows a spinner, never the all-good card, before the first result", async () => {
    vi.mocked(GetSystemStatus).mockReturnValue(new Promise(() => {}));
    await renderAwait(<SystemStatusSection />);
    expect(screen.getByTestId("async-state-loading")).toBeInTheDocument();
    expect(screen.queryByText("systemStatus.allGood")).toBeNull();
  });

  it("system status shows the failure with retry instead of all-good", async () => {
    vi.mocked(GetSystemStatus).mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce([]);
    await renderAwait(<SystemStatusSection />);
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    expect(screen.queryByText("systemStatus.allGood")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByText("systemStatus.allGood")).toBeInTheDocument();
  });

  it("installed extensions show the failure, not the empty state, and retry recovers", async () => {
    vi.mocked(ListInstalledExtensions).mockRejectedValueOnce(new Error("ext boom")).mockResolvedValueOnce([]);
    await renderAwait(<ExtensionSection />);
    expect(screen.getByRole("alert")).toHaveTextContent("ext boom");
    expect(screen.queryByText("extension.noExtensions")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByText("extension.noExtensions")).toBeInTheDocument();
  });

  it("network settings show the failure instead of a blank tab, and retry recovers", async () => {
    vi.mocked(GetNetworkProxySettings)
      .mockRejectedValueOnce(new Error("net boom"))
      .mockResolvedValueOnce(
        system.NetworkProxyView.createFrom({
          mode: "direct",
          type: "http",
          host: "",
          port: 0,
          noProxy: "",
          envVars: [],
        })
      );
    await renderAwait(<NetworkSection />);
    expect(screen.getByRole("alert")).toHaveTextContent("net boom");
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByText("network.proxy.title")).toBeInTheDocument();
  });

  it("command review settings show the failure instead of spinning forever, and retry recovers", async () => {
    vi.mocked(GetCommandReviewState)
      .mockRejectedValueOnce(new Error("cr boom"))
      .mockResolvedValueOnce({ reviewers: [], selected: "", active: "", problem: "" } as never);
    vi.mocked(GetCommandReviewSettings).mockResolvedValue({
      timeoutMs: 5000,
      lastFailReason: "",
      lastFailAt: 0,
    } as never);
    await renderAwait(<CommandReviewSection />);
    expect(screen.getByRole("alert")).toHaveTextContent("cr boom");
    expect(screen.queryByTestId("async-state-loading")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByText("commandReview.settings.title")).toBeInTheDocument();
  });

  describe("backup lists", () => {
    beforeEach(() => {
      vi.mocked(GetGitHubToken).mockResolvedValue("tok");
      vi.mocked(GetStoredGitHubUser).mockResolvedValue("u");
      vi.mocked(GetGitHubUser).mockResolvedValue({ login: "u" } as never);
      vi.mocked(GetWebDAVConfig).mockResolvedValue(new system.WebDAVStoredConfig());
      vi.mocked(ListBackupGists).mockResolvedValue([]);
      vi.mocked(ListWebDAVBackups).mockResolvedValue([]);
    });

    it("gist list failure is shown with retry", async () => {
      vi.mocked(ListBackupGists).mockRejectedValueOnce(new Error("gist boom")).mockResolvedValueOnce([]);
      await renderAwait(<BackupSection />);
      expect(screen.getByRole("alert")).toHaveTextContent("gist boom");
      await act(async () => {
        fireEvent.click(screen.getByTestId("async-state-retry"));
      });
      expect(screen.queryByRole("alert")).toBeNull();
      expect(ListBackupGists).toHaveBeenCalledTimes(2);
    });

    it("webdav list failure is shown with retry", async () => {
      vi.mocked(GetGitHubToken).mockResolvedValue("");
      vi.mocked(GetWebDAVConfig).mockResolvedValue(
        system.WebDAVStoredConfig.createFrom({ url: "https://dav", configured: true })
      );
      vi.mocked(ListWebDAVBackups).mockRejectedValueOnce(new Error("dav boom")).mockResolvedValueOnce([]);
      await renderAwait(<BackupSection />);
      expect(screen.getByRole("alert")).toHaveTextContent("dav boom");
      await act(async () => {
        fireEvent.click(screen.getByTestId("async-state-retry"));
      });
      expect(screen.queryByRole("alert")).toBeNull();
      expect(ListWebDAVBackups).toHaveBeenCalledTimes(2);
    });
  });

  it("opsctl status failure is shown instead of 'not installed', and retry recovers", async () => {
    vi.mocked(ListAIProviders).mockResolvedValue([]);
    vi.mocked(DetectSkills).mockResolvedValue(
      system.SkillInstallInfo.createFrom({
        universalPath: "",
        universalInstalled: false,
        universalAgents: [],
        standalone: [],
      })
    );
    vi.mocked(GetOpsctlInstallDir).mockResolvedValue("");
    vi.mocked(GetDataDir).mockResolvedValue("");
    vi.mocked(GetAppVersion).mockResolvedValue("dev");
    vi.mocked(DetectOpsctl)
      .mockRejectedValueOnce(new Error("detect boom"))
      .mockResolvedValueOnce({ installed: true, path: "/bin/opsctl", version: "v1", embedded: false });
    await renderAwait(<AISettingsSection />);
    expect(screen.getByRole("alert")).toHaveTextContent("detect boom");
    expect(screen.queryByText("integration.notInstalled")).toBeNull();
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByText("/bin/opsctl")).toBeInTheDocument();
  });
});
