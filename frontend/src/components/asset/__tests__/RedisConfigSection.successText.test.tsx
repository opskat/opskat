import { describe, it, expect, vi, beforeAll } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import i18nextFactory from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import zhCommon from "@/i18n/locales/zh-CN/common.json";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";
import { RedisProbe } from "../../../../wailsjs/go/query/Query";

// spec 要求测试连接成功行的最终展示文本(含分隔符「 · 」而非壳共享外壳的「：」),
// 这些字符在 setup.ts 的 t = key => key 全局 mock 下看不出来,必须用真实 i18next 渲染才能验证。
vi.unmock("react-i18next");
vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: vi.fn().mockResolvedValue([]),
  GetAssetPassword: vi.fn().mockResolvedValue(""),
  CancelTest: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("../../../../wailsjs/go/query/Query", () => ({
  RedisProbe: vi.fn(),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };
const testI18n = i18nextFactory.createInstance();

beforeAll(async () => {
  await testI18n.use(initReactI18next).init({
    resources: { "zh-CN": { common: zhCommon } },
    lng: "zh-CN",
    fallbackLng: "zh-CN",
    defaultNS: "common",
    interpolation: { escapeValue: false },
  });
});

async function renderRedis(editAsset: asset_entity.Asset) {
  const ref = createRef<AssetFormHandle>();
  render(
    <I18nextProvider i18n={testI18n}>
      <RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />
    </I18nextProvider>
  );
  return ref;
}

describe("RedisConfigSection 测试连接成功行文案(真实 i18n,校验最终展示文本符合 spec)", () => {
  it("集群 ok:「连接成功 · 集群 ok · M 主 R 从」", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      cluster: { state: "ok", masters: 3, replicas: 3, unreachableNodes: [] },
    } as never);
    const ref = await renderRedis(
      new asset_entity.Asset({ Type: "redis", Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}' })
    );

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successText).toBe("连接成功 · 集群 ok · 3 主 3 从");
  });

  it("集群非 ok:如实显示状态且不丢「集群」前缀,不可达后缀仍以「 · 」追加", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      cluster: { state: "fail", masters: 3, replicas: 3, unreachableNodes: ["172.18.0.11:6379"] },
    } as never);
    const ref = await renderRedis(
      new asset_entity.Asset({ Type: "redis", Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}' })
    );

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successText).toBe("连接成功 · 集群 fail · 3 主 3 从 · 种子节点可连 · 1 个节点不可达");
  });

  it("哨兵:「连接成功 · 当前主节点 host:port」", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: { authRequired: false, groups: [], masterAddr: "192.168.8.141:7102", otherSentinels: [] },
    } as never);
    const ref = await renderRedis(
      new asset_entity.Asset({
        Type: "redis",
        Config: '{"mode":"sentinel","nodes":["10.0.0.31:26379"],"master_name":"mymaster"}',
      })
    );

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successText).toBe("连接成功 · 当前主节点 192.168.8.141:7102");
  });

  it("单机:不提供 successText(壳只出通用「连接成功」)", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({ modeMismatch: false } as never);
    const ref = await renderRedis(
      new asset_entity.Asset({ Type: "redis", Config: '{"host":"127.0.0.1","port":6379}' })
    );

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successText).toBeUndefined();
    expect(result.successDetail).toBeUndefined();
  });
});
