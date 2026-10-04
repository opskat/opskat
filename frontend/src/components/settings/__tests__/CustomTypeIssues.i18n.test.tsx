import { describe, it, expect, vi, beforeAll, beforeEach } from "vitest";
import fs from "node:fs";
import path from "node:path";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import i18nextFactory, { type i18n as I18n } from "i18next";
import { I18nextProvider, initReactI18next } from "react-i18next";
import enCommon from "@/i18n/locales/en/common.json";
import zhCommon from "@/i18n/locales/zh-CN/common.json";
import { CustomTypeEditorDialog } from "@/components/settings/CustomTypeEditorDialog";
import { ImportCustomTypeDialog } from "@/components/settings/ImportCustomTypeDialog";
import { customtype } from "../../../../wailsjs/go/models";

// 后端校验问题只带 Code + Params,文案由前端按界面语言翻译(英文界面不能冒出中文,
// 也不能出现 "authtmpl:" 这类内部前缀)。setup.ts 的 t = key => key 看不出最终文案,
// 这里用真实 i18next + 两份语言包渲染。
vi.unmock("react-i18next");
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const instances: Record<string, I18n> = {};

beforeAll(async () => {
  for (const [lng, common] of [
    ["en", enCommon],
    ["zh-CN", zhCommon],
  ] as const) {
    const inst = i18nextFactory.createInstance();
    await inst.use(initReactI18next).init({
      resources: { [lng]: { common } },
      lng,
      fallbackLng: lng,
      defaultNS: "common",
      interpolation: { escapeValue: false },
    });
    instances[lng] = inst;
  }
});

const HAN = /\p{Script=Han}/u;

describe("custom type validation issues follow the UI language", () => {
  beforeEach(async () => {
    const mod = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(mod.SaveCustomType).mockReset();
  });

  it("type editor: renders translated issues next to their rows", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue(
      new customtype.SaveResult({
        issues: [
          { path: "fields[0].name", code: "field_name_duplicate", params: { name: "host" } },
          { path: "slug", code: "slug_taken", params: { slug: "grafana", name: "Grafana" } },
        ],
      })
    );

    for (const [lng, fieldText, slugText] of [
      ["en", 'Field name "host" is already used', '"grafana" is already taken by the custom type "Grafana"'],
      ["zh-CN", "字段名「host」重复", "标识「grafana」已被自定义类型「Grafana」占用"],
    ] as const) {
      const { unmount } = render(
        <I18nextProvider i18n={instances[lng]}>
          <CustomTypeEditorDialog open onOpenChange={vi.fn()} />
        </I18nextProvider>
      );
      await userEvent.setup().click(screen.getByRole("button", { name: instances[lng].t("action.save") }));
      expect(await screen.findByText(fieldText)).toBeInTheDocument();
      expect(screen.getByText(slugText)).toBeInTheDocument();
      unmount();
    }
  });

  it("type editor: template parse errors carry no authtmpl: prefix and stay English in the English UI", async () => {
    const { SaveCustomType } = await import("../../../../wailsjs/go/customtype/CustomType");
    vi.mocked(SaveCustomType).mockResolvedValue(
      new customtype.SaveResult({
        issues: [
          { path: "http.base_url", code: "template.unknown_field", params: { name: "hostname", expr: "hostname" } },
        ],
      })
    );

    render(
      <I18nextProvider i18n={instances.en}>
        <CustomTypeEditorDialog open onOpenChange={vi.fn()} />
      </I18nextProvider>
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await user.click(screen.getByTestId("config-tab-request"));

    const msg = await screen.findByText(/hostname/);
    expect(msg.textContent).not.toMatch(/authtmpl/);
    expect(msg.textContent).not.toMatch(HAN);
  });

  it("import preview: rejection reasons are translated", () => {
    const preview = new customtype.ImportPreview({
      issues: [{ path: "format", code: "version_unsupported", params: { version: "99", supported: "1" } }],
      slugTaken: false,
    });

    const { unmount } = render(
      <I18nextProvider i18n={instances.en}>
        <ImportCustomTypeDialog preview={preview} onOpenChange={vi.fn()} />
      </I18nextProvider>
    );
    const en = screen.getByText(/99/);
    expect(en.textContent).toContain("1");
    expect(en.textContent).not.toMatch(HAN);
    unmount();

    render(
      <I18nextProvider i18n={instances["zh-CN"]}>
        <ImportCustomTypeDialog preview={preview} onOpenChange={vi.fn()} />
      </I18nextProvider>
    );
    expect(screen.getByText(/99/).textContent).toMatch(HAN);
  });
});

// 问题码在 Go 里定义(custom_type_entity.Issue* 与 authtmpl.Code*),文案在两份语言包里:
// 两边是独立维护的,后端新增 / 改名一个问题码而语言包没跟上时,界面只会显示键名。
describe("every backend issue code has a translation", () => {
  function goConstants(file: string, prefix: string): string[] {
    const source = fs.readFileSync(path.resolve(process.cwd(), "..", file), "utf8");
    const re = new RegExp(`^\\s+${prefix}[A-Za-z]+\\s*=\\s*"([^"]+)"`, "gm");
    return [...source.matchAll(re)].map((m) => m[1]);
  }

  const issueCodes = goConstants("internal/model/entity/custom_type_entity/custom_type.go", "Issue").filter(
    (c) => c !== "template."
  );
  const templateCodes = goConstants("internal/pkg/authtmpl/parse_error.go", "Code").map((c) => `template.${c}`);

  it("finds the Go constants it checks", () => {
    expect(issueCodes).toContain("field_name_duplicate");
    expect(templateCodes).toContain("template.unknown_field");
  });

  it.each([
    ["en", enCommon],
    ["zh-CN", zhCommon],
  ] as const)("%s", (_lng, common) => {
    const issue = (common.customType as Record<string, unknown>).issue as Record<string, unknown> | undefined;
    const has = (code: string) =>
      typeof code.split(".").reduce<unknown>((node, k) => (node as Record<string, unknown> | undefined)?.[k], issue) ===
      "string";
    expect([...issueCodes, ...templateCodes].filter((c) => !has(c))).toEqual([]);
  });
});
