# OSS (对象存储) 资产类型 — 前端 P2 实施计划(表单 + 注册 + 序列化 + 详情卡 + i18n)

> For agentic workers: REQUIRED SUB-SKILL: superpowers:subagent-driven-development

## Goal

后端(P1)已落地 `oss` 资产类型:`internal/model/entity/asset_entity/oss_config.go` 定义了 9 字段 `OSSConfig`,`internal/assettype/oss.go` 已注册 handler、`SafeView`、`ResolvePassword`、`ValidateCreateArgs`,且 `frontend/wailsjs/go/oss/OSS.*` 绑定已生成。本计划交付**前端**:让用户能像 SSH/数据库一样在资产表单里**新建 / 编辑 / 测试连接** OSS 资产,并在详情面板看到只读配置。

具体产出:
1. 每资产配置序列化器 `OSSConfigSection.config.ts`(纯函数 + golden test),含厂商智能预填与凭据片段映射。
2. 资产表单 section `OSSConfigSection.tsx`(连接 / 高级 双 Tab,`PasswordSourceField` 密码来源,厂商切换预填)。
3. 详情卡 `OSSDetailInfoCard.tsx`(只读 SafeView 白名单键,机密打码)。
4. 注册 `lib/assetTypes/oss.ts` + `S3` 品牌图标 + registry / options 测试更新。
5. en + zh-CN i18n 键。

**明确不在本期(P3)**:对象浏览器工作区(Bucket / 前缀树 / 对象列表 / 上传下载 / 预签名),以及为之需要的 `CopyObject` 绑定、`tabStore` / `queryStore` / `MainPanel` / `App.tsx handleConnectAsset` 的 `oss` 分派。P2 因此把 `canConnect` 置 `false`(见 Task 4)。

## Architecture

前端资产类型是**注册化**的(不改分发器 switch):每种类型在 `frontend/src/lib/assetTypes/<type>.ts` 里调用 `registerAssetType(def)`,`def` 携带 `ConfigSection`(表单)与 `DetailInfoCard`(详情)。`AssetForm.tsx` 与 `AssetDetail.tsx` 纯粹按注册表渲染,**无需改动**。表单 section 走统一的 `useConfigSection` hook(收编 state/patch/校验上报/imperative handle 样板)+ 声明式 `ConfigGroupSchema<S>[]`(经 `buildConfigGroups` → `ConfigTabs`)。凭据(secret / 托管密码)由 `useAssetCredential` 独立持有,经 `PasswordSourceField` 渲染。序列化器是与后端 `OSSConfig` JSON 契约一一对应的纯函数,单测用 golden 锁字节序。

数据流(保存):`OSSConfigSection` 的 `build(state, ctx)` → `buildOSSConfig(state, resolveSaveCredential(cred, encrypt))` → `{ configJSON, sshTunnelId: 0 }` → 上层写入 `Asset.Config`。测试连接:`buildTestConfig(state)` → `{ assetType: "oss", configJSON, password }`,交由既有测试连接流(后端 `OSSTestConnection` / `connpool`)。编辑回填:`init` = `parseOSSConfig(Config)`(非机密字段),凭据 = `useAssetCredential(editAsset, ossCredentialFragment(Config))`(机密字段)。

## Tech Stack

- **前端**:React 19 + TypeScript,Wails v2 IPC(生成物在 `frontend/wailsjs/`,已 gitignore)。UI 库 `@opskat/ui`(Radix 封装)。i18n `react-i18next`,单一扁平命名空间 `frontend/src/i18n/locales/{en,zh-CN}/common.json`。图标 `@iconify/react` + `@iconify-icons/*`(离线打包)。
- **测试**:`vitest`(`environment: "happy-dom"`,`setupFiles: ./src/__tests__/setup.ts` 全局 mock 所有 wailsjs binder 包 + `react-i18next` 的 `t` 返回 key 原样)+ `@testing-library/react`。
- **模块别名**:`@` → `frontend/src`(仅 `frontend/vitest.config.ts` 的 `resolve.alias`)。`wailsjs/` 在 `src` 外,须用相对路径 import。

## Global Constraints

**后端 JSON 契约(真源,不可偏离)** — 表单产出的 config JSON 键必须精确匹配 `internal/model/entity/asset_entity/oss_config.go` 的 snake_case tag,共 9 键:

```
provider · endpoint · region · access_key_id · secret_access_key · credential_id · use_path_style · use_ssl · connect_timeout
```

- **厂商枚举**(值,`OSSConfig.Provider` 注释):`s3 | aliyun-oss | tencent-cos | minio | s3-compat`。
- **禁止新增字段**:spec §4/§5 提到的「分片大小 / part-size」在真实后端结构体里**不存在** —— 不要加 `partSizeMB` / chunk-size。无 SSH 隧道、无代理、无 TLS 证书 Tab(那些是 etcd 的,OSS 不适用)。
- **OSS 无 policy**:后端 `PolicyKind() == ""`、`DefaultPolicy() == nil` —— 注册时**省略 `policy`** 字段(`policy: undefined`)。
- **机密处理**:`Access Key ID` 明文字段;`Secret Access Key` 走 `PasswordSourceField`(手动输入 → `secret_access_key`;托管密码 → `credential_id`,选 `password` 类型凭证)。详情卡**绝不渲染** `secret_access_key`(打码)与 `credential_id`。
- **`use_ssl` 语义**:默认开(HTTPS),`buildOSSConfig` **始终写显式布尔**(`use_ssl: true|false`),因为「关闭」是有意义的非默认态,不能靠省略键表达。其余布尔(`use_path_style`)默认关 → 仅为 `true` 时写键。
- **`connect_timeout`**:秒;`0` 表示用后端默认 → `0` 时省略键,表单数字框 `blankWhenZero` 显示空。

**Gate 命令**(工作目录 `frontend/`,包管理器 pnpm;来自 `frontend/package.json` scripts + `docs/DEVELOP.md`):

```bash
cd frontend && pnpm test <单文件路径>   # 单测(vitest run 的位置参数是路径/子串过滤)
cd frontend && pnpm test               # 全量 vitest
cd frontend && npx tsc -b              # 类型检查(无独立 typecheck 脚本;等于 pnpm build 的前半)
cd frontend && pnpm lint               # eslint(可 pnpm lint:fix)
```

**提交约定**:gitmoji + 中文 subject,每个 Task 结束提交一次。**不带** issue / 评审编号(除非刻意关联 issue)。示例见各 Task 的 commit 步骤。

**i18n 铁律**:`en/common.json` 与 `zh-CN/common.json` 必须**同键锁步**(无 fallback locale)。各语言用地道表达,**不逐字互译**。

## File Structure

**新建**

| 文件 | 单一职责 |
| --- | --- |
| `frontend/src/components/asset/OSSConfigSection.config.ts` | 纯序列化器:`buildOSSConfig` / `parseOSSConfig` + `providerPrefillPatch`(厂商预填)+ `ossCredentialFragment`(编辑态机密映射)+ `OSS_DEFAULTS` / `OSSFormState` / `OSS_PROVIDER_VALUES` / `OSS_PROVIDER_LABEL_KEYS` |
| `frontend/src/components/asset/OSSConfigSection.tsx` | 表单 section:连接 / 高级 双 Tab 的声明式 schema + 厂商下拉(触发预填)+ 密码来源;`forwardRef<AssetFormHandle>` |
| `frontend/src/components/asset/detail/OSSDetailInfoCard.tsx` | 只读详情卡:渲染 SafeView 白名单键,机密打码 |
| `frontend/src/lib/assetTypes/oss.ts` | `registerAssetType({ type: "oss", ... })`,`canConnect:false`、无 policy |
| `frontend/src/components/asset/__tests__/OSSConfigSection.config.test.ts` | golden:锁 build 字段序 / parse 往返 / 厂商预填 / 凭据映射 |
| `frontend/src/components/asset/__tests__/OSSConfigSection.test.tsx` | section ref 契约:`buildConfig` / `buildTestConfig` 形状 + 校验上报 |
| `frontend/src/__tests__/OSSConfigSection.tabs.test.tsx` | Tab testid:`config-tab-connection` / `config-tab-advanced` 存在,tunnel / tls 不存在 |

**修改**

| 文件 | 改动 |
| --- | --- |
| `frontend/src/components/asset/configFields.tsx` | 给 `password` field-kind 加可选 `usernameKey?: keyof S`(默认 `"username"`),让 OSS 把托管凭证的 username 回填到 `accessKeyId`(而非硬编码 `username`) |
| `frontend/src/components/asset/brand-icons.tsx` | 新增 `S3Icon = brandIcon(awsS3Icon)`(`@iconify-icons/logos/aws-s3`) |
| `frontend/src/components/asset/IconPicker.tsx` | 把 `S3Icon` 接入图标选择器(import + `database` 分类 + `ICON_DISPLAY_NAMES`) |
| `frontend/src/lib/assetTypes/index.ts` | 追加 `import "./oss";`(etcd 之后,末位) |
| `frontend/src/lib/assetTypes/__tests__/registry.test.ts` | `getBuiltinTypes` 列表追加 `"oss"`;补 oss 的 connectAction / canConnect / canOpenFileManager 断言 |
| `frontend/src/__tests__/assetTypeOptions.test.ts` | 内建 value 列表 / category `byValue` / `buildAssetTypeGroups` 追加 `"oss"`;**重命名**与新内建 `oss` 撞名的扩展 mock(`oss` → `filestore`) |
| `frontend/src/components/asset/detail/__tests__/DetailInfoCards.test.tsx` | 加 `describe("OSSDetailInfoCard")` |
| `frontend/src/i18n/locales/en/common.json` | `nav.oss` + `asset.typeOSS` + `oss.form.*` + `oss.error.required` |
| `frontend/src/i18n/locales/zh-CN/common.json` | 同上,中文文案 |

**不改动**(注册化,已自动生效):`AssetForm.tsx`、`AssetDetail.tsx`、`lib/assetTypes/options.ts`(category / 分组从注册表 `def.category` 派生)。

---

## Task 1 — 配置序列化器 `OSSConfigSection.config.ts` + golden 测试

纯函数,零 React,先立地基。厂商预填与凭据映射都在这里做「有意义的分支」,表单层只调用(满足「预填在 config/section 层,不在共享代码里按字符串分支」)。

**Files**
- 新建 `frontend/src/components/asset/OSSConfigSection.config.ts`
- 新建 `frontend/src/components/asset/__tests__/OSSConfigSection.config.test.ts`

**Interfaces**
- Consumes: `type CredentialFragment = { credential_id?: number; password?: string }`(`@/components/asset/credentialConfig`)。
- Produces:
  - `interface OSSFormState { provider: string; endpoint: string; region: string; accessKeyId: string; usePathStyle: boolean; useSSL: boolean; connectTimeout: number }`
  - `const OSS_DEFAULTS: OSSFormState`
  - `const OSS_PROVIDER_VALUES: readonly ["s3","aliyun-oss","tencent-cos","minio","s3-compat"]`
  - `const OSS_PROVIDER_LABEL_KEYS: Record<string, string>`
  - `function buildOSSConfig(state: OSSFormState, cred: CredentialFragment): string`
  - `function parseOSSConfig(configJSON: string): OSSFormState`
  - `function providerPrefillPatch(provider: string): Partial<OSSFormState>`
  - `function ossCredentialFragment(configJSON: string): CredentialFragment`

**TDD**

- [ ] 写失败测试 `OSSConfigSection.config.test.ts`(完整内容):

  ```ts
  import { describe, it, expect } from "vitest";
  import {
    buildOSSConfig,
    parseOSSConfig,
    providerPrefillPatch,
    ossCredentialFragment,
    OSS_DEFAULTS,
    type OSSFormState,
  } from "@/components/asset/OSSConfigSection.config";

  const FULL: OSSFormState = {
    provider: "minio",
    endpoint: "http://localhost:9000",
    region: "us-east-1",
    accessKeyId: "AKIA",
    usePathStyle: true,
    useSSL: false,
    connectTimeout: 30,
  };

  describe("buildOSSConfig(锁字段序 provider→endpoint→region→access_key_id→cred→use_path_style→use_ssl→connect_timeout)", () => {
    it("全字段 + inline 密文", () => {
      expect(buildOSSConfig(FULL, { password: "ENC" })).toBe(
        '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
          '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"connect_timeout":30}'
      );
    });
    it("托管凭据 → credential_id 紧跟 access_key_id,不写 secret_access_key", () => {
      const json = buildOSSConfig(FULL, { credential_id: 7 });
      expect(json).toContain('"access_key_id":"AKIA","credential_id":7,"use_path_style":true');
      expect(json).not.toContain("secret_access_key");
    });
    it("空片段不写 credential_id / secret_access_key", () => {
      const json = buildOSSConfig(FULL, {});
      expect(json).not.toContain("secret_access_key");
      expect(json).not.toContain("credential_id");
    });
    it("默认态最小输出(use_ssl 默认开始终写)", () => {
      expect(buildOSSConfig(OSS_DEFAULTS, {})).toBe('{"provider":"s3","use_ssl":true}');
    });
    it("use_path_style 关闭时省略该键;use_ssl 关闭仍写显式 false", () => {
      const json = buildOSSConfig({ ...OSS_DEFAULTS, endpoint: "e", accessKeyId: "a", useSSL: false }, {});
      expect(json).not.toContain("use_path_style");
      expect(json).toContain('"use_ssl":false');
    });
    it("connect_timeout 为 0 时省略该键", () => {
      expect(buildOSSConfig({ ...OSS_DEFAULTS, connectTimeout: 0 }, {})).not.toContain("connect_timeout");
    });
  });

  describe("parseOSSConfig(镜像 build 字段集;secret 不入表单态)", () => {
    it("全字段回填(忽略 secret_access_key)", () => {
      expect(
        parseOSSConfig(
          '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
            '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"connect_timeout":30}'
        )
      ).toEqual(FULL);
    });
    it("缺字段用默认(provider→s3,use_ssl→true)", () => {
      expect(parseOSSConfig("{}")).toEqual(OSS_DEFAULTS);
    });
    it("显式 use_ssl:false 不被默认覆盖", () => {
      expect(parseOSSConfig('{"use_ssl":false}').useSSL).toBe(false);
    });
    it("非法 JSON 回退默认", () => {
      expect(parseOSSConfig("nope")).toEqual(OSS_DEFAULTS);
    });
    it("parse→build 往返(密文经 cred 片段回注)", () => {
      const original =
        '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
        '"access_key_id":"AKIA","secret_access_key":"ENC","use_path_style":true,"use_ssl":false,"connect_timeout":30}';
      expect(buildOSSConfig(parseOSSConfig(original), { password: "ENC" })).toBe(original);
    });
  });

  describe("providerPrefillPatch(纯函数,厂商→endpoint/region/path-style 预填)", () => {
    it("s3:virtual-hosted(path-style 关)", () => {
      expect(providerPrefillPatch("s3")).toEqual({
        provider: "s3", endpoint: "s3.us-east-1.amazonaws.com", region: "us-east-1", usePathStyle: false,
      });
    });
    it("aliyun-oss", () => {
      expect(providerPrefillPatch("aliyun-oss")).toEqual({
        provider: "aliyun-oss", endpoint: "oss-cn-hangzhou.aliyuncs.com", region: "cn-hangzhou", usePathStyle: false,
      });
    });
    it("tencent-cos", () => {
      expect(providerPrefillPatch("tencent-cos")).toEqual({
        provider: "tencent-cos", endpoint: "cos.ap-guangzhou.myqcloud.com", region: "ap-guangzhou", usePathStyle: false,
      });
    });
    it("minio:path-style 开", () => {
      expect(providerPrefillPatch("minio")).toEqual({
        provider: "minio", endpoint: "http://localhost:9000", region: "us-east-1", usePathStyle: true,
      });
    });
    it("s3-compat:仅切 provider,不预填(保留用户已填)", () => {
      expect(providerPrefillPatch("s3-compat")).toEqual({ provider: "s3-compat" });
    });
  });

  describe("ossCredentialFragment(编辑态映射 secret_access_key/credential_id → 通用片段)", () => {
    it("托管 → credential_id", () => {
      expect(ossCredentialFragment('{"credential_id":9}')).toEqual({ credential_id: 9 });
    });
    it("inline 密文 → password", () => {
      expect(ossCredentialFragment('{"secret_access_key":"ENC"}')).toEqual({ password: "ENC" });
    });
    it("credential_id 优先于 secret_access_key", () => {
      expect(ossCredentialFragment('{"credential_id":9,"secret_access_key":"ENC"}')).toEqual({ credential_id: 9 });
    });
    it("都无 / 非法 JSON → 空片段", () => {
      expect(ossCredentialFragment("{}")).toEqual({});
      expect(ossCredentialFragment("nope")).toEqual({});
    });
  });
  ```

- [ ] 运行(预期红):`cd frontend && pnpm test src/components/asset/__tests__/OSSConfigSection.config.test.ts`
  预期失败:`Error: Failed to resolve import "@/components/asset/OSSConfigSection.config"`(模块尚不存在)。

- [ ] 实现 `OSSConfigSection.config.ts`(完整内容):

  ```ts
  import type { CredentialFragment } from "./credentialConfig";

  /** 与后端 asset_entity.OSSConfig 一一对应的 JSON 形状(snake_case)。secret_access_key/credential_id 由凭据层写入。 */
  interface OSSConfigJSON {
    provider?: string;
    endpoint?: string;
    region?: string;
    access_key_id?: string;
    secret_access_key?: string;
    credential_id?: number;
    use_path_style?: boolean;
    use_ssl?: boolean;
    connect_timeout?: number;
  }

  /** 表单态:非机密字段;机密(secret/托管凭证)留在 useAssetCredential,不在此。 */
  export interface OSSFormState {
    provider: string;
    endpoint: string;
    region: string;
    accessKeyId: string;
    usePathStyle: boolean;
    useSSL: boolean;
    connectTimeout: number;
  }

  export const OSS_DEFAULTS: OSSFormState = {
    provider: "s3",
    endpoint: "",
    region: "",
    accessKeyId: "",
    usePathStyle: false,
    useSSL: true,
    connectTimeout: 0,
  };

  /** 厂商枚举(与后端 OSSConfig.Provider 注释一致)。 */
  export const OSS_PROVIDER_VALUES = ["s3", "aliyun-oss", "tencent-cos", "minio", "s3-compat"] as const;

  /** 厂商值 → 展示标签 i18n key(表单下拉 + 详情卡共用,单一出处)。 */
  export const OSS_PROVIDER_LABEL_KEYS: Record<string, string> = {
    s3: "oss.form.providerS3",
    "aliyun-oss": "oss.form.providerAliyunOSS",
    "tencent-cos": "oss.form.providerTencentCOS",
    minio: "oss.form.providerMinio",
    "s3-compat": "oss.form.providerS3Compat",
  };

  /** 厂商智能预填:endpoint 模板 + region 默认 + path-style 默认。s3-compat 不预填。 */
  const PROVIDER_PREFILL: Record<string, { endpoint: string; region: string; usePathStyle: boolean }> = {
    s3: { endpoint: "s3.us-east-1.amazonaws.com", region: "us-east-1", usePathStyle: false },
    "aliyun-oss": { endpoint: "oss-cn-hangzhou.aliyuncs.com", region: "cn-hangzhou", usePathStyle: false },
    "tencent-cos": { endpoint: "cos.ap-guangzhou.myqcloud.com", region: "ap-guangzhou", usePathStyle: false },
    minio: { endpoint: "http://localhost:9000", region: "us-east-1", usePathStyle: true },
  };

  /** 纯函数:切换厂商时的 patch。已知厂商 → 覆写 endpoint/region/usePathStyle;s3-compat/未知 → 仅切 provider,保留用户已填。 */
  export function providerPrefillPatch(provider: string): Partial<OSSFormState> {
    const p = PROVIDER_PREFILL[provider];
    if (!p) return { provider };
    return { provider, endpoint: p.endpoint, region: p.region, usePathStyle: p.usePathStyle };
  }

  /** 编辑态:把 OSS config 的 secret_access_key/credential_id 映射成通用凭据片段,喂给 useAssetCredential。 */
  export function ossCredentialFragment(configJSON: string): CredentialFragment {
    try {
      const cfg: OSSConfigJSON = JSON.parse(configJSON || "{}");
      if (cfg.credential_id) return { credential_id: cfg.credential_id };
      if (cfg.secret_access_key) return { password: cfg.secret_access_key };
      return {};
    } catch {
      return {};
    }
  }

  /** 序列化:按后端结构体字段序写键;空/false/0 一律省略,use_ssl 例外(默认开,始终写显式布尔)。 */
  export function buildOSSConfig(state: OSSFormState, cred: CredentialFragment): string {
    const cfg: OSSConfigJSON = {};
    if (state.provider) cfg.provider = state.provider;
    if (state.endpoint) cfg.endpoint = state.endpoint;
    if (state.region) cfg.region = state.region;
    if (state.accessKeyId) cfg.access_key_id = state.accessKeyId;
    if (cred.credential_id) cfg.credential_id = cred.credential_id;
    else if (cred.password) cfg.secret_access_key = cred.password;
    if (state.usePathStyle) cfg.use_path_style = true;
    cfg.use_ssl = state.useSSL;
    if (state.connectTimeout > 0) cfg.connect_timeout = state.connectTimeout;
    return JSON.stringify(cfg);
  }

  /** 反序列化:镜像 build 字段集;secret_access_key 不进表单态(凭据独立管理);非法 JSON 回退默认。 */
  export function parseOSSConfig(configJSON: string): OSSFormState {
    try {
      const cfg: OSSConfigJSON = JSON.parse(configJSON || "{}");
      return {
        provider: cfg.provider || "s3",
        endpoint: cfg.endpoint || "",
        region: cfg.region || "",
        accessKeyId: cfg.access_key_id || "",
        usePathStyle: cfg.use_path_style || false,
        useSSL: cfg.use_ssl ?? true,
        connectTimeout: cfg.connect_timeout || 0,
      };
    } catch {
      return { ...OSS_DEFAULTS };
    }
  }
  ```

- [ ] 运行(预期绿):`cd frontend && pnpm test src/components/asset/__tests__/OSSConfigSection.config.test.ts`
  预期:`Test Files 1 passed`,约 20 个 `it` 全绿。
- [ ] 类型检查:`cd frontend && npx tsc -b`(预期无错)。
- [ ] 提交:`✨ OSS 配置序列化与厂商预填纯函数`

---

## Task 2 — 表单 section `OSSConfigSection.tsx`(连接 / 高级 双 Tab + 密码来源 + 厂商预填)

**背景 API(实现者必须照用)**

- `useConfigSection<S>(opts)`(`@/components/asset/useConfigSection`)—— 收编样板,返回 `{ state, setState, patch }`。`opts` 关键字段:
  - `ref: Ref<AssetFormHandle>` · `editAsset?: asset_entity.Asset` · `onValidityChange: (v: SectionValidity) => void`
  - `init: (editAsset?) => S`(编辑态 parse,创建态 `{...DEFAULTS}`)
  - `validate: (state) => SectionValidity`(纯函数,`SectionValidity = { canTest: boolean; canSave: boolean; saveDisabledReason?: string }`)
  - `build: (state, ctx: AssetFormContext) => Promise<AssetConfigBuildResult>`,`AssetConfigBuildResult = { configJSON: string; sshTunnelId: number }`(`sshTunnelId` 非可选,OSS 恒 `0`);`AssetFormContext = { isEdit: boolean; encryptPassword: (plain) => Promise<string> }`
  - `buildTest?: (state, ctx) => Promise<AssetTestConfig>`,`AssetTestConfig = { assetType: string; configJSON: string; password: string }`(省略 = 不可测)
  - `deps?: unknown[]`(驱动 imperative handle 重建;放 `cred.value`)
- `ConfigGroupSchema<S>[]` + `buildConfigGroups(schema, { state, patch, ctx })` → `ConfigTabs`(`@/components/asset/configFields` / `@/components/asset/ConfigTabs`)。`ConfigTabs` 在 `groups.length > 1` 时渲染下划线 Tab,每 Tab `data-testid="config-tab-<key>"`。field-kind 见 `configFields.tsx`:`text` / `number`(`min` / `blankWhenZero`)/ `switch` / `password`(渲染 `PasswordSourceField`,要求 `ctx.cred`)/ `custom`(`render: (s, patch) => ReactNode` 逃逸口)。
- `useAssetCredential(editAsset?, initialCredentialConfig?: CredentialFragment)`(`@/components/asset/useAssetCredential`)——自持凭据子状态 + 加载 `ListCredentialsByType("password")` + 编辑态回填。**关键**:不传第二参时它按 `{credential_id, password}` 解析 `editAsset.Config`;OSS 的机密键是 `secret_access_key` 不是 `password`,所以**必须**传 `ossCredentialFragment(editAsset.Config)` 做映射,否则 inline 既有密文回填不上、保存会丢 `secret_access_key`。
- `resolveSaveCredential(cred.value, encrypt)` / `resolveTestCredential(cred.value)`(`@/components/asset/credentialConfig`)→ `CredentialFragment`(`{credential_id?} | {password?} | {}`),直接喂 `buildOSSConfig` 的第二参。

**Files**
- 新建 `frontend/src/components/asset/OSSConfigSection.tsx`
- 修改 `frontend/src/components/asset/configFields.tsx`(给 `password` field-kind 加 `usernameKey?: keyof S`)
- 新建 `frontend/src/components/asset/__tests__/OSSConfigSection.test.tsx`(ref 契约)
- 新建 `frontend/src/__tests__/OSSConfigSection.tabs.test.tsx`(Tab testid)

**Interfaces**
- Consumes:Task 1 全部导出;`useConfigSection` / `buildConfigGroups` / `ConfigGroupSchema` / `ConfigTabs` / `useAssetCredential` / `resolveSaveCredential` / `resolveTestCredential`;`AssetFormHandle` / `ConfigSectionProps`(`@/lib/assetTypes/formContract`);`Field`(`@/components/asset/fields`);`Select*`(`@opskat/ui`)。
- Produces:`export const OSSConfigSection: ForwardRefExoticComponent<ConfigSectionProps & RefAttributes<AssetFormHandle>>`。

**TDD**

- [ ] 先改 `configFields.tsx`(Key Finding #6:`{ kind: "password" }` 硬编码把托管凭证 username 打到 `username` 键;OSS 的对应字段是 `accessKeyId`。加可选 `usernameKey` 让它可路由,默认 `"username"` 保持既有 6 个 section 行为不变)。两处编辑:

  1. `password` variant 的类型声明(第 63 行附近):
     ```ts
     | { kind: "password"; placeholder?: string; secretLabel?: string; selectSecretLabel?: string; usernameKey?: keyof S }
     ```
  2. `case "password"` 里的 `onUsernameChange`(第 210 行附近):
     ```tsx
     onUsernameChange={(v) => patch({ [field.usernameKey ?? "username"]: v } as unknown as Partial<S>)}
     ```
     其余属性不动。此改为纯加法(现有调用不传 `usernameKey` → 落 `"username"`,行为等价),由既有 SSH/Redis/Database/MongoDB/Kafka/Etcd section 的现有测试兜底。

- [ ] 写失败测试 ①（ref 契约）`frontend/src/components/asset/__tests__/OSSConfigSection.test.tsx`(完整内容):

  ```tsx
  import { describe, it, expect, vi } from "vitest";
  import { render } from "@testing-library/react";
  import { createRef } from "react";
  import { OSSConfigSection } from "@/components/asset/OSSConfigSection";
  import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
  import { asset_entity } from "../../../../wailsjs/go/models";

  vi.mock("../../../../wailsjs/go/system/System", () => ({
    ListCredentialsByType: () => Promise.resolve([]),
    GetAssetPassword: () => Promise.resolve(""),
  }));

  const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

  describe("OSSConfigSection ref 契约", () => {
    it("编辑态(inline 既有密文):buildConfig 沿用密文,sshTunnelId 恒 0;buildTestConfig 同形,password 空", async () => {
      const editAsset = new asset_entity.Asset({
        Type: "oss",
        Config:
          '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
          '"access_key_id":"AKIA","secret_access_key":"OLD","use_path_style":true,"use_ssl":false,"connect_timeout":30}',
      });
      const ref = createRef<AssetFormHandle>();
      render(<OSSConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
      const built = await ref.current!.buildConfig(ctx);
      expect(built).toEqual({
        configJSON:
          '{"provider":"minio","endpoint":"http://localhost:9000","region":"us-east-1",' +
          '"access_key_id":"AKIA","secret_access_key":"OLD","use_path_style":true,"use_ssl":false,"connect_timeout":30}',
        sshTunnelId: 0,
      });
      const tc = await ref.current!.buildTestConfig!(ctx);
      expect(tc).toEqual({ assetType: "oss", configJSON: built.configJSON, password: "" });
    });

    it("创建态(无 endpoint/AK):上报 canSave/canTest=false + oss.error.required", () => {
      const onValidity = vi.fn();
      const ref = createRef<AssetFormHandle>();
      render(<OSSConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
      expect(onValidity).toHaveBeenLastCalledWith({
        canTest: false,
        canSave: false,
        saveDisabledReason: "oss.error.required",
      });
    });

    it("编辑态(有 endpoint+AK):上报 canSave/canTest=true,无 reason", () => {
      const editAsset = new asset_entity.Asset({
        Type: "oss",
        Config: '{"endpoint":"http://localhost:9000","access_key_id":"AKIA"}',
      });
      const onValidity = vi.fn();
      const ref = createRef<AssetFormHandle>();
      render(<OSSConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
      expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
    });
  });
  ```

- [ ] 写失败测试 ②（Tab testid）`frontend/src/__tests__/OSSConfigSection.tabs.test.tsx`(完整内容;沿用 etcd tabs 测试写法,全局 setup 已 mock System,无需本地 mock):

  ```tsx
  import { describe, it, expect, vi } from "vitest";
  import { render, screen } from "@testing-library/react";
  import { OSSConfigSection } from "@/components/asset/OSSConfigSection";

  const ctx = { isEdit: false, encryptPassword: vi.fn() };

  describe("OSSConfigSection tabs", () => {
    it("renders connection / advanced tabs (no tunnel/tls)", () => {
      render(<OSSConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
      expect(screen.getByTestId("config-tab-connection")).toBeInTheDocument();
      expect(screen.getByTestId("config-tab-advanced")).toBeInTheDocument();
      expect(screen.queryByTestId("config-tab-tunnel")).not.toBeInTheDocument();
      expect(screen.queryByTestId("config-tab-tls")).not.toBeInTheDocument();
    });
  });
  ```

- [ ] 运行(预期红):
  ```bash
  cd frontend && pnpm test src/components/asset/__tests__/OSSConfigSection.test.tsx src/__tests__/OSSConfigSection.tabs.test.tsx
  ```
  预期失败:`Failed to resolve import "@/components/asset/OSSConfigSection"`(section 尚不存在)。

- [ ] 实现 `OSSConfigSection.tsx`(完整内容):

  ```tsx
  import { forwardRef } from "react";
  import { useTranslation } from "react-i18next";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@opskat/ui";
  import { Field } from "@/components/asset/fields";
  import { ConfigTabs } from "@/components/asset/ConfigTabs";
  import { useConfigSection } from "@/components/asset/useConfigSection";
  import { buildConfigGroups, type ConfigGroupSchema } from "@/components/asset/configFields";
  import { useAssetCredential } from "./useAssetCredential";
  import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
  import {
    buildOSSConfig,
    parseOSSConfig,
    providerPrefillPatch,
    ossCredentialFragment,
    OSS_DEFAULTS,
    OSS_PROVIDER_VALUES,
    OSS_PROVIDER_LABEL_KEYS,
    type OSSFormState,
  } from "./OSSConfigSection.config";
  import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";

  /** 厂商下拉:选中即触发智能预填(endpoint/region/path-style)。逻辑走 providerPrefillPatch 纯函数,
   *  不在共享 configFields 里按厂商字符串分支(满足 OCP:扩展靠注册/纯函数,不改分发器)。 */
  function ProviderField({ state, patch }: { state: OSSFormState; patch: (p: Partial<OSSFormState>) => void }) {
    const { t } = useTranslation();
    return (
      <Field label={t("oss.form.provider")}>
        <Select value={state.provider} onValueChange={(v) => patch(providerPrefillPatch(v))}>
          <SelectTrigger data-testid="oss-provider-select" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {OSS_PROVIDER_VALUES.map((v) => (
              <SelectItem key={v} value={v}>
                {t(OSS_PROVIDER_LABEL_KEYS[v])}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
    );
  }

  const OSS_GROUPS: ConfigGroupSchema<OSSFormState>[] = [
    {
      key: "connection",
      label: "asset.tabConnection",
      fields: [
        { kind: "custom", render: (s, patch) => <ProviderField state={s} patch={patch} /> },
        { kind: "text", key: "endpoint", label: "oss.form.endpoint", required: true, placeholder: "oss.form.endpointPlaceholder" },
        { kind: "text", key: "region", label: "oss.form.region", placeholder: "oss.form.regionPlaceholder" },
        { kind: "text", key: "accessKeyId", label: "oss.form.accessKeyId" },
        { kind: "password", usernameKey: "accessKeyId", secretLabel: "oss.form.secretAccessKey" },
      ],
    },
    {
      key: "advanced",
      label: "asset.tabAdvanced",
      fields: [
        { kind: "switch", key: "usePathStyle", label: "oss.form.usePathStyle" },
        { kind: "switch", key: "useSSL", label: "oss.form.useSSL" },
        { kind: "number", key: "connectTimeout", label: "oss.form.connectTimeout", min: 0, blankWhenZero: true },
      ],
    },
  ];

  export const OSSConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function OSSConfigSection(
    { editAsset, onValidityChange },
    ref
  ) {
    // OSS 机密键是 secret_access_key(非通用 password),编辑态须显式映射,否则 inline 密文回填/保存会丢。
    const cred = useAssetCredential(editAsset, editAsset ? ossCredentialFragment(editAsset.Config) : undefined);
    const { state, patch } = useConfigSection<OSSFormState>({
      ref,
      editAsset,
      onValidityChange,
      init: (a) => (a ? parseOSSConfig(a.Config) : { ...OSS_DEFAULTS }),
      validate: (s) => {
        const ok = s.endpoint.trim() !== "" && s.accessKeyId.trim() !== "";
        return { canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "oss.error.required" };
      },
      build: async (s, ctx) => ({
        configJSON: buildOSSConfig(s, await resolveSaveCredential(cred.value, ctx.encryptPassword)),
        sshTunnelId: 0,
      }),
      buildTest: async (s) => ({
        assetType: "oss",
        configJSON: buildOSSConfig(s, resolveTestCredential(cred.value)),
        password: cred.value.password,
      }),
      deps: [cred.value],
    });

    const groups = buildConfigGroups(OSS_GROUPS, { state, patch, ctx: { cred, editAsset } });
    return <ConfigTabs groups={groups} />;
  });
  ```

- [ ] 运行(预期绿):
  ```bash
  cd frontend && pnpm test src/components/asset/__tests__/OSSConfigSection.test.tsx src/__tests__/OSSConfigSection.tabs.test.tsx
  ```
  预期:两文件全绿(ref 3 例 + tabs 1 例)。
  说明:托管凭证 username→accessKeyId 回填走 `{ kind: "password", usernameKey: "accessKeyId" }`,由 TypeScript 保证键合法(`usernameKey: keyof OSSFormState`);其运行时回填属交互路径,留待 e2e / 手测(happy-dom 里模拟 Radix Select 选择过于脆弱,不写脆测)。
- [ ] 类型检查 + lint:`cd frontend && npx tsc -b && pnpm lint`(预期无错)。
- [ ] 提交:`✨ OSS 资产表单(连接/高级 双 Tab + 密码来源)`

---

## Task 3 — 详情卡 `OSSDetailInfoCard.tsx`

**背景 API**:`DetailInfoCardProps = { asset: asset_entity.Asset; sshTunnelName: (id?: number) => string | null }`(`@/lib/assetTypes/types`)。原语在 `@/components/asset/detail/InfoItem`:`DetailSection({ title, children })` / `DetailGrid({ children })` / `InfoItem({ label, value, mono? })`。`@/components/asset/detail/utils`:`MASKED_SECRET = "●●●●●●"`、`ENABLED_VALUE = "✓"`、`DISABLED_VALUE = "✗"`、`parseDetailConfig<T>(config?) : T | null`。OSS 不用 `TunnelInfo` / `ProxyDetailSection`(无隧道/代理)。

**Files**
- 新建 `frontend/src/components/asset/detail/OSSDetailInfoCard.tsx`
- 修改 `frontend/src/components/asset/detail/__tests__/DetailInfoCards.test.tsx`(共享详情卡测试文件,追加 OSS describe)

**Interfaces**
- Consumes:`OSS_PROVIDER_LABEL_KEYS`(Task 1);`DetailSection` / `DetailGrid` / `InfoItem`;`MASKED_SECRET` / `ENABLED_VALUE` / `DISABLED_VALUE` / `parseDetailConfig`;`DetailInfoCardProps`。
- Produces:`export function OSSDetailInfoCard(props: DetailInfoCardProps): JSX.Element | null`。

**TDD**

- [ ] 在 `DetailInfoCards.test.tsx` 追加 import 与 describe:
  - 顶部 import 增加:`import { OSSDetailInfoCard } from "../OSSDetailInfoCard";`
  - 文件末尾(最后一个 describe 之后)追加:
    ```tsx
    describe("OSSDetailInfoCard", () => {
      it("渲染非机密字段,secret 打码,provider 展示为本地化标签", () => {
        const asset = makeAsset("oss", {
          provider: "aliyun-oss",
          endpoint: "oss-cn-hangzhou.aliyuncs.com",
          region: "cn-hangzhou",
          access_key_id: "AKIA",
          secret_access_key: "ENC",
          use_path_style: false,
          use_ssl: true,
        });
        const { getByText, queryByText } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
        expect(getByText("oss-cn-hangzhou.aliyuncs.com")).toBeInTheDocument();
        expect(getByText("cn-hangzhou")).toBeInTheDocument();
        expect(getByText("AKIA")).toBeInTheDocument();
        expect(getByText("●●●●●●")).toBeInTheDocument(); // MASKED_SECRET
        expect(getByText("oss.form.providerAliyunOSS")).toBeInTheDocument(); // t mock 原样返回 key
        expect(queryByText("ENC")).not.toBeInTheDocument(); // 绝不渲染明文密文
      });

      it("托管凭证态(无 secret)不渲染 credential_id,也不渲染打码行", () => {
        const asset = makeAsset("oss", {
          provider: "minio",
          endpoint: "http://localhost:9000",
          access_key_id: "AKIA",
          credential_id: 42,
          use_path_style: true,
          use_ssl: false,
        });
        const { queryByText } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
        expect(queryByText("42")).not.toBeInTheDocument(); // credential_id 绝不渲染
        expect(queryByText("●●●●●●")).not.toBeInTheDocument();
      });

      it("handles empty config without crashing", () => {
        const asset = makeAsset("oss", {});
        const { container } = render(<OSSDetailInfoCard asset={asset} sshTunnelName={noopTunnel} />);
        expect(container).toBeDefined();
      });
    });
    ```

- [ ] 运行(预期红):`cd frontend && pnpm test src/components/asset/detail/__tests__/DetailInfoCards.test.tsx`
  预期失败:`Failed to resolve import "../OSSDetailInfoCard"`。

- [ ] 实现 `OSSDetailInfoCard.tsx`(完整内容):

  ```tsx
  import { useTranslation } from "react-i18next";
  import type { DetailInfoCardProps } from "@/lib/assetTypes/types";
  import { OSS_PROVIDER_LABEL_KEYS } from "../OSSConfigSection.config";
  import { DetailGrid, DetailSection, InfoItem } from "./InfoItem";
  import { DISABLED_VALUE, ENABLED_VALUE, MASKED_SECRET, parseDetailConfig } from "./utils";

  /** 只读 SafeView 白名单(见 internal/assettype/oss.go 的 SafeView);secret/credential_id 故意不在。 */
  interface OSSConfig {
    provider?: string;
    endpoint?: string;
    region?: string;
    access_key_id?: string;
    secret_access_key?: string;
    use_path_style?: boolean;
    use_ssl?: boolean;
    connect_timeout?: number;
  }

  export function OSSDetailInfoCard({ asset }: DetailInfoCardProps) {
    const { t } = useTranslation();

    const cfg = parseDetailConfig<OSSConfig>(asset.Config);
    if (!cfg) return null;

    return (
      <DetailSection title={t("nav.oss")}>
        <DetailGrid>
          {cfg.provider && (
            <InfoItem label={t("oss.form.provider")} value={t(OSS_PROVIDER_LABEL_KEYS[cfg.provider] ?? cfg.provider)} />
          )}
          {cfg.endpoint && <InfoItem label={t("oss.form.endpoint")} value={cfg.endpoint} mono />}
          {cfg.region && <InfoItem label={t("oss.form.region")} value={cfg.region} mono />}
          {cfg.access_key_id && <InfoItem label={t("oss.form.accessKeyId")} value={cfg.access_key_id} mono />}
          {cfg.secret_access_key && <InfoItem label={t("oss.form.secretAccessKey")} value={MASKED_SECRET} />}
          <InfoItem label={t("oss.form.usePathStyle")} value={cfg.use_path_style ? ENABLED_VALUE : DISABLED_VALUE} />
          <InfoItem label={t("oss.form.useSSL")} value={cfg.use_ssl ? ENABLED_VALUE : DISABLED_VALUE} />
          {cfg.connect_timeout ? (
            <InfoItem label={t("oss.form.connectTimeout")} value={String(cfg.connect_timeout)} mono />
          ) : null}
        </DetailGrid>
      </DetailSection>
    );
  }
  ```

- [ ] 运行(预期绿):`cd frontend && pnpm test src/components/asset/detail/__tests__/DetailInfoCards.test.tsx`(预期原有全部 + 新增 3 例全绿)。
- [ ] 类型检查:`cd frontend && npx tsc -b`。
- [ ] 提交:`✨ OSS 资产详情卡`

---

## Task 4 — 品牌图标 + 注册 `assetTypes/oss.ts` + `index.ts` + registry / options 测试

**canConnect 决策(锁定)**:对象浏览器工作区是 P3,当前**不存在** OSS 的连接目标。`AssetTree.tsx` 用 `def.canConnect` 门控双击连接与右键「连接」菜单;置 `canConnect: false` 即零副作用地抑制全部连接 UI。`connectAction` 是**必填** `"terminal" | "query"`(无第三选项),填占位 `"query"`(`canConnect:false` 下 `App.tsx handleConnectAsset` 路径不可达)。`testable: true` 独立成立——测试连接只需 section 的 `buildTestConfig` + 后端已存在的 `OSSTestConnection`/`connpool`。P3 落地浏览器后再把 `canConnect` 翻为 `true` 并在 `App.tsx` 加 `oss` 分派。

**Category 决策**:`AssetTypeCategory = "servers" | "databases" | "middleware" | "extension"` 无 "storage" 桶。新增 category 是跨切面大改(动 `options.ts` 的 `CATEGORY_ORDER`、类型选择器 UI、两处 category 测试),超出 P2。沿用 etcd 先例选 `"databases"`,与既有测试骨架一致、改动最小。

**图标决策**:选 `@iconify-icons/logos/aws-s3`(logos 集=彩色,颜色已内置,**无需**给 `ICON_COLORS` 加条目),包装成 `S3Icon`。S3 是所有这些厂商实现的协议之源,作为「S3 兼容对象存储」通用标最贴切且不需猜 hex。

**Files**
- 修改 `frontend/src/components/asset/brand-icons.tsx`
- 修改 `frontend/src/components/asset/IconPicker.tsx`
- 新建 `frontend/src/lib/assetTypes/oss.ts`
- 修改 `frontend/src/lib/assetTypes/index.ts`
- 修改 `frontend/src/lib/assetTypes/__tests__/registry.test.ts`
- 修改 `frontend/src/__tests__/assetTypeOptions.test.ts`

**Interfaces**
- Consumes:`OSSConfigSection`(Task 2)、`OSSDetailInfoCard`(Task 3)、`S3Icon`(本 Task)、`registerAssetType`(`./_register`)。
- Produces:注册表新增键 `"oss"`;`brand-icons` 新增 `export const S3Icon`。

**TDD**

- [ ] 先改测试断言(预期红,因 `oss` 尚未注册):

  1. `frontend/src/lib/assetTypes/__tests__/registry.test.ts`
     - `getBuiltinTypes returns all built-in types` 列表末尾追加 `"oss"`:
       ```ts
       expect(getBuiltinTypes().map((def) => def.type)).toEqual([
         "ssh", "database", "redis", "mongodb", "kafka", "k8s", "serial", "local", "etcd", "oss",
       ]);
       ```
     - `ssh and k8s are terminal, others are query` 追加一行:
       ```ts
       expect(getAssetType("oss")!.connectAction).toBe("query");
       ```
     - `only ssh exposes the file-manager action ...` 追加一行:
       ```ts
       expect(getAssetType("oss")!.canOpenFileManager).toBeFalsy();
       ```
     - 新增一个 describe 内 it(记录本期语义):
       ```ts
       it("oss 本期仅新建/编辑/测试(对象浏览器落地前不开连接)", () => {
         expect(getAssetType("oss")!.canConnect).toBe(false);
         expect(getAssetType("oss")!.canConnectInNewTab).toBe(false);
         expect(getAssetType("oss")!.testable).toBe(true);
       });
       ```

  2. `frontend/src/__tests__/assetTypeOptions.test.ts`
     - `returns built-in options when extensions registry is empty` 的 value 列表追加 `"oss"`:
       ```ts
       expect(values).toEqual(["ssh", "database", "redis", "mongodb", "kafka", "k8s", "serial", "local", "etcd", "oss"]);
       ```
     - `category classification` 的 `byValue` 断言加一键:
       ```ts
       etcd: "databases",
       oss: "databases",
       ```
     - `buildAssetTypeGroups` 的 databases 数组追加 `"oss"`:
       ```ts
       expect(groups[1].options.map((o) => o.value)).toEqual(["database", "redis", "mongodb", "etcd", "oss"]);
       ```
     - **重命名撞名 mock**(Key Finding #7:该文件底部用 `type: "oss"` 的**扩展** mock 测 `ext-<name>` 命名空间,与新内建 `oss` 无关但会撞名——一旦内建 `oss` 注册,`getAssetTypeOptions(extManifest).find(o=>o.value==="oss")` 会先命中内建项,破坏扩展命名空间测试)。把 `extManifest` 及两处断言里的 `oss` 改成 `filestore`:
       ```ts
       const extManifest = {
         filestore: {
           manifest: {
             name: "filestore",
             version: "1",
             icon: "Server",
             i18n: { displayName: "FileStore", description: "" },
             assetTypes: [{ type: "filestore", i18n: { name: "assetType.filestore.name" } }],
           },
         },
       };

       describe("extension option i18n namespace", () => {
         it("tags extension options with the ext-<name> namespace and treats label as an i18n key", () => {
           const extOpt = getAssetTypeOptions(extManifest as never).find((o) => o.value === "filestore")!;
           expect(extOpt.labelIsI18nKey).toBe(true);
           expect(extOpt.i18nNs).toBe("ext-filestore");
           expect(extOpt.label).toBe("assetType.filestore.name");
         });
       });

       describe("resolveAssetTypeLabel", () => {
         it("resolves built-in labels in the default namespace (no ns passed)", () => {
           const ssh = getAssetTypeOptions({}).find((o) => o.value === "ssh")!;
           const calls: Array<[string, { ns?: string } | undefined]> = [];
           const t = (k: string, o?: { ns?: string }) => {
             calls.push([k, o]);
             return `X(${k})`;
           };
           expect(resolveAssetTypeLabel(ssh, t)).toBe("X(nav.ssh)");
           expect(calls[0]).toEqual(["nav.ssh", undefined]);
         });

         it("resolves extension labels via the ext-<name> namespace", () => {
           const extOpt = getAssetTypeOptions(extManifest as never).find((o) => o.value === "filestore")!;
           const t = (k: string, o?: { ns?: string }) =>
             o?.ns === "ext-filestore" && k === "assetType.filestore.name" ? "对象存储" : k;
           expect(resolveAssetTypeLabel(extOpt, t)).toBe("对象存储");
         });
       });
       ```

- [ ] 运行(预期红):
  ```bash
  cd frontend && pnpm test src/lib/assetTypes/__tests__/registry.test.ts src/__tests__/assetTypeOptions.test.ts
  ```
  预期失败:`getBuiltinTypes` 列表 `expected [ ...9 ] to deeply equal [ ...10 with "oss" ]`;`getAssetType("oss")` 为 `undefined` 触发 `Cannot read properties of undefined`。

- [ ] 实现图标 —— `brand-icons.tsx` 两处编辑:
  - import 区(etcd 那行附近)加:
    ```ts
    import awsS3Icon from "@iconify-icons/logos/aws-s3";
    ```
  - `// ===== Databases & Middleware =====` 段末(`MemcachedIcon` 之后)加:
    ```ts
    // ===== Object Storage =====
    export const S3Icon = brandIcon(awsS3Icon);
    ```

- [ ] 实现图标接入 —— `IconPicker.tsx` 三处编辑:
  - 从 `./brand-icons` 的 import 列表加 `S3Icon,`(放 `MinioIcon,` 附近)。
  - `CATEGORIES` 的 `database` 分组 `icons` 里加(`minio: MinioIcon,` 之后):
    ```ts
    s3: S3Icon,
    ```
  - `ICON_DISPLAY_NAMES` 加(`minio: "MinIO",` 附近):
    ```ts
    s3: "Amazon S3",
    ```
  - 不动 `ICON_COLORS`(logos 集彩色内置)。

- [ ] 实现注册 `frontend/src/lib/assetTypes/oss.ts`(完整内容):

  ```ts
  import { S3Icon } from "@/components/asset/brand-icons";
  import { registerAssetType } from "./_register";
  import { OSSDetailInfoCard } from "@/components/asset/detail/OSSDetailInfoCard";
  import { OSSConfigSection } from "@/components/asset/OSSConfigSection";

  registerAssetType({
    type: "oss",
    icon: S3Icon,
    aliases: ["oss"],
    label: "nav.oss",
    category: "databases",
    // 本期只做「新建/编辑/测试连接」。对象浏览器工作区是 P3:当前无连接目标,
    // canConnect:false 直接抑制 AssetTree 的双击连接与右键「连接」菜单。
    // connectAction 是必填(仅 terminal|query),填占位 "query"(canConnect:false 下不可达);
    // P3 落地浏览器后翻为 true 并在 App.tsx handleConnectAsset 加 oss 分支。
    canConnect: false,
    canConnectInNewTab: false,
    connectAction: "query",
    DetailInfoCard: OSSDetailInfoCard,
    ConfigSection: OSSConfigSection,
    testable: true,
    // 后端 PolicyKind()=="" / DefaultPolicy()==nil —— OSS 无 policy。
    policy: undefined,
  });
  ```

- [ ] 实现 `index.ts` 副作用 import —— 在 `import "./etcd";` 之后加一行:
  ```ts
  import "./oss";
  ```

- [ ] 运行(预期绿):
  ```bash
  cd frontend && pnpm test src/lib/assetTypes/__tests__/registry.test.ts src/__tests__/assetTypeOptions.test.ts
  ```
  预期:两文件全绿(含新增 oss 断言 + 重命名后的扩展命名空间测试)。
- [ ] 类型检查 + lint:`cd frontend && npx tsc -b && pnpm lint`。
- [ ] 提交:`✨ 注册 OSS 资产类型与 S3 品牌图标`

---

## Task 5 — i18n(en + zh-CN 锁步)

**Files**
- 修改 `frontend/src/i18n/locales/en/common.json`
- 修改 `frontend/src/i18n/locales/zh-CN/common.json`

**Interfaces**:消费方是前面所有 Task 用到的 key —— `nav.oss`、`asset.tabConnection`/`asset.tabAdvanced`(**已存在**,复用不新增)、`oss.form.*`、`oss.error.required`、`asset.typeOSS`。`asset.passwordSource*` 等由 `PasswordSourceField` 泛化消费,已存在,不新增。

**说明**:测试里 `t` 被 mock 成返回 key 原样,故 i18n 缺失不影响前面 Task 的绿;本 Task 让真实应用可读。三处锚点(en/zh 行号相同):`nav.etcd`(第 33 行)、`asset.typeEtcd`(第 268 行)、顶层 `"etcd": {`(第 2502 行)。

**TDD**(JSON 文案无独立单测;用「全量 vitest 不回归 + JSON 合法 + 键完整性脚本」作为 gate)

- [ ] `en/common.json` 三处插入:
  - `nav` 内,`"etcd": "etcd",` 之后加:
    ```json
    "oss": "Object Storage",
    ```
  - `asset` 内,`"typeEtcd": "etcd",` 之后加:
    ```json
    "typeOSS": "Object Storage",
    ```
  - 顶层,在 `"etcd": {` 之前新增整段(锚点 `  "etcd": {` 唯一):
    ```json
    "oss": {
      "form": {
        "provider": "Provider",
        "providerS3": "Amazon S3",
        "providerAliyunOSS": "Alibaba Cloud OSS",
        "providerTencentCOS": "Tencent Cloud COS",
        "providerMinio": "MinIO",
        "providerS3Compat": "S3-Compatible",
        "endpoint": "Endpoint",
        "endpointPlaceholder": "host or scheme://host:port",
        "region": "Region",
        "regionPlaceholder": "e.g. us-east-1",
        "accessKeyId": "Access Key ID",
        "secretAccessKey": "Secret Access Key",
        "usePathStyle": "Path-style addressing",
        "useSSL": "Use HTTPS",
        "connectTimeout": "Connect timeout (s)"
      },
      "error": {
        "required": "Endpoint and Access Key ID are required"
      }
    },
    ```

- [ ] `zh-CN/common.json` 同三处插入(地道中文,非逐字直译):
  - `nav` 内 `"etcd": "etcd",` 之后:
    ```json
    "oss": "对象存储",
    ```
  - `asset` 内 `"typeEtcd": "etcd",` 之后:
    ```json
    "typeOSS": "对象存储",
    ```
  - 顶层 `"etcd": {` 之前:
    ```json
    "oss": {
      "form": {
        "provider": "厂商",
        "providerS3": "Amazon S3",
        "providerAliyunOSS": "阿里云 OSS",
        "providerTencentCOS": "腾讯云 COS",
        "providerMinio": "MinIO",
        "providerS3Compat": "S3 兼容",
        "endpoint": "Endpoint",
        "endpointPlaceholder": "host 或 scheme://host:port",
        "region": "地域(Region)",
        "regionPlaceholder": "如 us-east-1",
        "accessKeyId": "Access Key ID",
        "secretAccessKey": "Secret Access Key",
        "usePathStyle": "路径寻址(Path-style)",
        "useSSL": "使用 HTTPS",
        "connectTimeout": "连接超时(秒)"
      },
      "error": {
        "required": "Endpoint 和 Access Key ID 必填"
      }
    },
    ```

- [ ] 校验 JSON 合法 + 键锁步(两文件同键):
  ```bash
  cd frontend && node -e "const a=require('./src/i18n/locales/en/common.json'),b=require('./src/i18n/locales/zh-CN/common.json'); const flat=(o,p='')=>Object.entries(o).flatMap(([k,v])=>v&&typeof v==='object'?flat(v,p+k+'.'):[p+k]); const A=new Set(flat(a)),B=new Set(flat(b)); const miss=[...A].filter(k=>!B.has(k)).concat([...B].filter(k=>!A.has(k))); if(miss.length){console.error('KEY MISMATCH',miss);process.exit(1)} console.log('i18n keys locked, count=',A.size)"
  ```
  预期:`i18n keys locked, count= <N>`(无 `KEY MISMATCH`),尤其确认 `nav.oss` / `asset.typeOSS` / `oss.form.provider` … / `oss.error.required` 双侧都在。

- [ ] 全量回归:`cd frontend && pnpm test`(预期全绿,无回归)+ `cd frontend && npx tsc -b`。
- [ ] 提交:`🌐 OSS 资产 i18n(en/zh-CN)`

---

## 验收(全部 Task 完成后)

```bash
cd frontend && pnpm test        # 全量 vitest 绿
cd frontend && npx tsc -b       # 类型检查无错
cd frontend && pnpm lint        # eslint 无错
```

观察式验证(遵循 AGENTS.md「观察而非断言」):运行应用 → 资产表单类型下拉出现「对象存储」;选不同厂商观察 Endpoint/Region/Path-style 是否按预填变化;填 MinIO(本地 S3 兼容)真实凭据点「测试连接」,读 `logs/opskat.log` 与 `opskat.db` 的 `audit_logs` 确认 `OSSTestConnection` 成功/失败副作用;保存后在详情面板核对只读字段与密文打码。

## 交付物与后续

**P2 交付**:OSS 资产的**新建 / 编辑 / 测试连接**表单(连接 + 高级双 Tab、厂商智能预填、密码来源手动/托管)、注册与 S3 品牌图标、只读详情卡、en/zh-CN i18n,以及配套 golden / ref / tabs / registry / options / detail 测试。对象浏览器**不在**本期。

**P3(后续,不在本计划)**:对象浏览器工作区 —— 左 Bucket + 前缀树 / 右 面包屑 + 工具栏 + 对象列表 + 详情抽屉 + 传输队列 + 预签名分享,以及 rename/move/copy(需后端补 `CopyObject` 绑定,当前 `frontend/wailsjs/go/oss/OSS.d.ts` 仅有 `OSSListBuckets` / `OSSListObjects` / `OSSPresignGet` / `OSSPresignPut` / `OSSRemoveObject` / `OSSStatObject` / `OSSTestConnection`)。届时:把 `assetTypes/oss.ts` 的 `canConnect` 翻为 `true`,在 `App.tsx handleConnectAsset` 加 `oss` 分派(或按 k8s 走 `page` tab),扩展 `stores/tabStore.ts` 的 `QueryTabMeta.assetType` 联合 + `queryStore.ts` + `MainPanel.tsx` 分支,并新增「面包屑」原语(仓库暂无)。前缀树复用 `redisKeyTree` 分隔符逻辑,对象列表/操作/传输可 lift 自 SFTP `file-manager/`。
