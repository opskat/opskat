import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider, useTheme } from "@/components/theme-provider";
import { SetWindowBackground } from "../../wailsjs/go/system/System";

// 窗口出现到网页首帧之间透出的是原生窗口底色。前端把主题解析出的页面背景色交给后端，
// 后端存下来供下次启动在网页绘制前就用上（见 internal/app/system/window_background.go）。
//
// jsdom 不渲染颜色：样式表给两套主题各一个 --background token，假 canvas 按填入的
// token 吐出像素，相当于 WebKit 把 oklch 栅格化成 sRGB 的那一步。
const tokenPixels: Record<string, [number, number, number]> = {
  "oklch(0.96 0.01 250)": [239, 242, 247],
  "oklch(0.18 0.025 250)": [5, 10, 30],
};

let style: HTMLStyleElement;

beforeEach(() => {
  style = document.createElement("style");
  style.textContent = ":root { --background: oklch(0.96 0.01 250); } .dark { --background: oklch(0.18 0.025 250); }";
  document.head.appendChild(style);
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockImplementation(function () {
    let fill = "";
    return {
      set fillStyle(v: string) {
        fill = v;
      },
      fillRect: () => {},
      getImageData: () => ({ data: new Uint8ClampedArray([...(tokenPixels[fill] ?? [0, 0, 0]), 255]) }),
    } as unknown as CanvasRenderingContext2D;
  } as unknown as typeof HTMLCanvasElement.prototype.getContext);
  vi.mocked(SetWindowBackground).mockResolvedValue();
});

afterEach(() => {
  cleanup();
  style.remove();
  localStorage.removeItem("theme");
  document.documentElement.classList.remove("dark", "light");
  vi.restoreAllMocks();
  vi.mocked(SetWindowBackground).mockReset();
});

function DarkButton() {
  const { setTheme } = useTheme();
  return <button onClick={() => setTheme("dark")}>dark</button>;
}

describe("window background sync", () => {
  it("把解析出的主题背景色以 #rrggbb 交给后端，切换主题时再交一次", () => {
    localStorage.setItem("theme", "light");
    render(
      <ThemeProvider>
        <DarkButton />
      </ThemeProvider>
    );
    expect(SetWindowBackground).toHaveBeenLastCalledWith("#eff2f7");

    fireEvent.click(screen.getByText("dark"));
    expect(SetWindowBackground).toHaveBeenLastCalledWith("#050a1e");
  });

  it("跟随系统时，系统外观变化也同步", () => {
    let dark = false;
    const listeners: Array<() => void> = [];
    vi.spyOn(window, "matchMedia").mockImplementation(
      (query: string) =>
        ({
          get matches() {
            return dark;
          },
          media: query,
          addEventListener: (_: string, cb: () => void) => listeners.push(cb),
          removeEventListener: () => {},
        }) as unknown as MediaQueryList
    );
    localStorage.setItem("theme", "system");
    render(<ThemeProvider>{null}</ThemeProvider>);
    expect(SetWindowBackground).toHaveBeenLastCalledWith("#eff2f7");

    dark = true;
    act(() => listeners.forEach((cb) => cb()));
    expect(SetWindowBackground).toHaveBeenLastCalledWith("#050a1e");
  });

  it("拿不到颜色时报错但不影响主题切换", () => {
    vi.mocked(HTMLCanvasElement.prototype.getContext).mockReturnValue(null);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
    localStorage.setItem("theme", "dark");

    render(<ThemeProvider>{null}</ThemeProvider>);

    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(SetWindowBackground).not.toHaveBeenCalled();
    expect(consoleError).toHaveBeenCalledWith("Sync window background failed:", expect.any(Error));
  });
});
