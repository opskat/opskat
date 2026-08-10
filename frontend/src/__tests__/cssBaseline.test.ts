import { describe, expect, it } from "vitest";
import { build } from "vite";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import config from "../../vite.config";

/**
 * WebView2 Runtime 不是所有 Windows 机器都跟得上 Evergreen 更新（#273 报告的是 110）。
 * Tailwind v4 的基线是 Chrome 111，主题 token 写成 `--background: oklch(...)`：
 * 自定义属性解析期不校验，但 `background-color: var(--background)` 替换后计算值非法，
 * 属性退回 unset —— 弹窗背景变透明、边框只剩 currentColor 的线条。
 *
 * 这里用生产配置真实构建一小段 CSS，断言旧引擎能拿到可用颜色。
 */

/** 旧引擎会整条丢弃的颜色函数；只有包在 @supports 里才允许出现。 */
const MODERN_COLOR_FN = /(?:oklch|oklab|lch|color-mix)\(/g;

/** 去掉每个 @supports 块（含前置条件与花括号内容），只留下无条件生效的声明。 */
function stripSupportsBlocks(css: string): string {
  let out = "";
  let i = 0;
  for (;;) {
    const start = css.indexOf("@supports", i);
    if (start === -1) return out + css.slice(i);
    out += css.slice(i, start);
    let j = css.indexOf("{", start);
    if (j === -1) return out;
    let depth = 1;
    for (j += 1; j < css.length && depth > 0; j += 1) {
      if (css[j] === "{") depth += 1;
      else if (css[j] === "}") depth -= 1;
    }
    i = j;
  }
}

async function buildCss(source: string): Promise<string> {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "opskat-cssbaseline-"));
  const entry = path.join(dir, "entry.css");
  fs.writeFileSync(entry, source);
  try {
    const result = await build({
      configFile: false,
      logLevel: "silent",
      css: config.css,
      build: {
        cssMinify: config.build?.cssMinify,
        write: false,
        rollupOptions: { input: entry },
      },
    });
    const { output } = (Array.isArray(result) ? result[0] : result) as {
      output: Array<{ type: string; fileName: string; source?: string | Uint8Array }>;
    };
    const css = output.find((o) => o.type === "asset" && o.fileName.endsWith(".css"))?.source;
    if (typeof css !== "string") throw new Error("构建产物中没有 CSS asset");
    return css;
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

describe("CSS 旧引擎基线", () => {
  it("把主题 token 里的 oklch 降级成无条件生效的颜色", async () => {
    const css = await buildCss(
      `:root { --background: oklch(0.96 0.01 250); }\n.panel { background-color: var(--background); }`
    );

    // 广色域值仍可保留，但必须让位给一个旧引擎认得的兜底值。
    expect(stripSupportsBlocks(css)).toMatch(/--background:\s*#[0-9a-f]{3,8}/i);
  }, 60_000);

  it("不留下任何无 @supports 兜底的现代颜色函数", async () => {
    const css = await buildCss(
      [
        `:root { --accent: oklch(0.55 0.22 260); }`,
        `.solid { color: oklch(0.7 0.1 200); }`,
        `.blend { background-color: color-mix(in oklab, oklch(0.8 0.1 150) 50%, transparent); }`,
        `.token { box-shadow: inset 0 0 0 2px var(--accent); }`,
      ].join("\n")
    );

    expect(stripSupportsBlocks(css).match(MODERN_COLOR_FN)).toBeNull();
  }, 60_000);
});
