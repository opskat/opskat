import { SetWindowBackground } from "../../wailsjs/go/system/System";

// 页面背景 token 栅格化后的 sRGB 色值（#rrggbb）。token 是 oklch，WebKit 的计算样式原样
// 返回 oklch 字符串，交给 canvas 画一个像素才拿得到实际颜色。读 :root 上的 token 而不是
// body 的计算背景色：body 带 200ms 过渡，切换主题时读到的是过渡中间色。
function currentBackgroundHex(): string {
  const token = getComputedStyle(document.documentElement).getPropertyValue("--background").trim();
  const canvas = document.createElement("canvas");
  canvas.width = 1;
  canvas.height = 1;
  const ctx = canvas.getContext("2d");
  if (!ctx) throw new Error("2d canvas context unavailable");
  ctx.fillStyle = token;
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
  return "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("");
}

/**
 * 把当前主题的页面背景色同步为原生窗口底色。后端立即应用并存进配置：下次启动时窗口出现到
 * 网页首帧之间透出的就是它，而不是 Wails 默认的白色。主题解析结果每次变化后调用。
 */
export function syncWindowBackground(): void {
  // 只是外观同步：失败要报出来，但不能让调用它的主题 effect 跟着抛错。
  const report = (err: unknown) => console.error("Sync window background failed:", err);
  try {
    SetWindowBackground(currentBackgroundHex()).catch(report);
  } catch (err) {
    report(err);
  }
}
