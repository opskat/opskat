# AssetForm 组件注册化 — 阶段 4a(契约 + 通用壳 + 迁移 local)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 `AssetFormHandle`/`ConfigSectionProps` ref 契约与 AssetForm 的"通用路径",并把最简单的 `local` 类型迁移过去 —— 证明 seam,后续 8 类型照此一类型一 commit。

**Architecture:** `AssetTypeDefinition` 增 `ConfigSection?`(forwardRef 组件)+ `testable?`。每个迁移后的 ConfigSection 自持 state、经 `editAsset` prop 回填、`useImperativeHandle` 暴露 `buildConfig`/`buildTestConfig`、`onValidityChange` 上报启用态。AssetForm 加 `def?.ConfigSection ? 通用路径 : 遗留 switch` 双路径(双路径只活在 #144 分支中间 commit,末 commit 删除)。local 的保存序列化/编辑回填先抽成纯函数 `buildLocalConfig`/`parseLocalConfig` 并 golden 锁定(行为保持),再搬进自持 state 的 section。

**Tech Stack:** React 19(forwardRef / useImperativeHandle)+ TypeScript,Vitest + @testing-library/react,Wails 生成 binding。

**Spec:** `docs/superpowers/specs/2026-06-05-assetform-registration-phase4-design.md`。Issue #130,累加到 #144。

---

## 现状锚点(AssetForm.tsx,实现前必读)

- 导入区:1–50;`getAssetType` **未**导入(需加 `import { getAssetType } from "@/lib/assetTypes"`)。`EncryptPassword` 已导入(27)。
- `const { createAsset, updateAsset } = useAssetStore();` 在 319。
- local 现状:state `localShell/localArgs/localCwd`(440–443);`loadLocalConfig`(917–926)+ `resetLocalFields`(928–932);编辑分发分支(503–504);创建态 reset 调用 `resetLocalFields()`(532);`handleTypeChange` 中 `if (newType === "local") setHost("")`(949);保存分支(1586–1598);渲染块(2110–2119,6 个受控 props)。
- 共享持久化尾:1618–1654(建 `asset` + `createAsset`/`updateAsset` + toast)。
- 共享校验:`isTestableAssetType`(1658–1665,**不含 local**)、`saveDisabledReason`(1689–1709,local 仅命中 `!name.trim()` 一支)、`saveDisabled`(1710)。
- `parseLocalShellArgs`(可抛 `unclosed quote`/`unfinished escape`)、`formatLocalShellArgs` 来自 `@/lib/localShellArgs`。

**不变量**:local 迁移后 `buildConfig` 产出的 config JSON 必须与旧 `handleSubmit` local 分支(1586–1598)字节一致;编辑回填与旧 `loadLocalConfig`(917–926)一致。local 不可测(`buildTestConfig` 为 null)、无 ssh 隧道(`sshTunnelId` 恒 0)。图标默认仍由壳经 `DEFAULT_ICONS[newType]`(map 查表,非 switch)处理。

---

## File Structure

- **Create** `frontend/src/lib/assetTypes/formContract.ts` — ref 契约类型(`AssetFormContext`/`AssetConfigBuildResult`/`AssetTestConfig`/`AssetFormHandle`/`ConfigSectionProps`/`ConfigSectionComponent`)。
- **Modify** `frontend/src/lib/assetTypes/types.ts` — `AssetTypeDefinition` 增 `ConfigSection?`/`testable?`。
- **Modify** `frontend/src/components/asset/LocalConfigSection.tsx` — 加 `buildLocalConfig`/`parseLocalConfig`/`LOCAL_DEFAULTS` 纯函数;组件重写为 forwardRef 自持 state。
- **Modify** `frontend/src/lib/assetTypes/local.ts` — 注册 `ConfigSection: LocalConfigSection`。
- **Modify** `frontend/src/components/asset/AssetForm.tsx` — 加通用路径 + `persistAsset` 抽取;删 local 遗留。
- **Create (test)** `frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx` — 纯函数 golden + section ref 行为。

---

## Task 1: ref 契约类型 + 注册表 def 字段

类型脚手架,无运行时行为,以 `tsc` 通过为验收。

**Files:**
- Create: `frontend/src/lib/assetTypes/formContract.ts`
- Modify: `frontend/src/lib/assetTypes/types.ts`

- [ ] **Step 1: 写契约文件**

创建 `frontend/src/lib/assetTypes/formContract.ts`:

```ts
import type { ForwardRefExoticComponent, RefAttributes } from "react";
import type { asset_entity } from "../../../wailsjs/go/models";

/** 父壳交给每个 section 的共享横切助手；按类型需要逐步扩充(YAGNI)。 */
export interface AssetFormContext {
  isEdit: boolean;
  /** 明文→密文(走后端 EncryptPassword);凭据类型用,local 不用。 */
  encryptPassword: (plain: string) => Promise<string>;
}

/** 保存序列化结果。 */
export interface AssetConfigBuildResult {
  configJSON: string;
  /** ssh_asset_id 关联;无隧道类型恒 0。 */
  sshTunnelId: number;
}

/** 测试连接所需最小信息(壳据此调 TestAssetConnection);serial 等无密码传 ""。 */
export interface AssetTestConfig {
  assetType: string;
  configJSON: string;
  password: string;
}

/** 每个 ConfigSection 经 useImperativeHandle 暴露的命令式句柄。 */
export interface AssetFormHandle {
  buildConfig: (ctx: AssetFormContext) => Promise<AssetConfigBuildResult>;
  /** 仅可测类型实现;不可测类型为 null。 */
  buildTestConfig: ((ctx: AssetFormContext) => Promise<AssetTestConfig>) | null;
}

export interface ConfigSectionProps {
  /** 编辑态回填来源;创建态为 undefined。 */
  editAsset?: asset_entity.Asset;
  ctx: AssetFormContext;
  /** state 变化时上报,驱动壳 Test/Save 按钮启用态(反应式)。 */
  onValidityChange: (v: { canTest: boolean; canSave: boolean }) => void;
}

export type ConfigSectionComponent = ForwardRefExoticComponent<
  ConfigSectionProps & RefAttributes<AssetFormHandle>
>;
```

- [ ] **Step 2: 扩 AssetTypeDefinition**

在 `frontend/src/lib/assetTypes/types.ts` 顶部加 import,并给 `AssetTypeDefinition` 增两个可选字段:

```ts
import type { ConfigSectionComponent } from "./formContract";
```

在 `AssetTypeDefinition` 接口内(`policy?` 之前)加:

```ts
  /** 资产表单的 per-type config 区(注册化表单);缺省 = 走遗留/扩展路径。 */
  ConfigSection?: ConfigSectionComponent;
  /** 是否支持"测试连接"(替代 isTestableAssetType 链)。 */
  testable?: boolean;
```

- [ ] **Step 3: tsc 校验**

Run: `cd frontend && npx tsc --noEmit`
Expected: 0 error(纯类型新增,既有 9 个 registry 文件不传这两个可选字段不报错)。

- [ ] **Step 4: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/lib/assetTypes/formContract.ts frontend/src/lib/assetTypes/types.ts
git commit -m "✨ 资产表单 ref 契约 + AssetTypeDefinition.ConfigSection/testable #130"
```

---

## Task 2: local 配置纯函数 + golden(RED→GREEN)

按 spec 风险项"先抽纯函数→golden 锁定→再搬进 section":先把 local 的保存/回填逻辑抽成纯函数并锁字节一致,此时 AssetForm 仍走旧 inline 分支(纯函数与之并行,golden 证等价)。

**Files:**
- Modify: `frontend/src/components/asset/LocalConfigSection.tsx`(加导出纯函数,暂不动组件)
- Create: `frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx`

- [ ] **Step 1: 写失败测试**

创建 `frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx`:

```tsx
import { describe, it, expect } from "vitest";
import { buildLocalConfig, parseLocalConfig, LOCAL_DEFAULTS } from "@/components/asset/LocalConfigSection";

describe("buildLocalConfig (锁旧 handleSubmit local 分支字节一致)", () => {
  it("shell+args+cwd 全有", () => {
    expect(buildLocalConfig({ shell: "/bin/zsh", args: "-l", cwd: "~" })).toBe(
      '{"shell":"/bin/zsh","args":["-l"],"cwd":"~"}'
    );
  });
  it("空 shell/args 省略,保留 cwd", () => {
    expect(buildLocalConfig({ shell: "", args: "", cwd: "~" })).toBe('{"cwd":"~"}');
  });
  it("空 cwd 省略", () => {
    expect(buildLocalConfig({ shell: "/bin/sh", args: "", cwd: "" })).toBe('{"shell":"/bin/sh"}');
  });
  it("args 非法时抛错(由调用方 toast)", () => {
    expect(() => buildLocalConfig({ shell: "", args: '"abc', cwd: "" })).toThrow("unclosed quote");
  });
});

describe("parseLocalConfig (锁旧 loadLocalConfig)", () => {
  it("回填 shell/args/cwd", () => {
    expect(parseLocalConfig('{"shell":"/bin/zsh","args":["-l","-i"],"cwd":"/root"}')).toEqual({
      shell: "/bin/zsh",
      args: "-l -i",
      cwd: "/root",
    });
  });
  it("缺字段用默认(cwd 缺→~)", () => {
    expect(parseLocalConfig("{}")).toEqual({ shell: "", args: "", cwd: "~" });
  });
  it("非法 JSON 回退默认", () => {
    expect(parseLocalConfig("not json")).toEqual(LOCAL_DEFAULTS);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/LocalConfigSection.test.tsx`
Expected: FAIL —— 导入 `buildLocalConfig`/`parseLocalConfig`/`LOCAL_DEFAULTS` 不存在。

- [ ] **Step 3: 实现纯函数**

在 `frontend/src/components/asset/LocalConfigSection.tsx` 顶部(组件之前)加导出。先确保 import 含 `parseLocalShellArgs`(原文件只 import 了 `formatLocalShellArgs`,需补):

```ts
import { formatLocalShellArgs, parseLocalShellArgs } from "@/lib/localShellArgs";
```

加纯函数:

```ts
export interface LocalFormState {
  shell: string;
  args: string;
  cwd: string;
}

export const LOCAL_DEFAULTS: LocalFormState = { shell: "", args: "", cwd: "~" };

/** 保存序列化:镜像旧 handleSubmit 的 local 分支(1586–1598)。args 非法→抛(调用方 toast)。 */
export function buildLocalConfig(state: LocalFormState): string {
  const cfg: Record<string, unknown> = {};
  if (state.shell) cfg.shell = state.shell;
  const argList = parseLocalShellArgs(state.args);
  if (argList.length) cfg.args = argList;
  if (state.cwd) cfg.cwd = state.cwd;
  return JSON.stringify(cfg);
}

/** 编辑态回填:镜像旧 loadLocalConfig(917–926)。解析失败→默认值。 */
export function parseLocalConfig(configJSON: string): LocalFormState {
  try {
    const cfg = JSON.parse(configJSON || "{}");
    return {
      shell: cfg.shell || "",
      args: formatLocalShellArgs(cfg.args || []),
      cwd: cfg.cwd || "~",
    };
  } catch {
    return { ...LOCAL_DEFAULTS };
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/LocalConfigSection.test.tsx`
Expected: PASS(7 用例全绿)。

- [ ] **Step 5: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/components/asset/LocalConfigSection.tsx frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx
git commit -m "✅ 抽 local 配置纯函数 buildLocalConfig/parseLocalConfig + golden #130"
```

---

## Task 3: 迁移 local —— section 自持 state + 壳通用路径(删遗留)

组件 prop 接口变更会同时打破壳的遗留渲染(2110–2119 传旧 props),故组件重写与壳改动一并落地、一次提交。

**Files:**
- Modify: `frontend/src/components/asset/LocalConfigSection.tsx`(重写组件)
- Modify: `frontend/src/lib/assetTypes/local.ts`(注册 ConfigSection)
- Modify: `frontend/src/components/asset/AssetForm.tsx`(通用路径 + 删 local 遗留)
- Modify: `frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx`(加 section ref 行为测试)

- [ ] **Step 1: 写失败测试(section ref 行为)**

在 `LocalConfigSection.test.tsx` 顶部补 import:

```tsx
import { render } from "@testing-library/react";
import { createRef } from "react";
import { vi } from "vitest";
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/local/Local", () => ({ ListLocalShells: () => Promise.resolve([]) }));

const fakeCtx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => p };
```

加测试 describe:

```tsx
describe("LocalConfigSection ref 契约", () => {
  it("创建态:buildConfig 返回默认 JSON,buildTestConfig 为 null", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<LocalConfigSection ref={ref} ctx={fakeCtx} onValidityChange={() => {}} />);
    expect(ref.current!.buildTestConfig).toBeNull();
    await expect(ref.current!.buildConfig(fakeCtx)).resolves.toEqual({
      configJSON: '{"cwd":"~"}',
      sshTunnelId: 0,
    });
  });

  it("编辑态:从 editAsset.Config 回填后 buildConfig round-trip 一致", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "local",
      Config: '{"shell":"/bin/zsh","args":["-l"],"cwd":"/root"}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<LocalConfigSection ref={ref} editAsset={editAsset} ctx={fakeCtx} onValidityChange={() => {}} />);
    const r = await ref.current!.buildConfig(fakeCtx);
    expect(r.configJSON).toBe('{"shell":"/bin/zsh","args":["-l"],"cwd":"/root"}');
  });

  it("上报 canSave=true / canTest=false", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<LocalConfigSection ref={ref} ctx={fakeCtx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenCalledWith({ canTest: false, canSave: true });
  });
});
```

> 注:`react-i18next` 的 `useTranslation` 在仓库 vitest setup 已全局 mock(110 文件现状);若该 section 测试报 `t` 未定义,按既有 setup 文件补 mock(与其它组件测试一致),不在本步新造。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/LocalConfigSection.test.tsx`
Expected: FAIL —— `LocalConfigSection` 尚不接受 `ref`/`editAsset`/`ctx`/`onValidityChange`(旧 props 为 shell/setShell…),`ref.current` 为 null。

- [ ] **Step 3: 重写 LocalConfigSection 组件为 forwardRef 自持 state**

把 `frontend/src/components/asset/LocalConfigSection.tsx` 的 `LocalConfigSectionProps` 接口与 `LocalConfigSection` 函数整段替换为(保留 Step 3/Task2 加的纯函数 + import,顶部 React import 改为含 `forwardRef`/`useImperativeHandle`):

```tsx
import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
```

删除旧 `export interface LocalConfigSectionProps { ... }`,组件改为:

```tsx
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

export const LocalConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(
  function LocalConfigSection({ editAsset, onValidityChange }, ref) {
    const { t } = useTranslation();
    const [shells, setShells] = useState<ShellInfo[]>([]);
    const [state, setState] = useState<LocalFormState>(() =>
      editAsset ? parseLocalConfig(editAsset.Config) : { ...LOCAL_DEFAULTS }
    );

    useEffect(() => {
      ListLocalShells()
        .then((list) => setShells(list || []))
        .catch(() => setShells([]));
    }, []);

    // local 无必填校验:始终可保存、不可测试(onValidityChange 为壳 setState,身份稳定)。
    useEffect(() => {
      onValidityChange({ canTest: false, canSave: true });
    }, [onValidityChange]);

    useImperativeHandle(
      ref,
      () => ({
        buildConfig: async () => ({ configJSON: buildLocalConfig(state), sshTunnelId: 0 }),
        buildTestConfig: null,
      }),
      [state]
    );

    const patch = (p: Partial<LocalFormState>) => setState((s) => ({ ...s, ...p }));

    const onSelectPreset = (val: string) => {
      if (val === "__default__") {
        patch({ shell: "", args: "" });
        return;
      }
      const s = shells[Number(val)];
      if (s) patch({ shell: s.path, args: formatLocalShellArgs(s.args || []) });
    };

    return (
      <div className="grid gap-3 border rounded-lg p-4">
        <div className="grid gap-2">
          <Label>{t("asset.localShell")}</Label>
          <Select onValueChange={onSelectPreset}>
            <SelectTrigger>
              <SelectValue placeholder={t("asset.localShellPreset")} />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="__default__">{t("asset.localDefaultShell")}</SelectItem>
              {shells.map((s, i) => (
                <SelectItem key={`${s.path}-${i}`} value={String(i)}>
                  {s.name}
                  {s.args && s.args.length ? ` (${s.path} ${formatLocalShellArgs(s.args)})` : ` (${s.path})`}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Input
            value={state.shell}
            onChange={(e) => patch({ shell: e.target.value })}
            placeholder={t("asset.localShellPlaceholder")}
            className="font-mono"
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.localArgs")}</Label>
          <Input
            value={state.args}
            onChange={(e) => patch({ args: e.target.value })}
            placeholder={t("asset.localArgsPlaceholder")}
            className="font-mono"
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("asset.localCwd")}</Label>
          <Input
            value={state.cwd}
            onChange={(e) => patch({ cwd: e.target.value })}
            placeholder={t("asset.localCwdPlaceholder")}
            className="font-mono"
          />
        </div>
      </div>
    );
  }
);
```

- [ ] **Step 4: 注册 local.ConfigSection**

在 `frontend/src/lib/assetTypes/local.ts` import `LocalConfigSection` 并加注册字段(local 不可测,不设 `testable`):

```ts
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";
```
在 `registerAssetType({...})` 内(`DetailInfoCard` 之后、`policy` 之前)加:
```ts
  ConfigSection: LocalConfigSection,
```

- [ ] **Step 5: AssetForm 加通用路径 + persistAsset(壳改动)**

5a. 导入区(50 行后)加:
```ts
import { getAssetType } from "@/lib/assetTypes";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
```

5b. 组件内(`activeTestIdRef` 附近,~339)加 ref + validity + ctx:
```ts
  const sectionRef = useRef<AssetFormHandle>(null);
  const [validity, setValidity] = useState({ canTest: false, canSave: false });
  const ctx: AssetFormContext = useMemo(
    () => ({ isEdit: !!editAsset, encryptPassword: EncryptPassword }),
    [editAsset]
  );
```

5c. 抽共享持久化(放在 `handleSubmit` 之前):
```ts
  const persistAsset = async (asset: asset_entity.Asset) => {
    setSaving(true);
    try {
      if (editAsset?.ID) {
        asset.ID = editAsset.ID;
        await updateAsset(asset);
      } else {
        await createAsset(asset);
      }
      onOpenChange(false);
    } catch (e) {
      toast.error(String(e));
    } finally {
      setSaving(false);
    }
  };
```

5d. `handleSubmit` 开头(`cancelActiveTest();` 之后、`let config: string;` 之前)插入通用路径:
```ts
    const def = getAssetType(assetType);
    if (def?.ConfigSection) {
      if (!sectionRef.current) return;
      let built;
      try {
        built = await sectionRef.current.buildConfig(ctx);
      } catch (e) {
        toast.error(e instanceof Error ? e.message : String(e));
        return;
      }
      const asset = new asset_entity.Asset({
        ...(editAsset || {}),
        Name: name,
        Type: assetType,
        GroupID: groupId,
        Icon: icon,
        Description: description,
        Config: built.configJSON,
        sshTunnelId: built.sshTunnelId,
      });
      await persistAsset(asset);
      return;
    }
```

5e. 遗留持久化尾(原 1640–1653 `setSaving(true)…finally`)替换为:
```ts
    await persistAsset(asset);
```

5f. 编辑回填分发(479–517):在 `setDescription(...)` 之后、`if (editType === "ssh")` 之前加迁移类型短路(已注册化类型由 section 经 `editAsset` prop 自行回填,壳不再调 loadXxx):
```ts
        if (getAssetType(editType)?.ConfigSection) {
          // 已注册化类型:config 回填由 section 经 editAsset prop 完成,壳跳过
        } else if (editType === "ssh") {
```
(即把原 `if (editType === "ssh") {` 改成 `} else if (editType === "ssh") {`,前面接上述短路 if。)

5g. 通用渲染块:在 per-type 渲染区(SSH 块之前,~1829)加:
```tsx
            {def?.ConfigSection && (
              <def.ConfigSection
                key={assetType}
                ref={sectionRef}
                editAsset={editAsset ?? undefined}
                ctx={ctx}
                onValidityChange={setValidity}
              />
            )}
```
> `def` 已在 `handleSubmit` 外作用域?否 —— `def` 在 handleSubmit 内定义。渲染处需另取:在 return 之前(`typeLabel` 附近 ~1656)加 `const sectionDef = getAssetType(assetType);`,渲染块与 saveDisabled 用 `sectionDef`。把 5g/5h 的 `def?.ConfigSection` 改为 `sectionDef?.ConfigSection`。

5h. saveDisabled 接入 validity(generic 类型用 section 上报,不再走 per-type reason):把 `saveDisabledReason`(1689)首段改为:
```ts
  const saveDisabledReason = !name.trim()
    ? "asset.formMissingName"
    : sectionDef?.ConfigSection
      ? ""
      : assetType === "database" && driver === "sqlite" && !path.trim()
        ? "asset.formMissingPath"
        : /* ……其余既有链不变…… */;
```
并把 `saveDisabled`(1710)改为:
```ts
  const saveDisabled = saving || !!saveDisabledReason || (!!sectionDef?.ConfigSection && !validity.canSave);
```

- [ ] **Step 6: 删 local 遗留(壳)**

删除以下(local 现走通用路径):
- state 声明 440–443(`localShell`/`localArgs`/`localCwd`)。
- `loadLocalConfig`(917–926)+ `resetLocalFields`(928–932)。
- 创建态 reset 调用 `resetLocalFields();`(532)。
- `handleTypeChange` 内 `if (newType === "local") setHost("");`(949)。
- 保存分支 `} else if (assetType === "local") { … }`(1586–1598)。
- 渲染块 `{assetType === "local" && (<LocalConfigSection … 6 props/>)}`(2110–2119)。
- 顶部 `LocalConfigSection` 的旧具名 import(45)—— 它现由通用渲染经 registry 间接使用,AssetForm 不再直接引用(若 eslint 报 unused 则删)。
- 编辑分发的 `} else if (editType === "local") { loadLocalConfig(editAsset); }`(503–504)—— 已被 5f 短路覆盖,删该分支。

> `parseLocalShellArgs`/`formatLocalShellArgs` 在 AssetForm 是否还被别处用?`formatLocalShellArgs` 仅 loadLocalConfig 用、`parseLocalShellArgs` 仅 local 保存分支用 —— 两者删除后,若 AssetForm import(46)残留 unused,一并删该 import。`DEFAULT_ICONS[newType]`(946,含 local→terminal)保留(map 查表给默认图标,非 switch)。

- [ ] **Step 7: tsc + 全量测试 + lint**

Run: `cd frontend && npx tsc --noEmit`
Expected: 0 error(尤其确认无残留引用已删的 `localShell`/`loadLocalConfig` 等)。

Run: `cd frontend && npx vitest run src/components/asset/__tests__/LocalConfigSection.test.tsx`
Expected: PASS(纯函数 7 + section ref 3 = 10 用例)。

Run: `cd frontend && npx vitest run`
Expected: 全绿(无新增失败;迁移行为保持)。

Run: `cd frontend && npx eslint src/components/asset/AssetForm.tsx src/components/asset/LocalConfigSection.tsx src/lib/assetTypes`
Expected: 0(无 unused import / unused var)。

- [ ] **Step 8: Commit**

```bash
cd /Users/codfrm/Code/opskat/opskat
git add frontend/src/components/asset/LocalConfigSection.tsx frontend/src/lib/assetTypes/local.ts frontend/src/components/asset/AssetForm.tsx frontend/src/components/asset/__tests__/LocalConfigSection.test.tsx
git commit -m "♻️ AssetForm 通用 ConfigSection 路径 + 迁移 local 注册化(section 自持 state) #130"
```

---

## Task 4: 观测验证(端到端,非自动化)

**Files:** 无(运行 app / opsctl 观测)

AssetForm 全量 render 测试无现成 harness;壳级行为按 spec 用观测验证(后端零改动)。

- [ ] **Step 1: 创建态**

跑 app,新增一个 `local` 资产(自定义 shell + args + cwd),保存。读 `opskat.db` 的 `assets` 行,确认 `Config` JSON 与迁移前格式一致(`{"shell":…,"args":[…],"cwd":…}`,空字段省略),`Type=local`、`Icon` 默认 terminal。

- [ ] **Step 2: 编辑态**

编辑刚建的 local 资产,确认表单回填 shell/args/cwd 正确;改 cwd 后保存,确认 DB 更新且其它字段不变。

- [ ] **Step 3: 类型切换**

新增资产时在类型选择器切到 local 再切走再切回,确认 local 区字段每次回到默认(remount 语义),无脏值残留。

---

## Self-Review

- **Spec 覆盖**:契约(§1)→ Task 1;通用壳路径 + persistAsset + 双路径(§2)→ Task 3 Step 5;section 自持 state + editAsset 回填 + onValidityChange(§1/§2)→ Task 3 Step 3;golden 行为保持(§5)→ Task 2;迁移顺序 local 打头(§4)→ 本计划即 4a;观测验证(§验证策略)→ Task 4。
- **未覆盖(本计划外,后续 4b+)**:其余 8 类型迁移、`testable`/`buildTestConfig` 实际接入测试编排(serial 起)、`AssetFormContext` 扩 managedPasswords/Keys/tunnelOptions、默认图标 section 上报、末 commit 删遗留 switch。`testable` 字段本计划仅定义不消费(local 不可测),消费留 serial。
- **Placeholder 扫描**:无 TBD;每步给实际代码 + 精确行号锚点 + 期望输出。
- **类型一致**:`AssetFormHandle.buildConfig/buildTestConfig`、`ConfigSectionProps.{editAsset,ctx,onValidityChange}`、`AssetConfigBuildResult.{configJSON,sshTunnelId}`、`LocalFormState.{shell,args,cwd}`、`buildLocalConfig`/`parseLocalConfig`/`LOCAL_DEFAULTS` 全程同名一致;壳侧 `sectionRef`/`validity`/`ctx`/`persistAsset`/`sectionDef` 命名贯穿 Task 3。
- **风险**:Task 3 是大 commit(组件 + 壳一并);中间态因 prop 接口变更不可单独编译,故合一。Step 5g 的 `def` 作用域已在注上修正为 `sectionDef`。
