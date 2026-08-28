import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActiveTasksQuitDialog, type QuitActivity } from "@/components/ActiveTasksQuitDialog";

const activities: QuitActivity[] = [
  { kind: "ai", category: "running", title: "AI task", detail: "Conversation 7" },
  { kind: "terminal", category: "connection", title: "mac mini", detail: "SSH" },
  { kind: "rdp", category: "connection", title: "windows-02", detail: "RDP" },
  { kind: "vnc", category: "connection", title: "jump host", detail: "VNC" },
];

describe("ActiveTasksQuitDialog", () => {
  it("separates running work from connected GUI sessions and confirms the exact total", () => {
    const onConfirm = vi.fn();
    render(<ActiveTasksQuitDialog open activities={activities} onOpenChange={() => {}} onConfirm={onConfirm} />);

    expect(screen.getByText("AI task")).toBeInTheDocument();
    expect(screen.getByText("mac mini")).toBeInTheDocument();
    expect(screen.getByText("windows-02")).toBeInTheDocument();
    expect(screen.getByText("jump host")).toBeInTheDocument();
    expect(screen.getByText("appQuit.runningGroup")).toBeInTheDocument();
    expect(screen.getByText("appQuit.connectionGroup")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "appQuit.quitActivities" }));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  it("does not render the removed explanatory callout or idle-session preference", () => {
    render(<ActiveTasksQuitDialog open activities={activities} onOpenChange={() => {}} onConfirm={() => {}} />);

    expect(screen.queryByText(/AI commands will stop/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});
