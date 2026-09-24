import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActiveTasksQuitDialog, type QuitActivity } from "@/components/ActiveTasksQuitDialog";

// 全局 setup 的 t 只回 key，丢掉插值参数，测不到"按钮统计的是哪些活动"。
// 这里覆盖成把参数拼进 key 的版本；t 仍须是稳定引用（见 setup.ts 的说明）。
vi.mock("react-i18next", () => {
  const t = (key: string, params?: Record<string, unknown>) => (params ? `${key}(${JSON.stringify(params)})` : key);
  const i18n = { language: "en", changeLanguage: vi.fn() };
  return {
    useTranslation: () => ({ t, i18n }),
    initReactI18next: { type: "3rdParty", init: vi.fn() },
  };
});

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

    fireEvent.click(screen.getByTestId("confirm-force-quit"));
    expect(onConfirm).toHaveBeenCalledOnce();
  });

  // 空闲连接断开不丢任何东西，不该被算进"退出并中断 N 项"里 —— 只有运行中的
  // 任务才是真正被中断的东西。
  it("counts only interruptible running work on the confirm button", () => {
    render(<ActiveTasksQuitDialog open activities={activities} onOpenChange={() => {}} onConfirm={() => {}} />);

    expect(screen.getByTestId("confirm-force-quit")).toHaveTextContent(
      `appQuit.quitActivities(${JSON.stringify({ count: 1 })})`
    );
  });

  it("does not render the removed explanatory callout or idle-session preference", () => {
    render(<ActiveTasksQuitDialog open activities={activities} onOpenChange={() => {}} onConfirm={() => {}} />);

    expect(screen.queryByText(/AI commands will stop/i)).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });
});
