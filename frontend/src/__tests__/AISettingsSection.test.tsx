import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AISettingsSection } from "@/components/settings/AISettingsSection";
import { DetectOpsctl } from "../../wailsjs/go/system/System";
import {
  DetectSkills,
  GetAppVersion,
  GetDataDir,
  GetOpsctlInstallDir,
  GetSkillPreview,
  UninstallSkill,
} from "../../wailsjs/go/system/System";
import { ListAIProviders } from "../../wailsjs/go/ai/AI";
import { system } from "../../wailsjs/go/models";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";

describe("AISettingsSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(ListAIProviders).mockResolvedValue([]);
    vi.mocked(DetectOpsctl).mockResolvedValue({
      installed: false,
      path: "",
      version: "",
      embedded: false,
    });
    vi.mocked(DetectSkills).mockResolvedValue(
      system.SkillInstallInfo.createFrom({
        universalPath: "C:/Users/test/.agents/skills/opsctl",
        universalInstalled: false,
        universalAgents: [],
        standalone: [],
      })
    );
    vi.mocked(GetOpsctlInstallDir).mockResolvedValue("");
    vi.mocked(GetDataDir).mockResolvedValue("");
    vi.mocked(GetAppVersion).mockResolvedValue("dev");
    vi.mocked(GetSkillPreview).mockResolvedValue("name: opsctl\ndescription: diagnostics");
  });

  it("opens the GitHub Releases page for manual opsctl CLI install", async () => {
    render(<AISettingsSection />);

    await userEvent.click(screen.getByRole("button", { name: /GitHub Releases/i }));

    expect(BrowserOpenURL).toHaveBeenCalledWith("https://github.com/opskat/opskat/releases");
  });

  it("allows users to select the rendered CLI paths and skill preview", async () => {
    vi.mocked(DetectOpsctl).mockResolvedValue({
      installed: true,
      path: "/usr/local/bin/opsctl",
      version: "v1.2.3",
      embedded: false,
    });
    vi.mocked(GetDataDir).mockResolvedValue("/var/lib/opskat");
    render(<AISettingsSection />);

    expect(await screen.findByText("/usr/local/bin/opsctl")).toHaveClass("select-text");
    expect(await screen.findByText("/var/lib/opskat")).toHaveClass("select-text");
    await userEvent.click(screen.getByRole("button", { name: "integration.skillPreview" }));
    expect(await screen.findByText(/description: diagnostics/)).toHaveClass("select-text");
  });

  it("marks the skill card installed when only a standalone target is installed", async () => {
    vi.mocked(DetectSkills).mockResolvedValue(
      system.SkillInstallInfo.createFrom({
        universalPath: "C:/Users/test/.agents/skills/opsctl",
        universalInstalled: false,
        universalAgents: [],
        standalone: [
          {
            key: "claude-code",
            name: "Claude Code",
            installed: true,
            path: "C:/Users/test/.claude/plugins/marketplaces/opskat/opsctl",
          },
        ],
      })
    );

    render(<AISettingsSection />);

    expect(await screen.findByText("integration.skillInstalled")).toBeTruthy();
  });

  it("uninstalls a single standalone AI plugin target", async () => {
    const base = {
      universalPath: "C:/Users/test/.agents/skills/opsctl",
      universalInstalled: false,
      universalAgents: [] as system.SkillAgent[],
      standalone: [] as system.SkillTarget[],
    };
    vi.mocked(DetectSkills)
      .mockResolvedValueOnce(
        system.SkillInstallInfo.createFrom({
          ...base,
          standalone: [
            {
              key: "claude-code",
              name: "Claude Code",
              installed: true,
              path: "C:/Users/test/.claude/plugins/marketplaces/opskat/opsctl",
            },
          ],
        })
      )
      .mockResolvedValueOnce(system.SkillInstallInfo.createFrom({ ...base, standalone: [] }));

    render(<AISettingsSection />);

    expect(await screen.findByText("Claude Code")).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: "integration.skillUninstall" }));
    await userEvent.click(screen.getAllByRole("button", { name: "integration.skillUninstall" }).at(-1)!);

    await waitFor(() => expect(UninstallSkill).toHaveBeenCalledWith("claude-code"));
    await waitFor(() => expect(DetectSkills).toHaveBeenCalledTimes(2));
  });
});
