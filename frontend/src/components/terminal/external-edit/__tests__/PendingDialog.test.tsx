import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { ExternalEditSession } from "@/lib/externalEditApi";
import { ExternalEditPendingDialog, type ExternalEditPendingItem } from "../PendingDialog";

function makeSession(partial: Partial<ExternalEditSession> & { id: string }): ExternalEditSession {
  return {
    assetId: 101,
    assetName: "asset-101",
    documentKey: "101:/srv/app/demo.txt",
    sessionId: "ssh-b",
    remotePath: "/srv/app/demo.txt",
    remoteRealPath: "/srv/app/demo.txt",
    localPath: "/tmp/demo.txt",
    workspaceRoot: "/tmp",
    workspaceDir: "/tmp/demo",
    editorId: "system-text",
    editorName: "System Text Editor",
    editorPath: "/opt/editor",
    originalSha256: "a",
    originalSize: 1,
    originalModTime: 1,
    originalEncoding: "utf-8",
    lastLocalSha256: "b",
    dirty: true,
    state: "dirty",
    hidden: false,
    expired: false,
    createdAt: 1,
    updatedAt: 10,
    lastLaunchedAt: 10,
    lastSyncedAt: 1,
    ...partial,
  };
}

const conflictSession = makeSession({ id: "conflict", state: "conflict" });
const recoverySession = makeSession({ id: "recovery", state: "dirty", resumeRequired: true });

const conflictItem: ExternalEditPendingItem = {
  id: "conflict-item",
  type: "conflict",
  decisionType: "conflict",
  session: conflictSession,
};
const recoveryItem: ExternalEditPendingItem = {
  id: "recovery-item",
  type: "pending",
  decisionType: "pending",
  sourceType: "recovery",
  session: recoverySession,
};

function renderDialog(pendingItems: ExternalEditPendingItem[]) {
  const handlers = {
    onMerge: vi.fn(),
    onAcceptRemote: vi.fn(),
    onOverwrite: vi.fn(),
    onContinueEdit: vi.fn(),
  };
  render(
    <ExternalEditPendingDialog
      open
      onOpenChange={vi.fn()}
      pendingItems={pendingItems}
      savingSessionId={null}
      mergePrepareErrors={{}}
      continueEditLabel="externalEdit.actions.continueEdit"
      onOpenErrorDetail={vi.fn()}
      {...handlers}
    />
  );
  return handlers;
}

describe("ExternalEditPendingDialog discard confirmations", () => {
  it("asks before overwriting the remote file and names the remote version as the discarded side", async () => {
    const user = userEvent.setup();
    const { onOverwrite } = renderDialog([conflictItem]);

    await user.click(screen.getByRole("button", { name: "externalEdit.actions.overwrite" }));

    expect(onOverwrite).not.toHaveBeenCalled();
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("externalEdit.builtIn.overwriteConfirmTitle")).toBeInTheDocument();
    expect(within(confirm).getByText("externalEdit.builtIn.overwriteConfirmDesc")).toBeInTheDocument();

    await user.click(within(confirm).getByRole("button", { name: "externalEdit.actions.overwrite" }));

    expect(onOverwrite).toHaveBeenCalledTimes(1);
    expect(onOverwrite).toHaveBeenCalledWith(conflictSession);
  });

  it("asks before accepting the remote version and names the local edits as the discarded side", async () => {
    const user = userEvent.setup();
    const { onAcceptRemote } = renderDialog([conflictItem]);

    await user.click(screen.getByRole("button", { name: "externalEdit.actions.acceptRemote" }));

    expect(onAcceptRemote).not.toHaveBeenCalled();
    const confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("externalEdit.pending.acceptRemoteConfirmTitle")).toBeInTheDocument();
    expect(within(confirm).getByText("externalEdit.pending.acceptRemoteConfirmDesc")).toBeInTheDocument();

    await user.click(within(confirm).getByRole("button", { name: "externalEdit.actions.acceptRemote" }));

    expect(onAcceptRemote).toHaveBeenCalledWith(conflictSession);
  });

  it("does nothing when the confirmation is cancelled", async () => {
    const user = userEvent.setup();
    const { onOverwrite, onAcceptRemote } = renderDialog([conflictItem]);

    await user.click(screen.getByRole("button", { name: "externalEdit.actions.overwrite" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "action.cancel" }));
    await user.click(screen.getByRole("button", { name: "externalEdit.actions.acceptRemote" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "action.cancel" }));

    expect(onOverwrite).not.toHaveBeenCalled();
    expect(onAcceptRemote).not.toHaveBeenCalled();
  });

  it("confirms the recovery-record overwrite and reread without claiming the remote changed", async () => {
    const user = userEvent.setup();
    const { onOverwrite, onAcceptRemote } = renderDialog([recoveryItem]);

    await user.click(screen.getByRole("button", { name: "externalEdit.actions.overwrite" }));
    let confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("externalEdit.pending.overwriteRecoveryDesc")).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "externalEdit.actions.overwrite" }));
    expect(onOverwrite).toHaveBeenCalledWith(recoverySession);

    await user.click(screen.getByRole("button", { name: "externalEdit.actions.reread" }));
    expect(onAcceptRemote).not.toHaveBeenCalled();
    confirm = await screen.findByRole("alertdialog");
    expect(within(confirm).getByText("externalEdit.pending.acceptRemoteConfirmDesc")).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "externalEdit.actions.reread" }));
    expect(onAcceptRemote).toHaveBeenCalledWith(recoverySession);
  });

  it("styles the overwrite trigger like the built-in editor: an outline button, the danger lives in the confirm", () => {
    renderDialog([conflictItem]);

    const overwrite = screen.getByRole("button", { name: "externalEdit.actions.overwrite" });
    const acceptRemote = screen.getByRole("button", { name: "externalEdit.actions.acceptRemote" });
    expect(overwrite.className).not.toContain("bg-destructive");
    expect(overwrite.className).toBe(acceptRemote.className);
  });
});
