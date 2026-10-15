import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryErrorPanel } from "@/components/query/QueryErrorPanel";

const { toastError, notifyCopied } = vi.hoisted(() => ({ toastError: vi.fn(), notifyCopied: vi.fn() }));
vi.mock("sonner", () => ({ toast: { error: toastError } }));
vi.mock("@/lib/notify", () => ({ notifyCopied, notifySuccess: vi.fn() }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("QueryErrorPanel copy", () => {
  // 剪贴板写入失败(无权限 / 窗口未聚焦)时要告诉用户,而不是静默成一个未处理的 rejection。
  it("reports a clipboard failure instead of confirming the copy", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("clipboard denied"));
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    render(<QueryErrorPanel title="failed" error="boom" onRetry={vi.fn()} />);

    await userEvent.click(screen.getByRole("button", { name: "query.copyError" }));

    await waitFor(() => expect(toastError).toHaveBeenCalledWith("Error: clipboard denied"));
    expect(notifyCopied).not.toHaveBeenCalled();
  });
});
