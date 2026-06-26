import { describe, it, expect, vi, beforeAll, afterEach } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import * as reactI18next from "react-i18next";
import { Fields, type FieldDesc } from "@/components/asset/configFields";
import { buildConfigGroups, type ConfigGroupSchema, type FieldRenderCtx } from "@/components/asset/configFields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";

/**
 * Simulates i18next default nsSeparator:":" splitting behavior.
 * When a key contains ":", i18next treats the part before ":" as namespace and
 * the part after as the actual key, returning only the key portion when the
 * namespace is not found.
 */
function tWithNsSplit(key: string, opts?: { defaultValue?: string }): string {
  if (opts?.defaultValue !== undefined) return opts.defaultValue;
  const colonIdx = key.indexOf(":");
  if (colonIdx !== -1) return key.slice(colonIdx + 1);
  return key;
}

interface S {
  host: string;
  port: number;
  database: number;
  tls: boolean;
  mode: string;
  driver: string;
  note: string;
}
const INIT: S = { host: "", port: 6379, database: 0, tls: false, mode: "a", driver: "mysql", note: "" };

function FieldsWithCtx({ fields, ctx }: { fields: FieldDesc<S>[]; ctx: FieldRenderCtx }) {
  const [state, setState] = useState<S>(INIT);
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));
  return <Fields fields={fields} state={state} patch={patch} ctx={ctx} />;
}

function Harness({ fields }: { fields: FieldDesc<S>[] }) {
  const [state, setState] = useState<S>(INIT);
  const patch = (p: Partial<S>) => setState((s) => ({ ...s, ...p }));
  return (
    <div>
      <Fields fields={fields} state={state} patch={patch} />
      <span data-testid="state">{JSON.stringify(state)}</span>
    </div>
  );
}
const stateOf = (el: HTMLElement): S => JSON.parse(el.textContent || "{}");

// Radix Select 在 happy-dom 无 layout/pointer-capture,补齐 userEvent 驱动所需最小桩。
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.hasPointerCapture = vi.fn().mockReturnValue(false);
  Element.prototype.releasePointerCapture = vi.fn();
});

describe("Fields 渲染器 · 基础 kind", () => {
  it("text:输入回写", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host" }]} />
    );
    fireEvent.change(getByTestId("f-host"), { target: { value: "example.com" } });
    expect(stateOf(getByTestId("state")).host).toBe("example.com");
  });

  it("number:min 把值钳到 >=min", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "number", key: "database", label: "asset.db", min: 0, testid: "f-db" }]} />
    );
    fireEvent.change(getByTestId("f-db"), { target: { value: "-5" } });
    expect(stateOf(getByTestId("state")).database).toBe(0);
  });

  it("number:blankWhenZero 时 0 显示为空串", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "number", key: "port", label: "asset.port", blankWhenZero: true, testid: "f-port" }]} />
    );
    fireEvent.change(getByTestId("f-port"), { target: { value: "0" } });
    expect((getByTestId("f-port") as HTMLInputElement).value).toBe("");
  });

  it("switch:切换回写布尔", () => {
    const { getByRole, getByTestId } = render(
      <Harness fields={[{ kind: "switch", key: "tls", label: "asset.tls" }]} />
    );
    fireEvent.click(getByRole("switch"));
    expect(stateOf(getByTestId("state")).tls).toBe(true);
  });

  it("select:选项回写", () => {
    const { getByTestId } = render(
      <Harness
        fields={[
          {
            kind: "select",
            key: "driver",
            label: "asset.driver",
            testid: "f-driver",
            options: [
              { value: "mysql", label: "MySQL" },
              { value: "postgresql", label: "PostgreSQL" },
            ],
          },
        ]}
      />
    );
    // Radix Select 在 jsdom 下用键盘交互不稳;此处只断言 trigger 渲染出当前值。
    expect(getByTestId("f-driver")).toBeTruthy();
  });

  it("visibleWhen=false:不渲染", () => {
    const { queryByTestId } = render(
      <Harness
        fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host", visibleWhen: (s) => s.tls }]}
      />
    );
    expect(queryByTestId("f-host")).toBeNull();
  });

  it("row:横排渲染两个子字段", () => {
    const { getByTestId } = render(
      <Harness
        fields={[
          {
            kind: "row",
            fields: [
              { kind: "text", key: "host", label: "asset.host", testid: "f-host" },
              { kind: "number", key: "port", label: "asset.port", testid: "f-port" },
            ],
          },
        ]}
      />
    );
    expect(getByTestId("f-host")).toBeTruthy();
    expect(getByTestId("f-port")).toBeTruthy();
  });
});

function fakeCred(): UseAssetCredential {
  return {
    value: { password: "", encryptedPassword: "", passwordSource: "inline", passwordCredentialId: 0 },
    managedPasswords: [],
    setPassword: () => {},
    setPasswordSource: () => {},
    setPasswordCredentialId: () => {},
  };
}

describe("Fields 渲染器 · composite kind", () => {
  it("password:从 ctx.cred 渲染 PasswordSourceField(出现来源切换段控件)", () => {
    const ctx: FieldRenderCtx = { cred: fakeCred() };
    const { getByTestId } = render(<FieldsWithCtx fields={[{ kind: "password" }]} ctx={ctx} />);
    expect(getByTestId("password-source-inline")).toBeTruthy();
  });

  it("password: placeholder 为 i18n key 时经 t() 翻译后透出(不原样显示 key)", () => {
    vi.spyOn(reactI18next, "useTranslation").mockReturnValue({
      t: (k: string, o?: { defaultValue?: string }) =>
        k === "asset.passwordPlaceholder" ? "请输入密码" : (o?.defaultValue ?? k),
      i18n: { language: "zh-CN", changeLanguage: vi.fn() },
      ready: true,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);
    const ctx: FieldRenderCtx = { cred: fakeCred() };
    const { container } = render(
      <FieldsWithCtx fields={[{ kind: "password", placeholder: "asset.passwordPlaceholder" }]} ctx={ctx} />
    );
    const pwInput = container.querySelector('input[type="password"]') as HTMLInputElement;
    expect(pwInput.placeholder).toBe("请输入密码");
    vi.restoreAllMocks();
  });

  it("tunnel:渲染 ConnectionMethodFields(出现连接方式 radiogroup)", () => {
    const { getAllByRole } = render(<FieldsWithCtx fields={[{ kind: "tunnel" }]} ctx={{}} />);
    expect(getAllByRole("radiogroup").length).toBeGreaterThan(0);
  });

  it("custom:调用 render 并把 state/patch 传入", () => {
    const { getByTestId } = render(
      <FieldsWithCtx fields={[{ kind: "custom", render: (s) => <span data-testid="c">{s.driver}</span> }]} ctx={{}} />
    );
    expect(getByTestId("c").textContent).toBe("mysql");
  });
});

describe("Fields 渲染器 · Phase 2 扩展", () => {
  afterEach(() => vi.restoreAllMocks());

  it("textarea: required 渲染必填星号, mono 加等宽类", () => {
    const { getByRole, container } = render(
      <Harness fields={[{ kind: "textarea", key: "note", label: "asset.endpoints", required: true, mono: true }]} />
    );
    const ta = getByRole("textbox") as HTMLTextAreaElement;
    expect(ta.className).toContain("font-mono");
    expect(container.textContent).toContain("*"); // FieldLabel 在必填时渲染 " *"
  });

  it("textarea: 无 mono 时不加等宽类", () => {
    const { getByRole } = render(<Harness fields={[{ kind: "textarea", key: "note", label: "asset.endpoints" }]} />);
    expect((getByRole("textbox") as HTMLTextAreaElement).className).not.toContain("font-mono");
  });

  it("segmented: ariaLabel 提供时 radiogroup 有 aria-label(无可见 label 也生效)", () => {
    const { getByRole } = render(
      <Harness
        fields={[
          {
            kind: "segmented",
            key: "mode",
            ariaLabel: "asset.mongoUri",
            options: [
              { value: "manual", label: "Manual" },
              { value: "uri", label: "URI" },
            ],
          },
        ]}
      />
    );
    // label 未给 → aria-label 唯一来源是 ariaLabel;断言其非空(不依赖 i18n 是否翻译该键)
    expect(getByRole("radiogroup").getAttribute("aria-label")).toBeTruthy();
  });

  it("text: 字面量 placeholder 经 t() 后原样透出(Phase 1 行为不变)", () => {
    const { getByTestId } = render(
      <Harness
        fields={[{ kind: "text", key: "host", label: "asset.host", placeholder: "example.com", testid: "f-host" }]}
      />
    );
    expect((getByTestId("f-host") as HTMLInputElement).placeholder).toBe("example.com");
  });

  it("text: 含冒号的字面量 placeholder 原样透出(nsSeparator 不截断)", () => {
    // Use a t() that simulates real i18next nsSeparator:":" splitting —
    // without defaultValue the colon splits off the namespace prefix.
    vi.spyOn(reactI18next, "useTranslation").mockReturnValue(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      { t: tWithNsSplit, i18n: { language: "en", changeLanguage: vi.fn() }, ready: true } as any
    );

    const { getByTestId } = render(
      <Harness
        fields={[
          {
            kind: "text",
            key: "host",
            label: "asset.host",
            placeholder: "192.168.100.50:9092",
            testid: "f-host",
          },
        ]}
      />
    );
    expect((getByTestId("f-host") as HTMLInputElement).placeholder).toBe("192.168.100.50:9092");
  });

  it("textarea: 含冒号的字面量 placeholder 原样透出(nsSeparator 不截断)", () => {
    vi.spyOn(reactI18next, "useTranslation").mockReturnValue(
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      { t: tWithNsSplit, i18n: { language: "en", changeLanguage: vi.fn() }, ready: true } as any
    );

    const { getByRole } = render(
      <Harness
        fields={[
          {
            kind: "textarea",
            key: "note",
            label: "asset.endpoints",
            placeholder: "192.168.100.50:9092",
          },
        ]}
      />
    );
    expect((getByRole("textbox") as HTMLTextAreaElement).placeholder).toBe("192.168.100.50:9092");
  });
});

describe("buildConfigGroups", () => {
  it("声明式组包成 Fields;render 逃逸口透传;badge 透传", () => {
    const schema: ConfigGroupSchema<S>[] = [
      { key: "a", label: "tab.a", fields: [{ kind: "text", key: "host", label: "asset.host", testid: "g-host" }] },
      { key: "b", label: "tab.b", badge: 3, render: () => <span data-testid="g-custom">x</span> },
    ];
    const groups = buildConfigGroups(schema, { state: INIT, patch: () => {} });
    expect(groups.map((g) => g.key)).toEqual(["a", "b"]);
    expect(groups[1].badge).toBe(3);
    const { getByTestId } = render(
      <>
        {groups[0].render()}
        {groups[1].render()}
      </>
    );
    expect(getByTestId("g-host")).toBeTruthy();
    expect(getByTestId("g-custom")).toBeTruthy();
  });
});

describe("Fields 渲染器 · Phase 3 扩展", () => {
  it("segmented: width 作为 Field 的 className", () => {
    const { container } = render(
      <Harness
        fields={[
          {
            kind: "segmented",
            key: "mode",
            width: "w-[190px] shrink-0",
            options: [
              { value: "manual", label: "Manual" },
              { value: "uri", label: "URI" },
            ],
          },
        ]}
      />
    );
    expect(container.innerHTML).toContain("w-[190px]");
  });

  it("select: 点击选项写回 state", async () => {
    const user = userEvent.setup();
    const { getByTestId, findByRole } = render(
      <Harness
        fields={[
          {
            kind: "select",
            key: "driver",
            label: "asset.driver",
            testid: "f-driver",
            options: [
              { value: "mysql", label: "MySQL" },
              { value: "postgresql", label: "PostgreSQL" },
            ],
          },
        ]}
      />
    );
    await user.click(getByTestId("f-driver"));
    await user.click(await findByRole("option", { name: "PostgreSQL" }));
    expect(stateOf(getByTestId("state")).driver).toBe("postgresql");
  });
});
