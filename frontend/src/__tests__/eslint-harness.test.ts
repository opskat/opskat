// eslint.config.js 中约定类规则的守护测试：通过真实配置跑 ESLint，
// 双向断言（违规被报 / 豁免路径与合规写法不被误报），防止规则被静默删除或缩权。
import path from "path";
import { describe, expect, it } from "vitest";
import { ESLint } from "eslint";

const frontendRoot = path.resolve(__dirname, "../..");
const eslint = new ESLint({ cwd: frontendRoot });

async function ruleIds(relativePath: string, code: string): Promise<(string | null)[]> {
  const [result] = await eslint.lintText(code, { filePath: path.join(frontendRoot, relativePath) });
  return result.messages.map((m) => m.ruleId);
}

describe("harness: 成功 toast 必须走 notify helper（AGENTS.md，#135）", () => {
  it("业务代码里的 toast.success 会被拦截", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `import { toast } from "sonner";\nexport const f = () => toast.success("done");\n`
    );
    expect(ids).toContain("no-restricted-properties");
  });

  it("可选链变体 toast?.success 同样被拦截", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `import { toast } from "sonner";\nexport const f = () => toast?.success("done");\n`
    );
    expect(ids).toContain("no-restricted-properties");
  });

  it("封装点 src/lib/notify.ts 被豁免", async () => {
    const ids = await ruleIds(
      "src/lib/notify.ts",
      `import { toast } from "sonner";\nexport const notifySuccess = (m: string) => toast.success(m);\n`
    );
    expect(ids).not.toContain("no-restricted-properties");
  });

  it("合规写法（notifySuccess / toast.error）不被误报", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `import { toast } from "sonner";\nimport { notifySuccess } from "@/lib/notify";\nexport const f = () => {\n  notifySuccess("saved");\n  toast.error("boom");\n};\n`
    );
    expect(ids).not.toContain("no-restricted-properties");
  });
});

describe("harness: 组件禁用 palette 类名，只用语义 token（DESIGN.md）", () => {
  it("字符串字面量里的 palette 类名被拦截（含透明度后缀）", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `export const C = () => <span className="bg-red-500/20 text-amber-500">x</span>;\n`
    );
    expect(ids).toContain("no-restricted-syntax");
  });

  it("模板字符串里的 palette 类名同样被拦截", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      "export const cls = (extra: string) => `text-emerald-600 ${extra}`;\n"
    );
    expect(ids).toContain("no-restricted-syntax");
  });

  it("语义 token 不被误报", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `export const C = () => <span className="bg-background text-warning text-muted-foreground border-border">x</span>;\n`
    );
    expect(ids).not.toContain("no-restricted-syntax");
  });

  it("devserver-ui 不在 token 体系内，palette 类名放行，但 React 19 禁令仍生效", async () => {
    const palette = await ruleIds(
      "packages/devserver-ui/src/panels/harness-fixture.tsx",
      `export const C = () => <span className="text-red-500">x</span>;\n`
    );
    expect(palette).not.toContain("no-restricted-syntax");

    const react19 = await ruleIds(
      "packages/devserver-ui/src/panels/harness-fixture.tsx",
      `import type { MutableRefObject } from "react";\nexport type R = MutableRefObject<number>;\n`
    );
    expect(react19).toContain("no-restricted-syntax");
  });
});
