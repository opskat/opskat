/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import path from "path";

// Windows 的 WebView2 Runtime 并非人人都跟得上 Evergreen 更新（#273 是 110、#226 是 100）。
// Tailwind v4 按 Chrome 111 生成样式：主题 token 是 `--background: oklch(...)`，自定义属性
// 解析期不校验，但 `background-color: var(--background)` 替换后计算值非法，整条属性退回
// unset —— 弹窗背景透明、边框只剩 currentColor，界面看上去"只有线条"。
// Chrome 99 是硬底（Tailwind v4 依赖 @layer，无法降级），取 100 作为基线。
// 注意 esbuild 的 build.cssTarget 不解决这个问题：它只降直接写在属性上的 oklch()，
// 自定义属性和 color-mix() 原样输出。只有 Lightning CSS 会为自定义属性生成
// 十六进制兜底 + @supports 双写，回归测试见 src/__tests__/cssBaseline.test.ts。
const MIN_CHROME = 100;

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  // dev: 预热首屏关键路径，避免 Vite 按需 transform 在窗口出现后串行排队
  server: {
    warmup: {
      clientFiles: [
        "./src/main.tsx",
        "./src/App.tsx",
        "./src/i18n/index.ts",
        "./src/components/layout/Sidebar.tsx",
        "./src/components/layout/AssetTree.tsx",
        "./src/components/layout/MainPanel.tsx",
        "./src/components/layout/TopBar.tsx",
        "./src/components/ai/SideAssistantPanel.tsx",
      ],
    },
  },
  // dev: 预声明重依赖，让 Vite server 启动时一次性 pre-bundle，
  // 避免首次浏览器请求触发"new dependencies optimized"导致整页重载
  optimizeDeps: {
    include: [
      "react",
      "react-dom",
      "react-dom/client",
      "react-i18next",
      "i18next",
      "sonner",
      "@iconify/react",
      "@floating-ui/dom",
      "@radix-ui/react-tooltip",
      "@radix-ui/react-dialog",
      "@radix-ui/react-popover",
      "@radix-ui/react-select",
      "@radix-ui/react-dropdown-menu",
      "@radix-ui/react-scroll-area",
      "tailwind-merge",
      "clsx",
      "zustand",
    ],
  },
  css: {
    // Vite 只在 css.transformer === "lightningcss" 时才自动推导 targets，
    // 这里只借用它做压缩，必须显式给，否则等于没设。
    lightningcss: {
      targets: { chrome: MIN_CHROME << 16 },
    },
  },
  build: {
    // 改用 terser：esbuild 的死代码消除存在一个 bug——把 xterm.js 里
    // `requestMode` 中 `let r; (IIFE)(r||={});` 错误地改成 `(IIFE)(void 0||(n={}))`，
    // `n` 未声明，导致运行时 `ReferenceError: Can't find variable: n`，
    // vim 等通过 DECRQM 查询终端能力的程序会让 xterm.js parser 崩溃，
    // 从而屏幕渲染停滞、键盘看上去"无响应"。
    minify: "terser",
    cssMinify: "lightningcss",
  },
  test: {
    environment: "happy-dom",
    setupFiles: ["./src/__tests__/setup.ts"],
  },
});
