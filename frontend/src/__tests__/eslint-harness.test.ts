// eslint.config.js 中约定类规则的守护测试：通过真实配置跑 ESLint，
// 双向断言（违规被报 / 豁免路径与合规写法不被误报），防止规则被静默删除或缩权。
import path from "path";
import { beforeAll, describe, expect, it } from "vitest";
import { ESLint } from "eslint";

const frontendRoot = path.resolve(__dirname, "../..");
const eslint = new ESLint({ cwd: frontendRoot });

// ESLint 冷启动（加载配置与全部插件）很慢，全量并行跑时首个用例会超过默认的 5000ms。
// 在 beforeAll 里用显式的长超时预热一次，用例本身也给足余量。
const eslintTimeout = 60_000;
beforeAll(async () => {
  await eslint.lintText("export {};\n", { filePath: path.join(frontendRoot, "src/components/harness-fixture.tsx") });
}, eslintTimeout);

async function ruleIds(relativePath: string, code: string): Promise<(string | null)[]> {
  const [result] = await eslint.lintText(code, { filePath: path.join(frontendRoot, relativePath) });
  return result.messages.map((m) => m.ruleId);
}

describe("harness: 成功 toast 必须走 notify helper（AGENTS.md，#135）", { timeout: eslintTimeout }, () => {
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

describe("harness: 组件禁用 palette 类名，只用语义 token（DESIGN.md）", { timeout: eslintTimeout }, () => {
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
});

describe("harness: 共享前端代码不得按驱动名分支（驱动属性读驱动描述）", { timeout: eslintTimeout }, () => {
  const compareInComponent = `export const f = (driver: string) => driver === "postgresql";\n`;

  it("与驱动名字面量的比较被拦截（含 !==、字面量在左、别名）", async () => {
    for (const code of [
      compareInComponent,
      `export const f = (driver: string) => driver !== "mysql";\n`,
      `export const f = (driver: string) => "sqlite" == driver;\n`,
      `export const f = (driver: string) => driver === "sqlserver";\n`,
    ]) {
      const ids = await ruleIds("src/components/harness-fixture.tsx", code);
      expect(ids).toContain("no-restricted-syntax");
    }
  });

  it("switch 的 case 驱动名被拦截", async () => {
    const ids = await ruleIds(
      "src/lib/harness-fixture.ts",
      `export const f = (driver: string) => {\n  switch (driver) {\n    case "mssql":\n      return 1;\n  }\n  return 0;\n};\n`
    );
    expect(ids).toContain("no-restricted-syntax");
  });

  it("驱动描述实现目录 src/lib/dbDrivers/ 不被误报", async () => {
    const ids = await ruleIds("src/lib/dbDrivers/harness-fixture.ts", compareInComponent);
    expect(ids).not.toContain("no-restricted-syntax");
  });

  it("测试文件不被误报", async () => {
    const ids = await ruleIds("src/components/__tests__/harness-fixture.test.tsx", compareInComponent);
    expect(ids).not.toContain("no-restricted-syntax");
  });

  it("写路径豁免名单内的文件不被误报", async () => {
    const ids = await ruleIds("src/lib/tableSql.ts", compareInComponent);
    expect(ids).not.toContain("no-restricted-syntax");
  });

  it("豁免名单之外的同类文件仍被拦截（豁免按文件，不按目录）", async () => {
    const ids = await ruleIds("src/lib/tableSqlNew.ts", compareInComponent);
    expect(ids).toContain("no-restricted-syntax");
  });

  it("合规写法不被误报（读驱动描述、与驱动无关的字符串比较）", async () => {
    const ids = await ruleIds(
      "src/components/harness-fixture.tsx",
      `import { driverHasCapability } from "@/lib/dbDrivers";\nexport const f = (d: undefined, kind: string) => driverHasCapability(d, "schemas") || kind === "table";\n`
    );
    expect(ids).not.toContain("no-restricted-syntax");
  });
});
