import { beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { AutoBackupCard } from "../AutoBackupCard";
import { system } from "../../../../wailsjs/go/models";
import {
  GetAutoBackupSettings,
  GetAutoBackupStatus,
  RunAutoBackupNow,
  SaveAutoBackupSettings,
  SelectAutoBackupDirectory,
} from "../../../../wailsjs/go/system/System";

vi.mock("sonner", () => ({ toast: { error: vi.fn(), warning: vi.fn(), info: vi.fn(), success: vi.fn() } }));

const nowSec = Math.floor(Date.now() / 1000);

function settings(over: Partial<system.AutoBackupSettings> = {}) {
  return system.AutoBackupSettings.createFrom({
    enabled: true,
    passwordSet: true,
    intervalMinutes: 30,
    keep: 10,
    targets: ["webdav", "gist", "local"],
    localDir: "/Users/demo/Backups",
    ...over,
  });
}

function target(id: string, over: Partial<system.AutoBackupTargetStatus> = {}) {
  return system.AutoBackupTargetStatus.createFrom({
    id,
    available: true,
    phase: "",
    lastSuccessAt: 0,
    lastError: "",
    lastErrorAt: 0,
    retryAt: 0,
    ...over,
  });
}

function status(targets: system.AutoBackupTargetStatus[], over: Partial<system.AutoBackupStatus> = {}) {
  return system.AutoBackupStatus.createFrom({
    running: false,
    lastCheckAt: nowSec - 180,
    lastCheckResult: "unchanged",
    lastCheckError: "",
    nextCheckAt: nowSec + 1800,
    targets,
    webdavUrl: "https://dav.example.com/opskat/",
    githubUser: "octocat",
    ...over,
  });
}

async function renderCard() {
  await act(async () => {
    render(<AutoBackupCard />);
  });
}

describe("AutoBackupCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(GetAutoBackupStatus).mockResolvedValue(status([target("webdav"), target("gist"), target("local")]));
  });

  it("shows only the title and switch while disabled", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(settings({ enabled: false, passwordSet: false }));
    await renderCard();
    expect(screen.getByText("autoBackup.title")).toBeInTheDocument();
    expect(screen.getByRole("switch", { name: "autoBackup.enable" })).not.toBeChecked();
    expect(screen.queryByLabelText("autoBackup.password")).toBeNull();
    expect(screen.queryByText("autoBackup.save")).toBeNull();
  });

  it("shows the load failure with retry instead of an empty form", async () => {
    vi.mocked(GetAutoBackupSettings).mockRejectedValueOnce(new Error("cfg boom")).mockResolvedValueOnce(settings());
    await renderCard();
    expect(screen.getByRole("alert")).toHaveTextContent("cfg boom");
    await act(async () => {
      fireEvent.click(screen.getByTestId("async-state-retry"));
    });
    expect(screen.getByLabelText("autoBackup.password")).toBeInTheDocument();
  });

  it("validates inline and does not save without a password or a usable location", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(
      settings({ enabled: false, passwordSet: false, targets: [], localDir: "" })
    );
    vi.mocked(GetAutoBackupStatus).mockResolvedValue(
      status([
        target("webdav", { available: false }),
        target("gist", { available: false }),
        target("local", { available: false }),
      ])
    );
    await renderCard();
    await act(async () => {
      fireEvent.click(screen.getByRole("switch", { name: "autoBackup.enable" }));
    });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /autoBackup.save/ }));
    });
    expect(screen.getByText("autoBackup.passwordRequired")).toBeInTheDocument();
    expect(screen.getByText("autoBackup.targetsRequired")).toBeInTheDocument();
    expect(SaveAutoBackupSettings).not.toHaveBeenCalled();
  });

  it("disables unavailable locations and links to their setup card", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(settings());
    vi.mocked(GetAutoBackupStatus).mockResolvedValue(
      status([target("webdav", { available: false }), target("gist", { available: false }), target("local")])
    );
    await renderCard();
    const webdav = screen.getByTestId("auto-backup-target-webdav");
    expect(within(webdav).getByRole("checkbox")).toBeDisabled();
    expect(within(webdav).getByText("autoBackup.webdav.goConfigure")).toBeInTheDocument();
    const gist = screen.getByTestId("auto-backup-target-gist");
    expect(within(gist).getByText("autoBackup.gist.goConfigure")).toBeInTheDocument();
  });

  it("shows per-location status: success time, running, and failure with the raw error", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(settings());
    vi.mocked(GetAutoBackupStatus).mockResolvedValue(
      status([
        target("webdav", {
          lastError: "PUT opskat-backup-auto-x.encrypted.json: 507 Insufficient Storage",
          retryAt: nowSec + 120,
        }),
        target("gist", { phase: "running" }),
        target("local", { lastSuccessAt: nowSec - 720 }),
      ])
    );
    await renderCard();
    const webdav = screen.getByTestId("auto-backup-target-webdav");
    expect(within(webdav).getByText("autoBackup.status.failedRetry")).toBeInTheDocument();
    expect(within(webdav).getByText(/507 Insufficient Storage/)).toBeInTheDocument();
    expect(
      within(screen.getByTestId("auto-backup-target-gist")).getByText("autoBackup.status.running")
    ).toBeInTheDocument();
    expect(within(screen.getByTestId("auto-backup-target-local")).getByText(/12/)).toBeInTheDocument();
  });

  it("submits the form and blocks Back up now while there are unsaved changes", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(settings({ targets: ["webdav"], localDir: "" }));
    vi.mocked(SelectAutoBackupDirectory).mockResolvedValue("/Volumes/Sync/OpsKat");
    vi.mocked(SaveAutoBackupSettings).mockResolvedValue(undefined);
    await renderCard();

    const runNow = screen.getByRole("button", { name: /autoBackup.runNow/ });
    expect(runNow).not.toBeDisabled();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /autoBackup.local.pick/ }));
    });
    fireEvent.change(screen.getByLabelText("autoBackup.password"), { target: { value: "new-pw" } });
    expect(screen.getByRole("button", { name: /autoBackup.runNow/ })).toBeDisabled();
    expect(screen.getByText("autoBackup.unsavedHint")).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /autoBackup.save/ }));
    });
    expect(SaveAutoBackupSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        enabled: true,
        password: "new-pw",
        intervalMinutes: 30,
        keep: 10,
        targets: ["webdav", "local"],
        localDir: "/Volumes/Sync/OpsKat",
      })
    );
  });

  it("runs a backup now and refreshes the status", async () => {
    vi.mocked(GetAutoBackupSettings).mockResolvedValue(settings());
    vi.mocked(RunAutoBackupNow).mockResolvedValue(undefined);
    await renderCard();
    const before = vi.mocked(GetAutoBackupStatus).mock.calls.length;
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: /autoBackup.runNow/ }));
    });
    expect(RunAutoBackupNow).toHaveBeenCalledTimes(1);
    expect(vi.mocked(GetAutoBackupStatus).mock.calls.length).toBeGreaterThan(before);
  });
});
