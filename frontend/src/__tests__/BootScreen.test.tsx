import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DismissBootScreen } from "@/lib/bootScreen";

// 窗口以隐藏状态启动，index.html 里的加载界面进入 DOM 后由内联脚本通知后端显示窗口；
// React 首次提交后加载界面淡出并移除。
afterEach(() => {
  cleanup();
  document.getElementById("boot")?.remove();
  vi.useRealTimers();
});

describe("boot screen", () => {
  it("React 提交后先淡出加载界面，过渡结束再移除", () => {
    vi.useFakeTimers();
    const boot = document.createElement("div");
    boot.id = "boot";
    document.body.appendChild(boot);

    render(<DismissBootScreen />);

    expect(boot.classList.contains("boot-done")).toBe(true);
    expect(document.getElementById("boot")).not.toBeNull();
    vi.advanceTimersByTime(250);
    expect(document.getElementById("boot")).toBeNull();
  });

  it("index.html 通知后端用的方法确实存在于生成的绑定里", () => {
    const html = readFileSync(resolve(__dirname, "../../index.html"), "utf8");
    const calls = [...html.matchAll(/window\.go\.system\.System\.(\w+)\(/g)].map((m) => m[1]);
    expect(calls).toEqual(["BootScreenShown"]);
    const bindings = readFileSync(resolve(__dirname, "../../wailsjs/go/system/System.d.ts"), "utf8");
    for (const name of calls) {
      expect(bindings).toContain(`export function ${name}(`);
    }
  });
});
