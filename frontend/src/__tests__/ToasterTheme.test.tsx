import { render, cleanup } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { Toaster } from "@opskat/ui";
import { toast } from "sonner";

// 回归：Toaster 曾从 next-themes 的 useTheme 取主题，而应用根本没挂
// NextThemesProvider，于是永远拿到 "system"，sonner 便按 prefers-color-scheme
// 自行解析——用户在深色系统下显式选浅色（或相反）时，toast 跟错。
// 主题的唯一真相是 <html> 上的 .dark class（ThemeProvider 写入），断言以此为准。

// sonner 通过 portal 挂到 body 上，且要有 toast 才会渲染出 [data-sonner-toaster]
async function mountAndReadTheme(node: React.ReactElement): Promise<string | null> {
  render(node);
  toast("t");
  await new Promise((r) => setTimeout(r, 20));
  return document.querySelector("[data-sonner-toaster]")?.getAttribute("data-sonner-theme") ?? null;
}

afterEach(() => {
  cleanup();
  document.documentElement.classList.remove("dark", "light");
});

describe("Toaster theme", () => {
  it("跟随 <html>.dark 渲染为 dark", async () => {
    document.documentElement.classList.add("dark");
    expect(await mountAndReadTheme(<Toaster />)).toBe("dark");
  });

  it("没有 .dark 时渲染为 light，而不是交给系统偏好", async () => {
    document.documentElement.classList.remove("dark");
    expect(await mountAndReadTheme(<Toaster />)).toBe("light");
  });

  it("显式传入的 theme 优先于自动解析", async () => {
    document.documentElement.classList.add("dark");
    expect(await mountAndReadTheme(<Toaster theme="light" />)).toBe("light");
  });
});
