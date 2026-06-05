# 阶段 4c:etcd 注册化 + 共享凭据抽象(useAssetCredential)实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development 逐 Task 驱动。步骤用 `- [ ]` 勾选跟踪。

**Goal:** 把 `etcd`(首个带凭据/加密/TLS/SSH 隧道的 db 族类型)迁到注册化通用 ConfigSection 路径,并顺带抽出 5 个 db 族类型共享的凭据抽象(`useAssetCredential` hook + 纯 `credentialConfig.ts`),etcd 作为首个消费者。

**Architecture:** 延续 4a/4b 的「section 自持状态 + ref 暴露 build*」契约。新增一层共享凭据子状态:`useAssetCredential(editAsset)` 自持 `{password, encryptedPassword, passwordSource, passwordCredentialId}` + 加载可选密码凭据列表,经纯函数 `resolveSaveCredential`/`resolveTestCredential` 解析出注入 config 的 `CredentialFragment`。etcd section 组合「自持的 EtcdFormState(非凭据字段)」+「凭据 hook」。保存与测试共用纯 `buildEtcdConfig(state, fragment)`,仅凭据片段与明文 4th-arg 不同。键序锁旧 **save** 分支(`endpoints→username→credential→tls…→timeouts→ssh_asset_id`);旧 test 分支键序不同但 Go struct 反序列化无关,统一到 save 序。

**Tech Stack:** React 19 forwardRef/useImperativeHandle/useState/useEffect、vitest + @testing-library/react、TypeScript strict、eslint(含 `react-refresh/only-export-components`、`prettier/prettier`)。

**迁移顺序参照:** `serial → **etcd** → redis → mongodb → database → k8s → kafka → ssh`。本计划只做 etcd;凭据抽象供后续 4 个 db 族类型复用。

**行为保持基线(逐字镜像旧 AssetForm,务必字节一致):**
- 旧 `loadEtcdConfig`(AssetForm.tsx:741-771)— 回填。
- 旧 save 分支(AssetForm.tsx:1470-1497)+ `encryptPasswordValue`(1201-1212)— 保存序列化 + 加密。
- 旧 `handleTestEtcdConnection`(1071-1106)+ `applyTestPasswordSource`(921-928)— 测试。
- 旧 endpoints 切分:`raw.split(/[\n,;]+/).map(s=>s.trim()).filter(Boolean)`(save 1471-1474 / test 1072-1075 / `etcdEndpointsList` 1620-1624 三处一致)。
- `sshTunnelId` 回填:`editAsset.sshTunnelId || cfg.ssh_asset_id || 0`(旧 754);保存写回 `sshTunnelId > 0 ? sshTunnelId : 0`(旧 1600-1602,等价 `state.sshTunnelId`)。

**与旧实现的两点等价性变更(已记录,非回归):**
1. **加密失败处理**:旧 `encryptPasswordValue` catch 后返回 `undefined` + 硬编码英文 toast `"Failed to encrypt password"`,save 静默 `return`。新版让 `ctx.encryptPassword` 的 reject 透传到 `buildConfig` → `handleSubmit` 既有 try/catch toast `e.message`(AssetForm.tsx:1356-1361)。语义等价(save 中止 + 错误 toast),且不再吞错(符合 AGENTS.md)。不为「encrypt resolve 成 undefined」这种不会发生的情况加防御。
2. **键序统一**:旧 save 与 test 凭据键插入位置不同;新版两路都用 `buildEtcdConfig`(凭据紧跟 username)。JSON 键序对后端 Go struct 反序列化无影响,golden 锁统一序。

---

## 文件结构

- **新建** `frontend/src/components/asset/credentialConfig.ts` — 纯:`CredentialState`/`CREDENTIAL_DEFAULTS`/`CredentialFragment`/`initCredentialFromConfig`/`resolveTestCredential`/`resolveSaveCredential`。
- **新建** `frontend/src/components/asset/__tests__/credentialConfig.test.ts` — 凭据纯函数 golden。
- **新建** `frontend/src/components/asset/useAssetCredential.ts` — hook:自持凭据子状态 + 加载 `ListCredentialsByType("password")` + 编辑态回填。
- **新建** `frontend/src/components/asset/EtcdConfigSection.config.ts` — 纯:`EtcdFormState`/`ETCD_DEFAULTS`/`parseEtcdEndpoints`/`buildEtcdConfig`/`parseEtcdConfig`。
- **新建** `frontend/src/components/asset/__tests__/EtcdConfigSection.config.test.ts` — etcd 序列化/回填 golden。
- **重写** `frontend/src/components/asset/EtcdConfigSection.tsx` — props 组件 → `forwardRef<AssetFormHandle, ConfigSectionProps>`,自持状态 + 凭据 hook + 校验 effect + useImperativeHandle。
- **新建** `frontend/src/components/asset/__tests__/EtcdConfigSection.test.tsx` — ref 契约(render-based)。
- **删除** `frontend/src/__tests__/EtcdConfigSection.test.tsx` — 旧 props 组件测试,被上一条取代。
- **改** `frontend/src/lib/assetTypes/etcd.ts` — 注册 `ConfigSection: EtcdConfigSection, testable: true`。
- **改** `frontend/src/components/asset/AssetForm.tsx` — 删 etcd 专属 state/接口/load/reset/test-handler/save-branch/render-block + import;清 4 条遗留负表里的 etcd 项。

---

## Task 1:共享凭据纯函数 + golden

**Files:**
- Create: `frontend/src/components/asset/credentialConfig.ts`
- Test: `frontend/src/components/asset/__tests__/credentialConfig.test.ts`

- [ ] **Step 1: 写失败测试** `credentialConfig.test.ts`

```ts
import { describe, it, expect, vi } from "vitest";
import {
  initCredentialFromConfig,
  resolveTestCredential,
  resolveSaveCredential,
  CREDENTIAL_DEFAULTS,
} from "@/components/asset/credentialConfig";

describe("initCredentialFromConfig (锁旧 load*Config credential 分支)", () => {
  it("credential_id 存在 → managed", () => {
    expect(initCredentialFromConfig({ credential_id: 7, password: "x" })).toEqual({
      password: "",
      encryptedPassword: "",
      passwordSource: "managed",
      passwordCredentialId: 7,
    });
  });
  it("无 credential_id → inline + 既有密文", () => {
    expect(initCredentialFromConfig({ password: "ENC" })).toEqual({
      password: "",
      encryptedPassword: "ENC",
      passwordSource: "inline",
      passwordCredentialId: 0,
    });
  });
  it("空 config → 默认", () => {
    expect(initCredentialFromConfig({})).toEqual(CREDENTIAL_DEFAULTS);
  });
});

describe("resolveTestCredential (锁旧 applyTestPasswordSource)", () => {
  it("managed + credId>0 → credential_id", () => {
    expect(
      resolveTestCredential({ password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 9 })
    ).toEqual({ credential_id: 9 });
  });
  it("inline 未改但有既有密文 → password=既有密文", () => {
    expect(
      resolveTestCredential({ password: "", encryptedPassword: "ENC", passwordSource: "inline", passwordCredentialId: 0 })
    ).toEqual({ password: "ENC" });
  });
  it("inline 输入了明文 → 空片段(明文走 4th-arg)", () => {
    expect(
      resolveTestCredential({ password: "plain", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 })
    ).toEqual({});
  });
  it("managed 但 credId=0 → 退回 inline 规则", () => {
    expect(
      resolveTestCredential({ password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 0 })
    ).toEqual({});
  });
});

describe("resolveSaveCredential (锁旧 save 分支 + encryptPasswordValue)", () => {
  it("managed + credId>0 → credential_id,不加密", async () => {
    const encrypt = vi.fn();
    expect(
      await resolveSaveCredential(
        { password: "p", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: 4 },
        encrypt
      )
    ).toEqual({ credential_id: 4 });
    expect(encrypt).not.toHaveBeenCalled();
  });
  it("inline 有明文 → 加密后 password", async () => {
    const encrypt = vi.fn(async (p: string) => `enc(${p})`);
    expect(
      await resolveSaveCredential(
        { password: "secret", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        encrypt
      )
    ).toEqual({ password: "enc(secret)" });
  });
  it("inline 无明文但有既有密文 → 沿用既有密文", async () => {
    const encrypt = vi.fn();
    expect(
      await resolveSaveCredential(
        { password: "", encryptedPassword: "OLD", passwordSource: "inline", passwordCredentialId: 0 },
        encrypt
      )
    ).toEqual({ password: "OLD" });
    expect(encrypt).not.toHaveBeenCalled();
  });
  it("inline 无明文无密文 → 空片段(不写 password 键)", async () => {
    expect(
      await resolveSaveCredential(
        { password: "", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        vi.fn()
      )
    ).toEqual({});
  });
  it("加密 reject → 透传(save 中止由上层处理)", async () => {
    await expect(
      resolveSaveCredential(
        { password: "x", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
        async () => {
          throw new Error("boom");
        }
      )
    ).rejects.toThrow("boom");
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/credentialConfig.test.ts`
Expected: FAIL(模块不存在)

- [ ] **Step 3: 实现** `credentialConfig.ts`

```ts
/** 凭据子状态:5 个 db 族类型(etcd/redis/mongodb/database/kafka)共享;section 经 useAssetCredential 持有。 */
export interface CredentialState {
  /** 明文(用户输入或揭示既有密码后解密回填);测试走 4th-arg,保存前加密。 */
  password: string;
  /** 编辑态既有密文;用户未改动时沿用。 */
  encryptedPassword: string;
  passwordSource: "inline" | "managed";
  passwordCredentialId: number;
}

export const CREDENTIAL_DEFAULTS: CredentialState = {
  password: "",
  encryptedPassword: "",
  passwordSource: "inline",
  passwordCredentialId: 0,
};

/** 注入 config 的凭据片段(credential_id 或 password 二选一,或都无)。 */
export type CredentialFragment = { credential_id?: number; password?: string };

/** 编辑态回填:镜像旧 load*Config credential 分支(credential_id→managed;否则 inline+既有密文)。 */
export function initCredentialFromConfig(cfg: { credential_id?: number; password?: string }): CredentialState {
  if (cfg.credential_id) {
    return { password: "", encryptedPassword: "", passwordSource: "managed", passwordCredentialId: cfg.credential_id };
  }
  return { password: "", encryptedPassword: cfg.password || "", passwordSource: "inline", passwordCredentialId: 0 };
}

/** 测试连接片段:镜像旧 applyTestPasswordSource(managed→credential_id;inline 未改但有既有密文→password)。 */
export function resolveTestCredential(s: CredentialState): CredentialFragment {
  if (s.passwordSource === "managed" && s.passwordCredentialId > 0) {
    return { credential_id: s.passwordCredentialId };
  }
  if (!s.password && s.encryptedPassword) {
    return { password: s.encryptedPassword };
  }
  return {};
}

/** 保存片段:镜像旧 save 分支 + encryptPasswordValue(managed→credential_id;否则明文加密 / 沿用既有密文)。
 *  加密失败由 encrypt 的 reject 透传给调用方(buildConfig→handleSubmit 统一 toast),不在此吞错。 */
export async function resolveSaveCredential(
  s: CredentialState,
  encrypt: (plain: string) => Promise<string>
): Promise<CredentialFragment> {
  if (s.passwordSource === "managed" && s.passwordCredentialId > 0) {
    return { credential_id: s.passwordCredentialId };
  }
  const encrypted = s.password ? await encrypt(s.password) : s.encryptedPassword;
  return encrypted ? { password: encrypted } : {};
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/credentialConfig.test.ts`
Expected: PASS(全部)

- [ ] **Step 5: tsc + eslint**

Run: `cd frontend && npx tsc --noEmit && npx eslint src/components/asset/credentialConfig.ts src/components/asset/__tests__/credentialConfig.test.ts`
Expected: 0 错误

- [ ] **Step 6: commit**

```bash
git add frontend/src/components/asset/credentialConfig.ts frontend/src/components/asset/__tests__/credentialConfig.test.ts
git commit -m "✅ 抽 db 族共享凭据纯函数 credentialConfig(init/resolveTest/resolveSave)+ golden #130"
```

---

## Task 2:etcd 配置纯函数 + golden

**Files:**
- Create: `frontend/src/components/asset/EtcdConfigSection.config.ts`
- Test: `frontend/src/components/asset/__tests__/EtcdConfigSection.config.test.ts`

- [ ] **Step 1: 写失败测试** `EtcdConfigSection.config.test.ts`

```ts
import { describe, it, expect } from "vitest";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "@/components/asset/EtcdConfigSection.config";

const FULL: EtcdFormState = {
  endpoints: "10.0.0.1:2379\n10.0.0.2:2379",
  username: "admin",
  tls: true,
  tlsInsecure: true,
  tlsServerName: "etcd.x",
  tlsCAFile: "/ca.pem",
  tlsCertFile: "/c.crt",
  tlsKeyFile: "/c.key",
  dialTimeoutSeconds: 5,
  commandTimeoutSeconds: 10,
  sshTunnelId: 3,
};

describe("parseEtcdEndpoints (锁旧 split(/[\\n,;]+/))", () => {
  it("混合换行/逗号/分号 + trim + 去空", () => {
    expect(parseEtcdEndpoints(" a:2379 \n b:2379 , c:2379 ; ")).toEqual(["a:2379", "b:2379", "c:2379"]);
  });
  it("全空 → 空数组", () => {
    expect(parseEtcdEndpoints("  \n ; , ")).toEqual([]);
  });
});

describe("buildEtcdConfig (锁旧 save 序:endpoints→username→credential→tls…→timeouts→ssh_asset_id)", () => {
  it("全字段 + 既加密 password", () => {
    expect(buildEtcdConfig(FULL, { password: "ENC" })).toBe(
      '{"endpoints":["10.0.0.1:2379","10.0.0.2:2379"],"username":"admin","password":"ENC",' +
        '"tls":true,"tls_insecure":true,"tls_server_name":"etcd.x","tls_ca_file":"/ca.pem",' +
        '"tls_cert_file":"/c.crt","tls_key_file":"/c.key","dial_timeout_seconds":5,' +
        '"command_timeout_seconds":10,"ssh_asset_id":3}'
    );
  });
  it("managed 凭据 → credential_id 紧跟 username", () => {
    expect(buildEtcdConfig(FULL, { credential_id: 7 })).toContain('"username":"admin","credential_id":7,"tls":true');
  });
  it("最小态(仅端点,默认超时仍写)", () => {
    expect(buildEtcdConfig({ ...ETCD_DEFAULTS, endpoints: "x:2379" }, {})).toBe(
      '{"endpoints":["x:2379"],"dial_timeout_seconds":5,"command_timeout_seconds":10}'
    );
  });
  it("tls=false 时省略全部 tls_* 子键", () => {
    const s = { ...FULL, tls: false };
    const json = buildEtcdConfig(s, {});
    expect(json).not.toContain("tls_insecure");
    expect(json).not.toContain("tls_server_name");
    expect(json).not.toContain('"tls":');
  });
  it("空片段不写 password / credential_id 键", () => {
    const json = buildEtcdConfig({ ...ETCD_DEFAULTS, endpoints: "x:2379" }, {});
    expect(json).not.toContain("password");
    expect(json).not.toContain("credential_id");
  });
  it("超时为 0 时省略对应键", () => {
    const json = buildEtcdConfig(
      { ...ETCD_DEFAULTS, endpoints: "x:2379", dialTimeoutSeconds: 0, commandTimeoutSeconds: 0 },
      {}
    );
    expect(json).toBe('{"endpoints":["x:2379"]}');
  });
});

describe("parseEtcdConfig (锁旧 loadEtcdConfig 非凭据字段)", () => {
  it("全字段回填(ssh_asset_id 仅来自 config)", () => {
    expect(
      parseEtcdConfig(
        '{"endpoints":["a:2379","b:2379"],"username":"u","tls":true,"tls_insecure":true,' +
          '"tls_server_name":"sn","tls_ca_file":"/ca","tls_cert_file":"/cc","tls_key_file":"/ck",' +
          '"dial_timeout_seconds":8,"command_timeout_seconds":20,"ssh_asset_id":5}'
      )
    ).toEqual({
      endpoints: "a:2379\nb:2379",
      username: "u",
      tls: true,
      tlsInsecure: true,
      tlsServerName: "sn",
      tlsCAFile: "/ca",
      tlsCertFile: "/cc",
      tlsKeyFile: "/ck",
      dialTimeoutSeconds: 8,
      commandTimeoutSeconds: 20,
      sshTunnelId: 5,
    });
  });
  it("缺字段用默认", () => {
    expect(parseEtcdConfig("{}")).toEqual(ETCD_DEFAULTS);
  });
  it("非法 JSON 回退默认", () => {
    expect(parseEtcdConfig("nope")).toEqual(ETCD_DEFAULTS);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/EtcdConfigSection.config.test.ts`
Expected: FAIL(模块不存在)

- [ ] **Step 3: 实现** `EtcdConfigSection.config.ts`

```ts
import type { CredentialFragment } from "./credentialConfig";

export interface EtcdFormState {
  endpoints: string;
  username: string;
  tls: boolean;
  tlsInsecure: boolean;
  tlsServerName: string;
  tlsCAFile: string;
  tlsCertFile: string;
  tlsKeyFile: string;
  dialTimeoutSeconds: number;
  commandTimeoutSeconds: number;
  sshTunnelId: number;
}

export const ETCD_DEFAULTS: EtcdFormState = {
  endpoints: "",
  username: "",
  tls: false,
  tlsInsecure: false,
  tlsServerName: "",
  tlsCAFile: "",
  tlsCertFile: "",
  tlsKeyFile: "",
  dialTimeoutSeconds: 5,
  commandTimeoutSeconds: 10,
  sshTunnelId: 0,
};

interface EtcdConfig {
  endpoints?: string[];
  username?: string;
  credential_id?: number;
  password?: string;
  tls?: boolean;
  tls_insecure?: boolean;
  tls_server_name?: string;
  tls_ca_file?: string;
  tls_cert_file?: string;
  tls_key_file?: string;
  dial_timeout_seconds?: number;
  command_timeout_seconds?: number;
  ssh_asset_id?: number;
}

/** 端点文本→数组(镜像旧 save/test/etcdEndpointsList 三处一致切分)。 */
export function parseEtcdEndpoints(raw: string): string[] {
  return raw
    .split(/[\n,;]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** 保存/测试共用序列化(键序锁旧 save 分支)。cred 由 resolveSave/TestCredential 预解析。 */
export function buildEtcdConfig(state: EtcdFormState, cred: CredentialFragment): string {
  const cfg: EtcdConfig = { endpoints: parseEtcdEndpoints(state.endpoints) };
  if (state.username) cfg.username = state.username;
  if (cred.credential_id) cfg.credential_id = cred.credential_id;
  else if (cred.password) cfg.password = cred.password;
  if (state.tls) cfg.tls = true;
  if (state.tls && state.tlsInsecure) cfg.tls_insecure = true;
  if (state.tls && state.tlsServerName) cfg.tls_server_name = state.tlsServerName;
  if (state.tls && state.tlsCAFile) cfg.tls_ca_file = state.tlsCAFile;
  if (state.tls && state.tlsCertFile) cfg.tls_cert_file = state.tlsCertFile;
  if (state.tls && state.tlsKeyFile) cfg.tls_key_file = state.tlsKeyFile;
  if (state.dialTimeoutSeconds > 0) cfg.dial_timeout_seconds = state.dialTimeoutSeconds;
  if (state.commandTimeoutSeconds > 0) cfg.command_timeout_seconds = state.commandTimeoutSeconds;
  if (state.sshTunnelId > 0) cfg.ssh_asset_id = state.sshTunnelId;
  return JSON.stringify(cfg);
}

/** 编辑态回填(镜像旧 loadEtcdConfig 非凭据字段;ssh_asset_id 仅取 config,asset.sshTunnelId 由 section 覆盖)。 */
export function parseEtcdConfig(configJSON: string): EtcdFormState {
  try {
    const cfg: EtcdConfig = JSON.parse(configJSON || "{}");
    return {
      endpoints: (cfg.endpoints || []).join("\n"),
      username: cfg.username || "",
      tls: cfg.tls || false,
      tlsInsecure: cfg.tls_insecure || false,
      tlsServerName: cfg.tls_server_name || "",
      tlsCAFile: cfg.tls_ca_file || "",
      tlsCertFile: cfg.tls_cert_file || "",
      tlsKeyFile: cfg.tls_key_file || "",
      dialTimeoutSeconds: cfg.dial_timeout_seconds || 5,
      commandTimeoutSeconds: cfg.command_timeout_seconds || 10,
      sshTunnelId: cfg.ssh_asset_id || 0,
    };
  } catch {
    return { ...ETCD_DEFAULTS };
  }
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd frontend && npx vitest run src/components/asset/__tests__/EtcdConfigSection.config.test.ts`
Expected: PASS(全部)

- [ ] **Step 5: tsc + eslint**

Run: `cd frontend && npx tsc --noEmit && npx eslint src/components/asset/EtcdConfigSection.config.ts src/components/asset/__tests__/EtcdConfigSection.config.test.ts`
Expected: 0 错误

- [ ] **Step 6: commit**

```bash
git add frontend/src/components/asset/EtcdConfigSection.config.ts frontend/src/components/asset/__tests__/EtcdConfigSection.config.test.ts
git commit -m "✅ 抽 etcd 配置纯函数 buildEtcdConfig/parseEtcdConfig + golden(键序锁旧 save) #130"
```

---

## Task 3:useAssetCredential hook + EtcdConfigSection 重写 + 注册 + 壳迁移(原子)

> 这是不可再分的原子提交:组件签名从 props 改成 forwardRef,壳里旧 `<EtcdConfigSection {...props}/>` 调用同时失效,必须与注册 + 壳删除同提交才编译通过 + 全绿(对应 4b 的 `9dbe8472`)。

**Files:**
- Create: `frontend/src/components/asset/useAssetCredential.ts`
- Rewrite: `frontend/src/components/asset/EtcdConfigSection.tsx`
- Create: `frontend/src/components/asset/__tests__/EtcdConfigSection.test.tsx`
- Delete: `frontend/src/__tests__/EtcdConfigSection.test.tsx`
- Modify: `frontend/src/lib/assetTypes/etcd.ts`
- Modify: `frontend/src/components/asset/AssetForm.tsx`

- [ ] **Step 1: 写 hook** `useAssetCredential.ts`

```ts
import { useEffect, useState } from "react";
import { ListCredentialsByType } from "../../../wailsjs/go/system/System";
import { asset_entity, credential_entity } from "../../../wailsjs/go/models";
import { CREDENTIAL_DEFAULTS, initCredentialFromConfig, type CredentialState } from "./credentialConfig";

export interface UseAssetCredential {
  value: CredentialState;
  managedPasswords: credential_entity.Credential[];
  setPassword: (v: string) => void;
  setPasswordSource: (v: "inline" | "managed") => void;
  setPasswordCredentialId: (v: number) => void;
}

/** db 族 section 共享:自持凭据子状态 + 加载可选密码凭据列表 + 编辑态回填。 */
export function useAssetCredential(editAsset?: asset_entity.Asset): UseAssetCredential {
  const [value, setValue] = useState<CredentialState>(() => {
    if (!editAsset) return { ...CREDENTIAL_DEFAULTS };
    try {
      return initCredentialFromConfig(JSON.parse(editAsset.Config || "{}"));
    } catch {
      return { ...CREDENTIAL_DEFAULTS };
    }
  });
  const [managedPasswords, setManagedPasswords] = useState<credential_entity.Credential[]>([]);

  useEffect(() => {
    ListCredentialsByType("password")
      .then((p) => setManagedPasswords(p || []))
      .catch(() => setManagedPasswords([]));
  }, []);

  const patch = (p: Partial<CredentialState>) => setValue((s) => ({ ...s, ...p }));
  return {
    value,
    managedPasswords,
    setPassword: (v) => patch({ password: v }),
    setPasswordSource: (v) => patch({ passwordSource: v }),
    setPasswordCredentialId: (v) => patch({ passwordCredentialId: v }),
  };
}
```

- [ ] **Step 2: 重写** `EtcdConfigSection.tsx`(forwardRef;render 体逐字搬旧 JSX,只把 `prop` 换成 `state.X` / `patch`,凭据换成 `cred.*`)

```tsx
import { forwardRef, useEffect, useImperativeHandle, useState } from "react";
import { useTranslation } from "react-i18next";
import { Input, Label, Switch, Textarea } from "@opskat/ui";
import { AssetSelect } from "@/components/asset/AssetSelect";
import { PasswordSourceField } from "@/components/asset/PasswordSourceField";
import type { AssetFormHandle, ConfigSectionProps } from "@/lib/assetTypes/formContract";
import { useAssetCredential } from "./useAssetCredential";
import { resolveSaveCredential, resolveTestCredential } from "./credentialConfig";
import {
  buildEtcdConfig,
  parseEtcdConfig,
  parseEtcdEndpoints,
  ETCD_DEFAULTS,
  type EtcdFormState,
} from "./EtcdConfigSection.config";

export const EtcdConfigSection = forwardRef<AssetFormHandle, ConfigSectionProps>(function EtcdConfigSection(
  { editAsset, onValidityChange },
  ref
) {
  const { t } = useTranslation();
  const [state, setState] = useState<EtcdFormState>(() => {
    if (!editAsset) return { ...ETCD_DEFAULTS };
    const parsed = parseEtcdConfig(editAsset.Config);
    // sshTunnelId 优先 asset 顶层字段(镜像旧 asset.sshTunnelId || cfg.ssh_asset_id || 0)。
    return { ...parsed, sshTunnelId: editAsset.sshTunnelId || parsed.sshTunnelId };
  });
  const patch = (p: Partial<EtcdFormState>) => setState((s) => ({ ...s, ...p }));
  const cred = useAssetCredential(editAsset);

  // 端点为保存/测试共同必填;上报反应式校验(onValidityChange 为壳 setState,身份稳定)。
  useEffect(() => {
    const ok = parseEtcdEndpoints(state.endpoints).length > 0;
    onValidityChange({ canTest: ok, canSave: ok, saveDisabledReason: ok ? "" : "etcd.error.endpointsRequired" });
  }, [state.endpoints, onValidityChange]);

  useImperativeHandle(
    ref,
    () => ({
      buildConfig: async (ctx) => {
        const frag = await resolveSaveCredential(cred.value, ctx.encryptPassword);
        return { configJSON: buildEtcdConfig(state, frag), sshTunnelId: state.sshTunnelId };
      },
      buildTestConfig: async () => ({
        assetType: "etcd",
        configJSON: buildEtcdConfig(state, resolveTestCredential(cred.value)),
        password: cred.value.password,
      }),
    }),
    [state, cred.value]
  );

  return (
    <>
      {/* Connection & Auth(单视觉块) */}
      <div className="grid gap-3 border rounded-lg p-3">
        <div className="grid gap-2">
          <Label>{t("etcd.form.endpoints")}</Label>
          <Textarea
            value={state.endpoints}
            onChange={(e) => patch({ endpoints: e.target.value })}
            rows={3}
            className="font-mono text-sm"
            placeholder={"10.0.0.1:2379\n10.0.0.2:2379"}
          />
          <p className="text-xs text-muted-foreground">{t("etcd.form.endpointsHint")}</p>
        </div>

        <div className="grid gap-2">
          <Label>{t("asset.username")}</Label>
          <Input value={state.username} onChange={(e) => patch({ username: e.target.value })} />
        </div>

        <PasswordSourceField
          source={cred.value.passwordSource}
          onSourceChange={cred.setPasswordSource}
          password={cred.value.password}
          onPasswordChange={cred.setPassword}
          credentialId={cred.value.passwordCredentialId}
          onCredentialIdChange={cred.setPasswordCredentialId}
          managedPasswords={cred.managedPasswords}
          hasExistingPassword={!!cred.value.encryptedPassword}
          editAssetId={editAsset?.ID}
          onUsernameChange={(v) => patch({ username: v })}
        />
      </div>

      {/* TLS */}
      <div className="flex items-center justify-between">
        <Label>{t("asset.tls")}</Label>
        <Switch checked={state.tls} onCheckedChange={(v) => patch({ tls: v })} />
      </div>

      {state.tls && (
        <>
          <div className="flex items-center justify-between">
            <Label>{t("etcd.form.tlsInsecure")}</Label>
            <Switch checked={state.tlsInsecure} onCheckedChange={(v) => patch({ tlsInsecure: v })} />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsServerName")}</Label>
            <Input
              value={state.tlsServerName}
              onChange={(e) => patch({ tlsServerName: e.target.value })}
              placeholder="etcd.example.com"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCAFile")}</Label>
            <Input
              value={state.tlsCAFile}
              onChange={(e) => patch({ tlsCAFile: e.target.value })}
              placeholder="/path/to/ca.pem"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsCertFile")}</Label>
            <Input
              value={state.tlsCertFile}
              onChange={(e) => patch({ tlsCertFile: e.target.value })}
              placeholder="/path/to/client.crt"
            />
          </div>

          <div className="grid gap-2">
            <Label>{t("etcd.form.tlsKeyFile")}</Label>
            <Input
              value={state.tlsKeyFile}
              onChange={(e) => patch({ tlsKeyFile: e.target.value })}
              placeholder="/path/to/client.key"
            />
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-3">
        <div className="grid gap-2">
          <Label>{t("etcd.form.dialTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.dialTimeoutSeconds}
            onChange={(e) => patch({ dialTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
        <div className="grid gap-2">
          <Label>{t("etcd.form.commandTimeout")}</Label>
          <Input
            className="[&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none"
            type="number"
            min={0}
            value={state.commandTimeoutSeconds}
            onChange={(e) => patch({ commandTimeoutSeconds: Math.max(0, Number(e.target.value) || 0) })}
          />
        </div>
      </div>

      <div className="grid gap-2">
        <Label>{t("asset.sshTunnel")}</Label>
        <AssetSelect
          value={state.sshTunnelId}
          onValueChange={(v) => patch({ sshTunnelId: v })}
          filterType="ssh"
          placeholder={t("asset.sshTunnelNone")}
        />
      </div>
    </>
  );
});
```

- [ ] **Step 3: 注册** `frontend/src/lib/assetTypes/etcd.ts` — 顶部加 import,registerAssetType 里加两行(参照 serial.ts):

```ts
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";
```
并在 `DetailInfoCard: EtcdDetailInfoCard,` 之后插入:
```ts
  ConfigSection: EtcdConfigSection,
  testable: true,
```

- [ ] **Step 4: 壳迁移** `AssetForm.tsx` — 删除以下 etcd 专属代码(只删,不动其他类型)。**注册后这些都已死/或对编译有依赖,必须在本原子提交一并删除,否则编译断**:
  - `import { EtcdConfigSection } ...`(壳不再直接用,改由 etcd.ts 引)。
  - `interface EtcdConfig {...}`(122-136)。
  - etcd state 9 个 `useState`(392-401)。
  - load 派发分支 `else if (editType === "etcd") loadEtcdConfig(editAsset);`(494-495)。
  - `resetEtcdFields()` 调用(520)与函数定义(868-879)。
  - `loadEtcdConfig`(741-771)。
  - `handleTestEtcdConnection`(1071-1106)。
  - save 分支 `else if (assetType === "etcd") {...}`(1470-1497)— 引用 etcd state,必须同删。
  - **`etcdEndpointsList()` 辅助(1620-1624)— 闭包引用已删的 `etcdEndpoints` state,必须同删。** 连同其两处调用:
    - `isTestConnectionDisabled` 里 `: assetType === "etcd" ? etcdEndpointsList().length === 0` 这层三元(1634-1635),else 上提。
    - `saveDisabledReason` 里 `: assetType === "etcd" && etcdEndpointsList().length === 0 ? "etcd.error.endpointsRequired"` 这层(1660-1661)。
  - **`handleRunTestConnection` 里 `: assetType === "etcd" ? handleTestEtcdConnection`(1675-1676)— 引用已删的 `handleTestEtcdConnection`,必须同删。**
  - render 块 `{assetType === "etcd" && (<EtcdConfigSection .../>)}`(1942-1976)。

  注意:`resetSharedFields("etcd")`/`resetEtcdFields()` 在 `loadEtcdConfig` 的 catch 里;整个 `loadEtcdConfig` 删除后该引用一并消失。保留 `DEFAULT_PORTS.etcd` / `DEFAULT_ICONS.etcd`(`handleTypeChange` 仍用)、`AssetType` union 里的 `| "etcd"`、与 `applyTestPasswordSource`/`encryptPasswordValue`(其它 db 类型仍用)。Task 4 只清剩下 3 个**无编译依赖**的字符串比较死项。

- [ ] **Step 5: 写 ref 契约测试** `__tests__/EtcdConfigSection.test.tsx`

```tsx
import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { EtcdConfigSection } from "@/components/asset/EtcdConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: () => Promise.resolve([]),
  GetAssetPassword: () => Promise.resolve(""),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

describe("EtcdConfigSection ref 契约", () => {
  it("编辑态(inline 既有密文):buildConfig 沿用密文 + ssh_asset_id;buildTestConfig 同形,password 空", async () => {
    const editAsset = new asset_entity.Asset({
      Type: "etcd",
      Config:
        '{"endpoints":["a:2379","b:2379"],"username":"u","password":"OLD","tls":true,' +
        '"tls_insecure":true,"dial_timeout_seconds":5,"command_timeout_seconds":10,"ssh_asset_id":9}',
    });
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={() => {}} />);
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({
      configJSON:
        '{"endpoints":["a:2379","b:2379"],"username":"u","password":"OLD","tls":true,' +
        '"tls_insecure":true,"dial_timeout_seconds":5,"command_timeout_seconds":10,"ssh_asset_id":9}',
      sshTunnelId: 9,
    });
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "etcd", configJSON: built.configJSON, password: "" });
  });

  it("创建态(无端点):上报 canSave/canTest=false + etcd.error.endpointsRequired", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({
      canTest: false,
      canSave: false,
      saveDisabledReason: "etcd.error.endpointsRequired",
    });
  });

  it("编辑态(有端点):上报 canSave/canTest=true,无 reason", () => {
    const editAsset = new asset_entity.Asset({ Type: "etcd", Config: '{"endpoints":["a:2379"]}' });
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<EtcdConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
```

- [ ] **Step 6: 删旧测试**

```bash
git rm frontend/src/__tests__/EtcdConfigSection.test.tsx
```

- [ ] **Step 7: 全量校验**

Run:
```bash
cd frontend && npx tsc --noEmit \
  && npx vitest run src/components/asset/__tests__/EtcdConfigSection.test.tsx src/components/asset/__tests__/EtcdConfigSection.config.test.ts src/components/asset/__tests__/credentialConfig.test.ts \
  && npx eslint src/components/asset/EtcdConfigSection.tsx src/components/asset/useAssetCredential.ts src/lib/assetTypes/etcd.ts src/components/asset/AssetForm.tsx src/components/asset/__tests__/EtcdConfigSection.test.tsx
```
Expected: tsc 0、vitest 全 PASS、eslint 0。再跑全量 `npx vitest run` 确认无连带回归。

- [ ] **Step 8: commit**

```bash
git add -A
git commit -m "♻️ 迁移 etcd 注册化(forwardRef section + useAssetCredential 共享凭据 hook) #130"
```

---

## Task 4:壳清理 etcd 剩余死项(无编译依赖的字符串比较)

> etcd 注册后 `sectionDef?.ConfigSection` 为真,以下 3 处 etcd 字符串比较已不可达(三元先走 ConfigSection 分支)。它们**不引用任何已删的 state/函数**,纯删死码,与 4b 的 `6d593b4d` 对称。(其余有编译依赖的死分支已在 Task 3 一并删除。)

**Files:**
- Modify: `frontend/src/components/asset/AssetForm.tsx`

- [ ] **Step 1: 删 etcd 死项(3 处)**
  - `isTestableAssetType` 负支去掉 `|| assetType === "etcd"`。
  - extension guard 去掉 `assetType !== "etcd" &&`。
  - `handleTypeChange` 去掉 `if (newType === "etcd") setHost("");`(etcd 现自持状态,清共享 host 无意义)。

- [ ] **Step 2: 全量校验**

Run:
```bash
cd frontend && npx tsc --noEmit && npx vitest run && npx eslint src/components/asset/AssetForm.tsx
```
Expected: tsc 0、vitest 全 PASS、eslint 0。grep `AssetForm.tsx` 内 `etcd`/`Etcd` 残留应**仅剩** 3 处合法引用:`DEFAULT_PORTS.etcd`、`DEFAULT_ICONS.etcd`、`AssetType` union 的 `| "etcd"`(均在最终 stage-4 清共享 state 时移除)。无任何 etcd 配置/校验/测试逻辑残留。

- [ ] **Step 3: commit**

```bash
git add frontend/src/components/asset/AssetForm.tsx
git commit -m "♻️ 去 AssetForm 里 etcd 遗留负表死分支 + handleTypeChange 死行(承 etcd 迁移) #130"
```

---

## Self-Review(写完计划复核)

- **Spec 覆盖**:load/save/test 三段 + endpoints 切分 + sshTunnelId 优先级 + 凭据 managed/inline/既有密文 + TLS 子键 + 超时省略 —— 均有对应 golden / ref 测试。✅
- **Placeholder**:无 TBD/TODO;每步含完整代码或精确删除位点。✅
- **类型一致**:`CredentialState`/`CredentialFragment`/`EtcdFormState`/`AssetFormHandle`/`ConfigSectionProps` 跨 Task 命名一致;`buildEtcdConfig` 返回 string(与 buildSerialConfig 一致)。✅
- **行号漂移提醒**:Task 3/4 的行号基于当前 `AssetForm.tsx`(2114 行);实现时以「符号 + 上下文」定位为准,行号仅参考(Task 3 删除会使 Task 4 行号前移)。
