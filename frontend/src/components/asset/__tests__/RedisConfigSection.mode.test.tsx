import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createRef, StrictMode } from "react";
import { RedisConfigSection } from "@/components/asset/RedisConfigSection";
import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";
import { asset_entity, redis_svc } from "../../../../wailsjs/go/models";
import { CancelTest } from "../../../../wailsjs/go/system/System";
import { RedisProbe } from "../../../../wailsjs/go/query/Query";

vi.mock("../../../../wailsjs/go/system/System", () => ({
  ListCredentialsByType: vi.fn().mockResolvedValue([]),
  GetAssetPassword: vi.fn().mockResolvedValue(""),
  CancelTest: vi.fn().mockResolvedValue(undefined),
}));
vi.mock("../../../../wailsjs/go/query/Query", () => ({
  RedisProbe: vi.fn(),
}));

const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

function lastValidity(spy: ReturnType<typeof vi.fn>): SectionValidity {
  return spy.mock.calls.at(-1)![0] as SectionValidity;
}

beforeEach(() => {
  vi.mocked(RedisProbe).mockReset();
  vi.mocked(CancelTest)
    .mockReset()
    .mockResolvedValue(undefined as never);
});

describe("RedisConfigSection 部署模式字段显隐 + 必填校验", () => {
  it("集群模式:显示种子节点、隐藏数据库字段;缺节点时 saveDisabledReason=asset.redisNodesRequired", async () => {
    const user = userEvent.setup();
    const onValidity = vi.fn();
    render(<RedisConfigSection ctx={ctx} onValidityChange={onValidity} />);
    await user.click(screen.getByTestId("redis-mode-cluster"));

    expect(screen.getByTestId("redis-nodes-textarea")).toBeInTheDocument();
    expect(screen.queryByTestId("redis-host-input")).not.toBeInTheDocument();
    expect(lastValidity(onValidity)).toEqual({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.redisNodesRequired",
    });
  });

  it("哨兵模式:有节点但缺主节点名称时 saveDisabledReason=asset.redisMasterNameRequired", async () => {
    const user = userEvent.setup();
    const onValidity = vi.fn();
    render(<RedisConfigSection ctx={ctx} onValidityChange={onValidity} />);
    await user.click(screen.getByTestId("redis-mode-sentinel"));
    await user.type(screen.getByTestId("redis-nodes-textarea"), "10.0.0.31:26379");

    expect(screen.getByTestId("redis-master-name-input")).toBeInTheDocument();
    expect(screen.getByTestId("redis-sentinel-username-input")).toBeInTheDocument();
    expect(lastValidity(onValidity)).toEqual({
      canTest: false,
      canSave: false,
      saveDisabledReason: "asset.redisMasterNameRequired",
    });
  });

  it("节点地址映射格式错误只禁保存,不禁测试", async () => {
    const user = userEvent.setup();
    const onValidity = vi.fn();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}',
    });
    render(<RedisConfigSection editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await user.click(screen.getByTestId("config-tab-advanced"));
    await user.type(screen.getByTestId("redis-node-address-map-textarea"), "not-a-valid-line");

    const v = lastValidity(onValidity);
    expect(v.canTest).toBe(true);
    expect(v.canSave).toBe(false);
    expect(v.saveDisabledReason).toBe("asset.redisMappingLineInvalid");
  });
});

describe("RedisConfigSection.startTest (RedisProbe)", () => {
  it("单机模式测试成功:successDetail 为空(壳只出通用「连接成功」)", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({ modeMismatch: false } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"127.0.0.1","port":6379}' });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const attempt = ref.current!.startTest!(ctx);
    const result = await attempt.result;
    expect(result.successDetail).toBeUndefined();
    expect(RedisProbe).toHaveBeenCalledWith(expect.any(String), expect.stringContaining('"host":"127.0.0.1"'), "", "");
  });

  it("集群模式测试成功:successDetail 走 redisTestClusterDetail key", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      cluster: { state: "ok", masters: 3, replicas: 3, unreachableNodes: [] },
    } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}',
    });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successDetail).toBe("asset.redisTestClusterDetail");
  });

  it("哨兵模式测试成功:successDetail 走 redisTestSentinelDetail key", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: { authRequired: false, groups: [], masterAddr: "10.0.0.42:6379", otherSentinels: [] },
    } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"sentinel","nodes":["10.0.0.31:26379"],"master_name":"mymaster"}',
    });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successDetail).toBe("asset.redisTestSentinelDetail");
  });

  it("哨兵需要单独密码:测试失败,errorMessage 给出提示,认证块高亮", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: { authRequired: true, groups: [], masterAddr: "", otherSentinels: [] },
    } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"sentinel","nodes":["10.0.0.31:26379"],"master_name":"mymaster"}',
    });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const attempt = ref.current!.startTest!(ctx);
    await expect(attempt.result).rejects.toThrow();
    let caught: unknown;
    try {
      await attempt.result;
    } catch (e) {
      caught = e;
    }
    expect(attempt.errorMessage?.(caught)).toBe("asset.redisSentinelAuthRequired");
  });

  it("cancel() 在探测已发出后调用后端 CancelTest(testID)", async () => {
    let resolveProbe!: (v: redis_svc.RedisProbeResult) => void;
    vi.mocked(RedisProbe).mockImplementation(
      () =>
        new Promise<redis_svc.RedisProbeResult>((resolve) => {
          resolveProbe = resolve;
        })
    );
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"127.0.0.1","port":6379}' });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const attempt = ref.current!.startTest!(ctx);
    await waitFor(() => expect(RedisProbe).toHaveBeenCalled());
    const testID = vi.mocked(RedisProbe).mock.calls[0][0];
    attempt.cancel();
    expect(CancelTest).toHaveBeenCalledWith(testID);
    resolveProbe({ modeMismatch: false } as redis_svc.RedisProbeResult);
    await expect(attempt.result).rejects.toThrow("cancelled");
  });
});

describe("RedisConfigSection 自动识别:模式切换 / 哨兵组读取 / 补全 / 生成映射", () => {
  it("单机测试探测到集群节点:显示切换横幅,点击后切到集群模式并以当前 host:port 作第一个种子节点", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe).mockResolvedValue({ modeMismatch: true, detectedMode: "cluster" } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"10.20.0.11","port":6379}' });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    await act(async () => {
      await ref.current!.startTest!(ctx).result;
    });
    const switchButton = await screen.findByTestId("redis-switch-mode-button");
    await user.click(switchButton);

    expect(screen.getByTestId("redis-nodes-textarea")).toHaveValue("10.20.0.11:6379");
    expect(screen.getByTestId("redis-mode-cluster")).toHaveAttribute("data-state", "active");
  });

  it("StrictMode(开发态双挂载)下探测结果仍会显示切换横幅", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({ modeMismatch: true, detectedMode: "cluster" } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"10.20.0.11","port":6379}' });
    render(
      <StrictMode>
        <RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />
      </StrictMode>
    );

    await act(async () => {
      await ref.current!.startTest!(ctx).result;
    });
    expect(await screen.findByTestId("redis-switch-mode-button")).toBeInTheDocument();
  });

  it("从哨兵读取:只有一个组且主节点名称为空时自动填入", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: {
        authRequired: false,
        groups: [{ name: "mymaster", masterAddr: "10.20.0.42:6379", replicas: 2 }],
        masterAddr: "",
        otherSentinels: [],
      },
    } as never);
    render(<RedisConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    await user.click(screen.getByTestId("redis-mode-sentinel"));
    await user.type(screen.getByTestId("redis-nodes-textarea"), "10.20.0.31:26379");
    await user.click(screen.getByTestId("redis-read-sentinel-button"));

    await waitFor(() => expect(screen.getByTestId("redis-master-name-input")).toHaveValue("mymaster"));
  });

  it("补全:把哨兵报告的其它节点追加到节点列表(已存在的不重复追加)", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: {
        authRequired: false,
        groups: [{ name: "mymaster", masterAddr: "10.20.0.42:6379", replicas: 2 }],
        masterAddr: "",
        otherSentinels: ["10.20.0.32:26379", "10.20.0.33:26379"],
      },
    } as never);
    render(<RedisConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    await user.click(screen.getByTestId("redis-mode-sentinel"));
    await user.type(screen.getByTestId("redis-nodes-textarea"), "10.20.0.31:26379");
    await user.click(screen.getByTestId("redis-read-sentinel-button"));

    const completeButton = await screen.findByTestId("redis-complete-sentinels-button");
    await user.click(completeButton);

    expect(screen.getByTestId("redis-nodes-textarea")).toHaveValue(
      "10.20.0.31:26379\n10.20.0.32:26379\n10.20.0.33:26379"
    );
  });

  it("生成映射:把未映射的不可达地址填入左侧,右侧留空;已列出的地址不重复追加", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      cluster: {
        state: "ok",
        masters: 3,
        replicas: 0,
        unreachableNodes: ["172.18.0.11:6379", "172.18.0.12:6379"],
      },
    } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"],"node_address_map":{"172.18.0.11:6379":"10.20.0.5:7001"}}',
    });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    await act(async () => {
      await ref.current!.startTest!(ctx).result;
    });
    await user.click(screen.getByTestId("config-tab-advanced"));
    const generateButton = await screen.findByTestId("redis-generate-mapping-button");
    await user.click(generateButton);

    expect(screen.getByTestId("redis-node-address-map-textarea")).toHaveValue(
      "172.18.0.11:6379 = 10.20.0.5:7001\n172.18.0.12:6379 = "
    );
  });
});

describe("RedisConfigSection 自动识别:规格补全", () => {
  it("集群测试有不可达节点:successDetail 走「种子节点可连 · N 个节点不可达」", async () => {
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      cluster: { state: "ok", masters: 3, replicas: 3, unreachableNodes: ["172.18.0.11:6379"] },
    } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}',
    });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);

    const result = await ref.current!.startTest!(ctx).result;
    expect(result.successDetail).toBe("asset.redisTestClusterUnreachableDetail");
  });

  it("切换到集群模式:当前 host:port 排在已有种子节点之前,并按集群模式继续识别", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe)
      .mockResolvedValueOnce({ modeMismatch: true, detectedMode: "cluster" } as never)
      .mockResolvedValueOnce({
        modeMismatch: false,
        cluster: { state: "ok", masters: 3, replicas: 0, unreachableNodes: ["172.18.0.11:6379"] },
      } as never);
    const ref = createRef<AssetFormHandle>();
    const editAsset = new asset_entity.Asset({ Type: "redis", Config: '{"host":"10.20.0.11","port":6379}' });
    render(<RedisConfigSection ref={ref} editAsset={editAsset} ctx={ctx} onValidityChange={vi.fn()} />);
    await user.click(screen.getByTestId("redis-mode-cluster"));
    await user.type(screen.getByTestId("redis-nodes-textarea"), "10.20.0.12:6379");
    await user.click(screen.getByTestId("redis-mode-standalone"));

    await act(async () => {
      await ref.current!.startTest!(ctx).result;
    });
    await user.click(await screen.findByTestId("redis-switch-mode-button"));

    expect(screen.getByTestId("redis-nodes-textarea")).toHaveValue("10.20.0.11:6379\n10.20.0.12:6379");
    await waitFor(() => expect(RedisProbe).toHaveBeenCalledTimes(2));
    expect(vi.mocked(RedisProbe).mock.calls[1][1]).toContain('"mode":"cluster"');
    await user.click(screen.getByTestId("config-tab-advanced"));
    expect(await screen.findByTestId("redis-generate-mapping-button")).toBeInTheDocument();
  });

  it("从哨兵读取时哨兵需要认证:提示「哨兵需要单独的密码」并聚焦哨兵密码", async () => {
    const user = userEvent.setup();
    vi.mocked(RedisProbe).mockResolvedValue({
      modeMismatch: false,
      sentinel: { authRequired: true, groups: [], masterAddr: "", otherSentinels: [] },
    } as never);
    render(<RedisConfigSection ctx={ctx} onValidityChange={vi.fn()} />);
    await user.click(screen.getByTestId("redis-mode-sentinel"));
    await user.type(screen.getByTestId("redis-nodes-textarea"), "10.20.0.31:26379");
    await user.click(screen.getByTestId("redis-read-sentinel-button"));

    expect(await screen.findByText("asset.redisSentinelAuthRequired")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("redis-sentinel-password-input")).toHaveFocus());
  });

  it("节点地址映射某一行不是 host:port:禁止保存并指出该行", async () => {
    const user = userEvent.setup();
    const onValidity = vi.fn();
    const editAsset = new asset_entity.Asset({
      Type: "redis",
      Config: '{"mode":"cluster","nodes":["10.0.0.1:7001"]}',
    });
    render(<RedisConfigSection editAsset={editAsset} ctx={ctx} onValidityChange={onValidity} />);
    await user.click(screen.getByTestId("config-tab-advanced"));
    await user.type(
      screen.getByTestId("redis-node-address-map-textarea"),
      "172.18.0.11:6379 = 10.0.0.5:7001\n172.18.0.12 = 10.0.0.6:7002"
    );

    const v = lastValidity(onValidity);
    expect(v.canSave).toBe(false);
    expect(v.saveDisabledReason).toBe("asset.redisMappingLineInvalid");
  });
});
