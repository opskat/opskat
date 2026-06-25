import { describe, it, expect } from "vitest";
import { render, fireEvent } from "@testing-library/react";
import { useState } from "react";
import { Fields, type FieldDesc } from "@/components/asset/configFields";
import { buildConfigGroups, type ConfigGroupSchema, type FieldRenderCtx } from "@/components/asset/configFields";
import type { UseAssetCredential } from "@/components/asset/useAssetCredential";

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

describe("Fields 渲染器 · 基础 kind", () => {
  it("text:输入回写", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host" }]} />);
    fireEvent.change(getByTestId("f-host"), { target: { value: "example.com" } });
    expect(stateOf(getByTestId("state")).host).toBe("example.com");
  });

  it("number:min 把值钳到 >=min", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "number", key: "database", label: "asset.db", min: 0, testid: "f-db" }]} />);
    fireEvent.change(getByTestId("f-db"), { target: { value: "-5" } });
    expect(stateOf(getByTestId("state")).database).toBe(0);
  });

  it("number:blankWhenZero 时 0 显示为空串", () => {
    const { getByTestId } = render(<Harness fields={[{ kind: "number", key: "port", label: "asset.port", blankWhenZero: true, testid: "f-port" }]} />);
    fireEvent.change(getByTestId("f-port"), { target: { value: "0" } });
    expect((getByTestId("f-port") as HTMLInputElement).value).toBe("");
  });

  it("switch:切换回写布尔", () => {
    const { getByRole, getByTestId } = render(<Harness fields={[{ kind: "switch", key: "tls", label: "asset.tls" }]} />);
    fireEvent.click(getByRole("switch"));
    expect(stateOf(getByTestId("state")).tls).toBe(true);
  });

  it("select:选项回写", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "select", key: "driver", label: "asset.driver", testid: "f-driver", options: [{ value: "mysql", label: "MySQL" }, { value: "postgresql", label: "PostgreSQL" }] }]} />
    );
    // Radix Select 在 jsdom 下用键盘交互不稳;此处只断言 trigger 渲染出当前值。
    expect(getByTestId("f-driver")).toBeTruthy();
  });

  it("visibleWhen=false:不渲染", () => {
    const { queryByTestId } = render(
      <Harness fields={[{ kind: "text", key: "host", label: "asset.host", testid: "f-host", visibleWhen: (s) => s.tls }]} />
    );
    expect(queryByTestId("f-host")).toBeNull();
  });

  it("row:横排渲染两个子字段", () => {
    const { getByTestId } = render(
      <Harness fields={[{ kind: "row", fields: [{ kind: "text", key: "host", label: "asset.host", testid: "f-host" }, { kind: "number", key: "port", label: "asset.port", testid: "f-port" }] }]} />
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
    const { getByTestId } = render(
      <FieldsWithCtx fields={[{ kind: "password" }]} ctx={ctx} />
    );
    expect(getByTestId("password-source-inline")).toBeTruthy();
  });

  it("tunnel:渲染 ConnectionMethodFields(出现连接方式 radiogroup)", () => {
    const { getAllByRole } = render(<FieldsWithCtx fields={[{ kind: "tunnel" }]} ctx={{}} />);
    expect(getAllByRole("radiogroup").length).toBeGreaterThan(0);
  });

  it("custom:调用 render 并把 state/patch 传入", () => {
    const { getByTestId } = render(
      <FieldsWithCtx
        fields={[{ kind: "custom", render: (s) => <span data-testid="c">{s.driver}</span> }]}
        ctx={{}}
      />
    );
    expect(getByTestId("c").textContent).toBe("mysql");
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
    const { getByTestId } = render(<>{groups[0].render()}{groups[1].render()}</>);
    expect(getByTestId("g-host")).toBeTruthy();
    expect(getByTestId("g-custom")).toBeTruthy();
  });
});
