import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { describe, expect, it } from "vitest";

function readSource(pathFromSrc: string): string {
  return readFileSync(join(process.cwd(), "src", pathFromSrc), "utf8");
}

function expectNoStaticImport(source: string, modulePath: string): void {
  const escaped = modulePath.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  expect(source).not.toMatch(new RegExp(`^import\\s+(?:type\\s+)?[\\s\\S]*?from\\s+["']${escaped}["'];?`, "m"));
  expect(source).not.toMatch(new RegExp(`^import\\s+["']${escaped}["'];?`, "m"));
}

describe("bundle boundaries", () => {
  it("keeps Monaco setup out of the startup entry", () => {
    const source = readSource("main.tsx");

    expectNoStaticImport(source, "./lib/monaco-setup");
  });

  it("keeps terminal renderer code out of the terminal store startup path", () => {
    const source = readSource("stores/terminalStore.ts");

    expectNoStaticImport(source, "@/components/terminal/terminalRegistry");
  });

  it("loads heavy tab surfaces through dynamic chunks", () => {
    const source = readSource("components/layout/MainPanel.tsx");

    for (const modulePath of [
      "@/components/asset/AssetDetail",
      "@/components/asset/GroupDetail",
      "@/components/terminal/SplitPane",
      "@/components/terminal/SessionToolbar",
      "@/components/terminal/TerminalToolbar",
      "@/components/terminal/FileManagerPanel",
      "@/components/terminal/editor/RemoteFileEditorTab",
      "@/components/settings/SettingsPage",
      "@/components/settings/CredentialManager",
      "@/components/audit/AuditLogPage",
      "@/components/forward/PortForwardPage",
      "@/components/snippet/SnippetsPage",
      "@/components/ai/AIChatContent",
      "@/components/query/DatabasePanel",
      "@/components/query/RedisPanel",
      "@/components/query/MongoDBPanel",
    ]) {
      expectNoStaticImport(source, modulePath);
    }
  });

  it("keeps AI chat content out of the side panel shell", () => {
    const source = readSource("components/ai/SideAssistantPanel.tsx");

    expectNoStaticImport(source, "./AIChatContent");
  });
});

const SRC_ROOT = join(process.cwd(), "src");
const RESOLVE_SUFFIXES = ["", ".ts", ".tsx", ".js", "/index.ts", "/index.tsx", "/index.js"];

/** 只认会进运行时依赖图的静态 import / re-export：`import type`、全是 `type` 说明符的导入与 `import()` 都不算。 */
function staticImportSpecifiers(source: string): string[] {
  const specifiers: string[] = [];
  const fromClause = /^(import|export)\s+(?!type\s)([\s\S]*?)\s+from\s+["']([^"']+)["']/gm;
  for (const [, keyword, clause, specifier] of source.matchAll(fromClause)) {
    if (keyword === "export" && !/^(\*|\{)/.test(clause)) continue;
    const named = clause.match(/^\{([\s\S]*)\}$/);
    if (named) {
      const names = named[1]
        .split(",")
        .map((n) => n.trim())
        .filter(Boolean);
      if (names.every((n) => n.startsWith("type "))) continue;
    }
    specifiers.push(specifier);
  }
  for (const [, specifier] of source.matchAll(/^import\s+["']([^"']+)["']/gm)) specifiers.push(specifier);
  return specifiers;
}

function resolveLocal(fromFile: string, specifier: string): string | null {
  let base: string;
  if (specifier.startsWith("@/")) base = join(SRC_ROOT, specifier.slice(2));
  else if (specifier.startsWith(".")) base = join(dirname(fromFile), specifier);
  else return null;
  for (const suffix of RESOLVE_SUFFIXES) {
    const candidate = base + suffix;
    if (existsSync(candidate) && statSync(candidate).isFile()) return candidate;
  }
  throw new Error(`unresolved import ${specifier} in ${fromFile}`);
}

/** 从入口出发沿静态 import 走完整张图：返回可达的本地文件（相对 src）与裸模块名。 */
function staticImportGraph(entryFromSrc: string): { files: Set<string>; packages: Set<string> } {
  const files = new Set<string>();
  const packages = new Set<string>();
  const queue = [join(SRC_ROOT, entryFromSrc)];
  while (queue.length) {
    const file = queue.pop()!;
    const rel = relative(SRC_ROOT, file);
    if (files.has(rel)) continue;
    files.add(rel);
    if (!/\.(ts|tsx)$/.test(file)) continue;
    for (const specifier of staticImportSpecifiers(readFileSync(file, "utf8"))) {
      const resolved = resolveLocal(file, specifier);
      if (resolved) queue.push(resolved);
      else packages.add(specifier);
    }
  }
  return { files, packages };
}

const BUILTIN_TYPE_COMPONENT =
  /^components\/asset\/(?!Extension)\w+ConfigSection\.tsx$|^components\/asset\/detail\/(?!Extension)\w+DetailInfoCard\.tsx$/;

describe("startup bundle boundaries", () => {
  const startup = staticImportGraph("main.tsx");

  it("reaches the asset-type registry metadata eagerly", () => {
    // 守卫自身的前提：入口确实静态走到了注册表，否则下面几条断言都是空转。
    expect(startup.files).toContain("lib/assetTypes/index.ts");
    expect(startup.files).toContain("lib/assetTypes/vnc.ts");
  });

  it("keeps locale bundles out of the startup graph", () => {
    expect([...startup.files].filter((f) => f.startsWith("i18n/locales/"))).toEqual([]);
  });

  it("keeps built-in ConfigSection / DetailInfoCard components out of the startup graph", () => {
    expect([...startup.files].filter((f) => BUILTIN_TYPE_COMPONENT.test(f))).toEqual([]);
  });

  it("keeps noVNC out of the startup graph and out of the VNC form section", () => {
    expect(startup.packages).not.toContain("@novnc/novnc");
    expect(staticImportGraph("components/asset/VNCConfigSection.tsx").packages).not.toContain("@novnc/novnc");
  });
});
