import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { createRef } from "react";
import { vi } from "vitest";
import { buildLocalConfig, parseLocalConfig, LOCAL_DEFAULTS } from "@/components/asset/LocalConfigSection.config";
import { LocalConfigSection } from "@/components/asset/LocalConfigSection";
import type { AssetFormHandle, AssetFormContext } from "@/lib/assetTypes/formContract";
import { asset_entity } from "../../../../wailsjs/go/models";

vi.mock("../../../../wailsjs/go/local/Local", () => ({ ListLocalShells: () => Promise.resolve([]) }));

const fakeCtx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => p };

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
