import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupSection } from "@/components/settings/BackupSection";
import { backup_svc, system } from "../../../../wailsjs/go/models";
import {
  GetGitHubToken,
  GetGitHubUser,
  GetStoredGitHubUser,
  GetWebDAVConfig,
  ImportFromGist,
  ImportFromWebDAV,
  ListBackupGists,
  ListWebDAVBackups,
  PreviewGistBackup,
  PreviewWebDAVBackup,
} from "../../../../wailsjs/go/system/System";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn() } }));

const summary = new backup_svc.BackupSummary({
  version: "1.0",
  exported_at: "2026-11-01T00:00:00Z",
  encrypted: true,
  includes_credentials: true,
  asset_count: 3,
  group_count: 1,
  credential_count: 2,
  policy_group_count: 1,
  forward_count: 1,
});

const emptyResult = new backup_svc.ImportResult({});

describe("BackupSection remote restore", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetGitHubToken).mockResolvedValue("");
    vi.mocked(GetStoredGitHubUser).mockResolvedValue("");
    vi.mocked(GetWebDAVConfig).mockResolvedValue(new system.WebDAVStoredConfig());
    vi.mocked(ListBackupGists).mockResolvedValue([]);
    vi.mocked(ListWebDAVBackups).mockResolvedValue([]);
  });

  it("restores from WebDAV through the import dialog: wrong password stays on the password step, defaults are merge without credentials or policy groups", async () => {
    const user = userEvent.setup();
    const backupName = "opskat-backup-20261101.encrypted.json";
    vi.mocked(GetWebDAVConfig).mockResolvedValue(
      new system.WebDAVStoredConfig({ configured: true, url: "https://dav.example.com/", authType: "none" })
    );
    vi.mocked(ListWebDAVBackups).mockResolvedValue([new backup_svc.WebDAVBackupInfo({ name: backupName, size: 10 })]);
    vi.mocked(PreviewWebDAVBackup)
      .mockRejectedValueOnce(new Error("解密失败，密码错误或数据损坏"))
      .mockResolvedValueOnce(summary);
    vi.mocked(ImportFromWebDAV).mockResolvedValue(emptyResult);

    render(<BackupSection />);

    const pull = await screen.findByRole("button", { name: "backup.webdavPull" });
    await waitFor(() => expect(pull).toBeEnabled());
    await user.click(pull);

    const password = await screen.findByPlaceholderText("backup.password");
    await user.type(password, "wrong");
    await user.click(screen.getByRole("button", { name: "backup.decryptPreview" }));

    expect(await screen.findByText("解密失败，密码错误或数据损坏")).toBeInTheDocument();
    expect(PreviewWebDAVBackup).toHaveBeenCalledWith(backupName, "wrong");
    expect(screen.getByPlaceholderText("backup.password")).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: "backup.modeMerge" })).not.toBeInTheDocument();
    expect(ImportFromWebDAV).not.toHaveBeenCalled();

    await user.clear(screen.getByPlaceholderText("backup.password"));
    await user.type(screen.getByPlaceholderText("backup.password"), "right");
    await user.click(screen.getByRole("button", { name: "backup.decryptPreview" }));

    expect(await screen.findByRole("radio", { name: "backup.modeMerge" })).toBeChecked();
    expect(screen.getByRole("switch", { name: /backup\.importAssets/ })).toBeChecked();
    expect(screen.getByRole("switch", { name: /backup\.importCredentials/ })).not.toBeChecked();
    expect(screen.getByRole("switch", { name: /backup\.importPolicyGroups/ })).not.toBeChecked();
    expect(screen.queryByText("backup.replaceWarning")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "backup.import" }));

    await waitFor(() => expect(ImportFromWebDAV).toHaveBeenCalledOnce());
    const [name, pw, opts] = vi.mocked(ImportFromWebDAV).mock.calls[0];
    expect(name).toBe(backupName);
    expect(pw).toBe("right");
    expect(opts).toMatchObject({
      mode: "merge",
      import_assets: true,
      import_credentials: false,
      import_policy_groups: false,
    });
  });

  it("restores from Gist through the import dialog and marks replace as destructive", async () => {
    const user = userEvent.setup();
    vi.mocked(GetGitHubToken).mockResolvedValue("gh-token");
    vi.mocked(GetStoredGitHubUser).mockResolvedValue("octo");
    vi.mocked(GetGitHubUser).mockResolvedValue(new backup_svc.GitHubUser({ login: "octo" }));
    vi.mocked(ListBackupGists).mockResolvedValue([
      new backup_svc.GistInfo({ id: "gist-1", description: "OpsKat backup", updated_at: "" }),
    ]);
    vi.mocked(PreviewGistBackup).mockResolvedValue(summary);
    vi.mocked(ImportFromGist).mockResolvedValue(emptyResult);

    render(<BackupSection />);

    await screen.findByText("backup.gistLoggedIn");
    const gistSelect = screen.getAllByRole("combobox").find((el) => el.textContent?.includes("backup.gistSelect"));
    expect(gistSelect).toBeDefined();
    await user.click(gistSelect!);
    await user.click(await screen.findByRole("option", { name: "backup.gistUpdate" }));
    await user.click(screen.getByRole("button", { name: "backup.gistPull" }));

    await user.type(await screen.findByPlaceholderText("backup.password"), "secret");
    await user.click(screen.getByRole("button", { name: "backup.decryptPreview" }));

    expect(PreviewGistBackup).toHaveBeenCalledWith("gist-1", "secret", "gh-token");
    const replace = await screen.findByRole("radio", { name: "backup.modeReplace" });
    expect(screen.getByRole("button", { name: "backup.import" })).not.toHaveAttribute("data-variant", "destructive");

    await user.click(replace);
    expect(screen.getByText("backup.replaceWarning")).toBeInTheDocument();
    const importButton = screen.getByRole("button", { name: "backup.import" });
    expect(importButton).toHaveAttribute("data-variant", "destructive");

    await user.click(importButton);
    await waitFor(() => expect(ImportFromGist).toHaveBeenCalledOnce());
    const [gistId, pw, token, opts] = vi.mocked(ImportFromGist).mock.calls[0];
    expect([gistId, pw, token]).toEqual(["gist-1", "secret", "gh-token"]);
    expect(opts).toMatchObject({ mode: "replace", import_credentials: false, import_policy_groups: false });
  });
});
