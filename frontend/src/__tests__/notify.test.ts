import { describe, it, expect, vi, beforeEach } from "vitest";
import { toast } from "sonner";
import { notifyCopied, notifySuccess } from "@/lib/notify";

vi.mock("sonner", () => ({
  toast: { success: vi.fn() },
}));

describe("notify helpers", () => {
  beforeEach(() => vi.clearAllMocks());

  it("notifyCopied 顶部居中且 1s 一闪而过", () => {
    notifyCopied("已复制");
    expect(toast.success).toHaveBeenCalledWith("已复制", { position: "top-center", duration: 1000 });
  });

  it("notifyCopied 支持传 id 覆盖前一条复制提示", () => {
    notifyCopied("已复制", { id: "terminal-copied" });
    expect(toast.success).toHaveBeenCalledWith("已复制", {
      position: "top-center",
      duration: 1000,
      id: "terminal-copied",
    });
  });

  it("notifySuccess 顶部居中，沿用默认停留时长", () => {
    notifySuccess("已保存");
    expect(toast.success).toHaveBeenCalledWith("已保存", { position: "top-center" });
  });

  it("notifySuccess 带 action 时把可点击按钮交给 toast，位置不变", () => {
    const onClick = vi.fn();
    notifySuccess("已创建", { action: { label: "打开", onClick } });
    expect(toast.success).toHaveBeenCalledWith("已创建", {
      position: "top-center",
      action: { label: "打开", onClick },
    });
  });
});
