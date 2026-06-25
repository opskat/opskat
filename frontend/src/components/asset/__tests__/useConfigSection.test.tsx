import { describe, it, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { createRef, useState, type Ref } from "react";
import { useConfigSection } from "@/components/asset/useConfigSection";
import type { AssetFormHandle, AssetFormContext, SectionValidity } from "@/lib/assetTypes/formContract";

interface S {
  host: string;
  port: number;
}
const ctx: AssetFormContext = { isEdit: false, encryptPassword: async (p) => `enc(${p})` };

function Harness({
  refOut,
  onValidityChange,
  withTest,
}: {
  refOut: Ref<AssetFormHandle>;
  onValidityChange: (v: SectionValidity) => void;
  withTest?: boolean;
}) {
  const { state, patch } = useConfigSection<S>({
    ref: refOut,
    onValidityChange,
    init: () => ({ host: "", port: 6379 }),
    validate: (s) => ({ canTest: !!s.host, canSave: !!s.host, saveDisabledReason: s.host ? "" : "missing" }),
    build: async (s) => ({ configJSON: JSON.stringify(s), sshTunnelId: 0 }),
    buildTest: withTest ? async (s) => ({ assetType: "x", configJSON: JSON.stringify(s), password: "" }) : undefined,
  });
  return (
    <div>
      <span data-testid="host">{state.host}</span>
      <button data-testid="set-port" onClick={() => patch({ port: state.port + 1 })} />
      <button data-testid="set-host" onClick={() => patch({ host: "h" })} />
    </div>
  );
}

describe("useConfigSection", () => {
  it("挂载即上报一次校验", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={onValidity} />);
    expect(onValidity).toHaveBeenCalledTimes(1);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: false, canSave: false, saveDisabledReason: "missing" });
  });

  it("校验结果不变则不重复上报(无关字段变更被守卫)", () => {
    const onValidity = vi.fn();
    const ref = createRef<AssetFormHandle>();
    const { getByTestId } = render(<Harness refOut={ref} onValidityChange={onValidity} />);
    onValidity.mockClear();
    fireEvent.click(getByTestId("set-port")); // port 变,validity 不变
    expect(onValidity).not.toHaveBeenCalled();
    fireEvent.click(getByTestId("set-host")); // host 变,validity 变
    expect(onValidity).toHaveBeenCalledTimes(1);
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });

  it("buildConfig 读到最新 state", async () => {
    const ref = createRef<AssetFormHandle>();
    const { getByTestId } = render(<Harness refOut={ref} onValidityChange={() => {}} />);
    fireEvent.click(getByTestId("set-host"));
    const built = await ref.current!.buildConfig(ctx);
    expect(built).toEqual({ configJSON: JSON.stringify({ host: "h", port: 6379 }), sshTunnelId: 0 });
  });

  it("buildTest 省略时 buildTestConfig 为 null", () => {
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={() => {}} />);
    expect(ref.current!.buildTestConfig).toBeNull();
  });

  it("buildTest 提供时 buildTestConfig 可调用", async () => {
    const ref = createRef<AssetFormHandle>();
    render(<Harness refOut={ref} onValidityChange={() => {}} withTest />);
    const tc = await ref.current!.buildTestConfig!(ctx);
    expect(tc).toEqual({ assetType: "x", configJSON: JSON.stringify({ host: "", port: 6379 }), password: "" });
  });
});

describe("useConfigSection validityDeps", () => {
  it("validityDeps 变化(state 不变)时按需重新上报校验", () => {
    const onValidity = vi.fn();

    function Harness() {
      const ref = createRef<AssetFormHandle>();
      const [blocked, setBlocked] = useState(true);
      useConfigSection<{ x: number }>({
        ref,
        onValidityChange: onValidity,
        init: () => ({ x: 0 }),
        // validate 读外部 blocked(经闭包),不改 state。
        validate: () => ({ canTest: !blocked, canSave: !blocked, saveDisabledReason: blocked ? "blocked" : "" }),
        build: async () => ({ configJSON: "{}", sshTunnelId: 0 }),
        validityDeps: [blocked],
      });
      return (
        <button data-testid="unblock" onClick={() => setBlocked(false)}>
          unblock
        </button>
      );
    }

    const { getByTestId } = render(<Harness />);
    // 挂载即上报一次(blocked)。
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: false, canSave: false, saveDisabledReason: "blocked" });

    // blocked 翻转(state 未变)→ validityDeps 变化 → 重新上报。
    fireEvent.click(getByTestId("unblock"));
    expect(onValidity).toHaveBeenLastCalledWith({ canTest: true, canSave: true, saveDisabledReason: "" });
  });
});
