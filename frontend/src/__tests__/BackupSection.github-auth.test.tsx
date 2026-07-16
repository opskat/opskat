import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { BackupSection } from "@/components/settings/BackupSection";
import {
  ClearGitHubToken,
  GetGitHubToken,
  GetGitHubUser,
  GetStoredGitHubUser,
  GetWebDAVConfig,
  ListBackupGists,
} from "../../wailsjs/go/system/System";

describe("BackupSection GitHub auth restore", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetGitHubToken).mockResolvedValue("stored-token");
    vi.mocked(GetStoredGitHubUser).mockResolvedValue("stored-user");
    vi.mocked(GetWebDAVConfig).mockResolvedValue(undefined);
    vi.mocked(ListBackupGists).mockResolvedValue([]);
  });

  it("keeps the stored login when GitHub validation fails transiently", async () => {
    vi.mocked(GetGitHubUser).mockRejectedValue(new Error("GitHub API 错误: 503"));

    render(<BackupSection />);

    expect(await screen.findByText("backup.gistLoggedIn")).toBeInTheDocument();
    await waitFor(() => expect(GetGitHubUser).toHaveBeenCalledWith("stored-token"));
    expect(ClearGitHubToken).not.toHaveBeenCalled();
  });

  it("clears the stored login only when GitHub explicitly rejects the token", async () => {
    vi.mocked(GetGitHubUser).mockRejectedValue(new Error("[GITHUB_TOKEN_INVALID] GitHub API 错误: 401"));

    render(<BackupSection />);

    await waitFor(() => expect(ClearGitHubToken).toHaveBeenCalledOnce());
    expect(screen.getByText("backup.gistLogin")).toBeInTheDocument();
  });
});
