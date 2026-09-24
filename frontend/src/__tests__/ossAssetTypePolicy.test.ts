import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { getAssetType } from "@/lib/assetTypes";

function goSource(relative: string): string {
  return fs.readFileSync(path.resolve(process.cwd(), relative), "utf8");
}

/** 后端 policy 种类词表（`PolicyKind*` 常量，见 registry.go 注释：资产轴与 policy 轴的唯一映射目标）。 */
function goPolicyKinds(): string[] {
  const source = goSource("../internal/model/entity/policy/registry.go");
  return [...source.matchAll(/PolicyKind[A-Za-z0-9]+\s*=\s*"([^"]+)"/g)].map((m) => m[1]);
}

/** Go `OSSPolicy` 结构体的 json tag；groups 由 AssetDetail 单独管理，不属于策略编辑区的字段。 */
function goOSSPolicyFieldNames(): string[] {
  const source = goSource("../internal/model/entity/policy/policy.go");
  const body = /type OSSPolicy struct \{([\s\S]*?)\n\}/.exec(source)?.[1] ?? "";
  return [...body.matchAll(/json:"([^",]+)/g)].map((m) => m[1]).filter((name) => name !== "groups");
}

function ossPolicyDef() {
  return getAssetType("oss")?.policy;
}

describe("oss asset type policy definition", () => {
  it("registers a policy definition so the asset form's policy editor renders for oss", () => {
    // AssetDetail.tsx 只在 `def.policy` 为真时渲染策略卡片（`if (!def?.policy) return null;`）。
    // oss 曾注册 `policy: undefined`，资产详情里因此完全没有策略编辑区。
    expect(ossPolicyDef()).toBeDefined();
  });

  it("uses a policyType the backend policy-kind vocabulary knows", () => {
    // policyType 同时是 CommandPolicyCard 传给 PolicyGroupManager 的 tab key 与
    // CreatePolicyGroup / ListPolicyGroups 的 policyType 参数，必须逐字命中后端的种类词表，
    // 否则 OSS 权限组建得出来却查不回去。两侧是独立来源，任一侧改名即红。
    expect(goPolicyKinds()).toContain(ossPolicyDef()?.policyType);
  });

  it("edits exactly the fields the Go OSSPolicy serializes", () => {
    // AssetDetail 用 fields[].key 拼出存进 CmdPolicy 的 JSON；键名与 Go 的 json tag 一旦漂移，
    // 策略会「保存成功」但后端解出空策略 —— 没有运行时错误可看，只有这条能发现。
    expect(
      ossPolicyDef()
        ?.fields.map((f) => f.key)
        .sort()
    ).toEqual(goOSSPolicyFieldNames().sort());
  });
});
